package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aceld/zinx/ziface"
	"github.com/aceld/zinx/zlog"
	"github.com/aceld/zinx/znet"
	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/protocol"
)

type config struct {
	Mode              string
	GatewayHost       string
	GatewayPort       int
	InternalURL       string
	InternalToken     string
	Token             string
	ClientPrefix      string
	DevicePrefix      string
	Clients           int
	MessagesPerClient int
	HTTPConcurrency   int
	UpstreamMsgID     uint
	DownlinkMsgID     uint
	BodySize          int
	Timeout           time.Duration
	RunDuration       time.Duration
	Rate              int
	EnableZinxLog     bool
}

type counters struct {
	bindAccepted     atomic.Int64
	bindRejected     atomic.Int64
	upstreamAccepted atomic.Int64
	upstreamRejected atomic.Int64
	downlinkSuccess  atomic.Int64
	downlinkRejected atomic.Int64
	sendErrors       atomic.Int64
	decodeErrors     atomic.Int64

	bindLatency     latencyRecorder
	upstreamLatency latencyRecorder
	downlinkLatency latencyRecorder
	failures        reasonCounts
}

func main() {
	cfg := parseFlags()
	if !cfg.EnableZinxLog {
		zlog.SetLogger(nopZinxLogger{})
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.OverallTimeout())
	defer cancel()

	startedAt := time.Now()
	counts := &counters{}
	var err error
	switch cfg.Mode {
	case "upstream":
		err = runUpstream(ctx, cfg, counts)
	case "downlink":
		err = runDownlink(ctx, cfg, counts)
	default:
		err = fmt.Errorf("unsupported mode %q", cfg.Mode)
	}

	printSummary(cfg, counts, time.Since(startedAt))
	if err != nil {
		fmt.Fprintf(os.Stderr, "loadtest failed: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	var cfg config
	fs := flag.NewFlagSet("loadtest", flag.ExitOnError)
	fs.StringVar(&cfg.Mode, "mode", "upstream", "load mode: upstream or downlink")
	fs.StringVar(&cfg.GatewayHost, "host", "127.0.0.1", "gateway TCP host")
	fs.IntVar(&cfg.GatewayPort, "port", 8999, "gateway TCP port")
	fs.StringVar(&cfg.InternalURL, "internal-url", "http://127.0.0.1:18080", "gateway internal HTTP base URL")
	fs.StringVar(&cfg.InternalToken, "internal-token", "dev-internal-token", "gateway internal HTTP token")
	fs.StringVar(&cfg.Token, "token", "dev-token", "client auth token")
	fs.StringVar(&cfg.ClientPrefix, "client-prefix", "load-client", "client id prefix")
	fs.StringVar(&cfg.DevicePrefix, "device-prefix", "device", "device id prefix")
	fs.IntVar(&cfg.Clients, "clients", 100, "number of simulated clients")
	fs.IntVar(&cfg.MessagesPerClient, "messages", 10, "messages per client")
	fs.IntVar(&cfg.HTTPConcurrency, "http-concurrency", 50, "downlink HTTP worker concurrency")
	fs.UintVar(&cfg.UpstreamMsgID, "upstream-msg-id", 1001, "upstream business message id")
	fs.UintVar(&cfg.DownlinkMsgID, "downlink-msg-id", 2001, "downlink message id")
	fs.IntVar(&cfg.BodySize, "body-size", 128, "message body size in bytes")
	fs.DurationVar(&cfg.Timeout, "timeout", 30*time.Second, "one-shot overall timeout, or duration-mode drain timeout")
	fs.DurationVar(&cfg.RunDuration, "duration", 0, "continuous load duration; 0 sends clients*messages and exits")
	fs.IntVar(&cfg.Rate, "rate", 0, "target messages per second in duration mode")
	fs.BoolVar(&cfg.EnableZinxLog, "zinx-log", false, "enable Zinx internal logs")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: loadtest [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	if cfg.Clients <= 0 {
		cfg.Clients = 1
	}
	if cfg.MessagesPerClient < 0 {
		cfg.MessagesPerClient = 0
	}
	if cfg.HTTPConcurrency <= 0 {
		cfg.HTTPConcurrency = 1
	}
	if cfg.BodySize < 0 {
		cfg.BodySize = 0
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.RunDuration < 0 {
		fmt.Fprintln(os.Stderr, "-duration must be greater than or equal to 0")
		os.Exit(2)
	}
	if cfg.DurationMode() && cfg.Rate <= 0 {
		fmt.Fprintln(os.Stderr, "-rate must be greater than 0 when -duration is set")
		os.Exit(2)
	}

	return cfg
}

func (cfg config) DurationMode() bool {
	return cfg.RunDuration > 0
}

func (cfg config) OverallTimeout() time.Duration {
	if !cfg.DurationMode() {
		return cfg.Timeout
	}

	return cfg.RunDuration + cfg.Timeout
}

func runUpstream(ctx context.Context, cfg config, counts *counters) error {
	expected := int64(cfg.Clients * cfg.MessagesPerClient)
	if cfg.DurationMode() {
		expected = -1
	}
	done := make(chan struct{})
	var doneOnce sync.Once
	if expected == 0 {
		close(done)
	}
	state := &upstreamState{
		config:   cfg,
		counts:   counts,
		expected: expected,
		done:     done,
		doneOnce: &doneOnce,
		body:     payload(cfg.BodySize),
		bound:    make(chan upstreamSender, cfg.Clients),
	}

	clients := make([]ziface.IClient, 0, cfg.Clients)
	for i := 0; i < cfg.Clients; i++ {
		clientID := fmt.Sprintf("%s-%d", cfg.ClientPrefix, i)
		deviceID := fmt.Sprintf("%s-%d", cfg.DevicePrefix, i)
		bindMessageID := fmt.Sprintf("load-bind-%d-%d", i, time.Now().UnixNano())
		client := znet.NewClient(cfg.GatewayHost, cfg.GatewayPort)
		client.AddRouter(protocol.MsgIDAck, &ackRouter{
			state:         state,
			clientID:      clientID,
			deviceID:      deviceID,
			bindMessageID: bindMessageID,
		})
		client.SetOnConnStart(func(conn ziface.IConnection) {
			state.rememberSent(bindMessageID)
			if err := sendPacket(conn, packetInput{
				msgID:     protocol.MsgIDBind,
				clientID:  clientID,
				deviceID:  deviceID,
				token:     cfg.Token,
				messageID: bindMessageID,
				body:      []byte("load-bind"),
			}); err != nil {
				state.forgetSent(bindMessageID)
				counts.sendErrors.Add(1)
				counts.bindRejected.Add(1)
				counts.failures.Add("bind_send_error")
				if !cfg.DurationMode() {
					state.markUpstreamSkipped(cfg.MessagesPerClient)
				}
				conn.Stop()
			}
		})
		client.Start()
		clients = append(clients, client)
	}
	defer stopClients(clients)

	if cfg.DurationMode() {
		return state.runContinuous(ctx)
	}

	select {
	case <-ctx.Done():
		counts.failures.Add(reasonFromError(ctx.Err()))
		return ctx.Err()
	case <-done:
		return nil
	}
}

type upstreamState struct {
	config   config
	counts   *counters
	expected int64
	done     chan struct{}
	doneOnce *sync.Once
	body     []byte
	sentAt   sync.Map
	bound    chan upstreamSender
	inFlight atomic.Int64
	stopped  atomic.Bool
}

type upstreamSender struct {
	conn     ziface.IConnection
	clientID string
	deviceID string
}

func (s *upstreamState) rememberSent(messageID string) {
	if messageID == "" {
		return
	}

	s.sentAt.Store(messageID, time.Now())
}

func (s *upstreamState) forgetSent(messageID string) {
	if messageID == "" {
		return
	}

	s.sentAt.Delete(messageID)
}

func (s *upstreamState) observeLatency(messageID string, recorder *latencyRecorder) {
	if messageID == "" || recorder == nil {
		return
	}

	value, ok := s.sentAt.LoadAndDelete(messageID)
	if !ok {
		return
	}
	sentAt, ok := value.(time.Time)
	if !ok || sentAt.IsZero() {
		return
	}

	recorder.Record(time.Since(sentAt))
}

func (s *upstreamState) markUpstreamAck(ack protocol.Ack) {
	s.observeLatency(ack.MessageID, &s.counts.upstreamLatency)
	if ack.Code == protocol.AckAccepted {
		s.counts.upstreamAccepted.Add(1)
	} else {
		s.markUpstreamRejected(classifyAck("upstream", ack))
		return
	}

	if s.durationMode() {
		s.inFlight.Add(-1)
	}
	s.closeWhenExpectedReached()
}

func (s *upstreamState) markUpstreamRejected(reason string) {
	s.counts.upstreamRejected.Add(1)
	s.counts.failures.Add(reason)
	if s.durationMode() {
		s.inFlight.Add(-1)
	}
	s.closeWhenExpectedReached()
}

func (s *upstreamState) markUpstreamSkipped(count int) {
	if count <= 0 {
		return
	}

	s.counts.upstreamRejected.Add(int64(count))
	s.closeWhenExpectedReached()
}

func (s *upstreamState) closeWhenExpectedReached() {
	if s.durationMode() {
		if s.stopped.Load() && s.inFlight.Load() <= 0 {
			s.doneOnce.Do(func() { close(s.done) })
		}
		return
	}

	total := s.counts.upstreamAccepted.Load() + s.counts.upstreamRejected.Load()
	if total >= s.expected {
		s.doneOnce.Do(func() { close(s.done) })
	}
}

func (s *upstreamState) durationMode() bool {
	return s.expected < 0
}

func (s *upstreamState) addSender(sender upstreamSender) {
	if !s.durationMode() {
		return
	}
	if sender.conn == nil {
		return
	}

	select {
	case s.bound <- sender:
	default:
		s.counts.failures.Add("sender_queue_full")
	}
}

func (s *upstreamState) runContinuous(ctx context.Context) error {
	ticker := time.NewTicker(rateInterval(s.config.Rate))
	defer ticker.Stop()

	durationTimer := time.NewTimer(s.config.RunDuration)
	defer durationTimer.Stop()

	var senders []upstreamSender
	nextSender := 0
	messageSeq := int64(0)

	for {
		select {
		case <-ctx.Done():
			s.counts.failures.Add(reasonFromError(ctx.Err()))
			return ctx.Err()
		case sender := <-s.bound:
			senders = append(senders, sender)
		case <-ticker.C:
			if len(senders) == 0 {
				continue
			}
			sender := senders[nextSender%len(senders)]
			nextSender++
			seq := atomic.AddInt64(&messageSeq, 1)
			s.sendContinuous(sender, seq)
		case <-durationTimer.C:
			s.stopped.Store(true)
			s.closeWhenExpectedReached()
			select {
			case <-ctx.Done():
				s.counts.failures.Add(reasonFromError(ctx.Err()))
				return ctx.Err()
			case <-s.done:
				return nil
			}
		}
	}
}

func (s *upstreamState) sendContinuous(sender upstreamSender, seq int64) {
	messageID := fmt.Sprintf("load-upstream-%s-%d-%d", sender.deviceID, seq, time.Now().UnixNano())
	s.rememberSent(messageID)
	s.inFlight.Add(1)
	if err := sendPacket(sender.conn, packetInput{
		msgID:     uint32(s.config.UpstreamMsgID),
		clientID:  sender.clientID,
		deviceID:  sender.deviceID,
		token:     s.config.Token,
		messageID: messageID,
		body:      s.body,
	}); err != nil {
		s.forgetSent(messageID)
		s.counts.sendErrors.Add(1)
		s.markUpstreamRejected("upstream_send_error")
	}
}

type ackRouter struct {
	znet.BaseRouter

	state         *upstreamState
	clientID      string
	deviceID      string
	bindMessageID string
	sendOnce      sync.Once
}

func (r *ackRouter) Handle(request ziface.IRequest) {
	packet, err := protocol.Decode(request.GetData())
	if err != nil {
		r.state.counts.decodeErrors.Add(1)
		r.state.counts.failures.Add("decode_error")
		return
	}

	var ack protocol.Ack
	if err := sonic.Unmarshal(packet.Body, &ack); err != nil {
		r.state.counts.decodeErrors.Add(1)
		r.state.counts.failures.Add("decode_error")
		return
	}

	accepted := ack.Code == protocol.AckAccepted
	if ack.MessageID == r.bindMessageID {
		r.state.observeLatency(ack.MessageID, &r.state.counts.bindLatency)
		if accepted {
			r.state.counts.bindAccepted.Add(1)
			r.sendOnce.Do(func() {
				if r.state.durationMode() {
					r.state.addSender(upstreamSender{
						conn:     request.GetConnection(),
						clientID: r.clientID,
						deviceID: r.deviceID,
					})
					return
				}
				r.sendUpstream(request.GetConnection())
			})
			return
		}
		r.state.counts.bindRejected.Add(1)
		r.state.counts.failures.Add(classifyAck("bind", ack))
		if !r.state.durationMode() {
			r.state.markUpstreamSkipped(r.state.config.MessagesPerClient)
		}
		return
	}

	r.state.markUpstreamAck(ack)
}

func (r *ackRouter) sendUpstream(conn ziface.IConnection) {
	for i := 0; i < r.state.config.MessagesPerClient; i++ {
		messageID := fmt.Sprintf("load-upstream-%s-%d-%d", r.deviceID, i, time.Now().UnixNano())
		r.state.rememberSent(messageID)
		if err := sendPacket(conn, packetInput{
			msgID:     uint32(r.state.config.UpstreamMsgID),
			clientID:  r.clientID,
			deviceID:  r.deviceID,
			token:     r.state.config.Token,
			messageID: messageID,
			body:      r.state.body,
		}); err != nil {
			r.state.forgetSent(messageID)
			r.state.counts.sendErrors.Add(1)
			r.state.markUpstreamRejected("upstream_send_error")
		}
	}
}

type packetInput struct {
	msgID     uint32
	clientID  string
	deviceID  string
	token     string
	messageID string
	body      []byte
}

func sendPacket(conn ziface.IConnection, input packetInput) error {
	packet := protocol.NewPacket(input.msgID, input.body)
	packet.ClientID = input.clientID
	packet.DeviceID = input.deviceID
	packet.Token = input.token
	packet.MessageID = input.messageID
	packet.TraceID = input.messageID
	packet.Timestamp = time.Now().UnixMilli()
	packet.Flags = protocol.FlagAckRequired

	data, err := protocol.Encode(packet)
	if err != nil {
		return err
	}

	return conn.SendMsg(packet.MsgID, data)
}

func stopClients(clients []ziface.IClient) {
	for _, client := range clients {
		if client == nil {
			continue
		}
		client.Stop()
	}
}

func runDownlink(ctx context.Context, cfg config, counts *counters) error {
	if cfg.DurationMode() {
		return runDownlinkContinuous(ctx, cfg, counts)
	}

	total := cfg.Clients * cfg.MessagesPerClient
	if total == 0 {
		return nil
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := cfg.HTTPConcurrency
	if workers > total {
		workers = total
	}
	client := &http.Client{Timeout: cfg.Timeout}
	body := payload(cfg.BodySize)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				if err := ctx.Err(); err != nil {
					counts.sendErrors.Add(1)
					counts.failures.Add(reasonFromError(err))
					continue
				}
				result, err := postDownlink(ctx, client, cfg, job, body)
				counts.downlinkLatency.Record(result.latency)
				if err != nil {
					counts.sendErrors.Add(1)
					counts.failures.Add(result.code)
					continue
				}
				if result.status >= http.StatusOK && result.status < http.StatusMultipleChoices {
					counts.downlinkSuccess.Add(1)
				} else {
					counts.downlinkRejected.Add(1)
					counts.failures.Add(result.code)
				}
			}
		}()
	}

	for i := 0; i < total; i++ {
		select {
		case <-ctx.Done():
			counts.failures.Add(reasonFromError(ctx.Err()))
			close(jobs)
			wg.Wait()
			return ctx.Err()
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()
	return ctx.Err()
}

func runDownlinkContinuous(ctx context.Context, cfg config, counts *counters) error {
	jobs := make(chan int, cfg.HTTPConcurrency*2)
	var wg sync.WaitGroup
	client := &http.Client{Timeout: cfg.Timeout}
	body := payload(cfg.BodySize)

	for i := 0; i < cfg.HTTPConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				if err := ctx.Err(); err != nil {
					counts.sendErrors.Add(1)
					counts.failures.Add(reasonFromError(err))
					continue
				}
				result, err := postDownlink(ctx, client, cfg, job, body)
				counts.downlinkLatency.Record(result.latency)
				if err != nil {
					counts.sendErrors.Add(1)
					counts.failures.Add(result.code)
					continue
				}
				if result.status >= http.StatusOK && result.status < http.StatusMultipleChoices {
					counts.downlinkSuccess.Add(1)
				} else {
					counts.downlinkRejected.Add(1)
					counts.failures.Add(result.code)
				}
			}
		}()
	}

	ticker := time.NewTicker(rateInterval(cfg.Rate))
	defer ticker.Stop()
	durationTimer := time.NewTimer(cfg.RunDuration)
	defer durationTimer.Stop()

	job := 0
	for {
		select {
		case <-ctx.Done():
			counts.failures.Add(reasonFromError(ctx.Err()))
			close(jobs)
			wg.Wait()
			return ctx.Err()
		case <-durationTimer.C:
			close(jobs)
			wg.Wait()
			return nil
		case <-ticker.C:
			select {
			case <-ctx.Done():
				counts.failures.Add(reasonFromError(ctx.Err()))
				close(jobs)
				wg.Wait()
				return ctx.Err()
			case jobs <- job:
				job++
			}
		}
	}
}

func rateInterval(rate int) time.Duration {
	if rate <= 0 {
		return time.Second
	}

	interval := time.Second / time.Duration(rate)
	if interval <= 0 {
		return time.Nanosecond
	}

	return interval
}

type downlinkResult struct {
	status  int
	code    string
	latency time.Duration
}

func postDownlink(ctx context.Context, client *http.Client, cfg config, index int, body []byte) (downlinkResult, error) {
	startedAt := time.Now()
	clientIndex := index % cfg.Clients
	messageID := fmt.Sprintf("load-downlink-%d-%d", index, time.Now().UnixNano())
	reqBody, err := sonic.Marshal(downlink.PushRequest{
		ClientID:    fmt.Sprintf("%s-%d", cfg.ClientPrefix, clientIndex),
		DeviceID:    fmt.Sprintf("%s-%d", cfg.DevicePrefix, clientIndex),
		MsgID:       uint32(cfg.DownlinkMsgID),
		MessageID:   messageID,
		TraceID:     messageID,
		AckRequired: true,
		Body:        body,
	})
	if err != nil {
		return downlinkResult{code: "marshal_error", latency: time.Since(startedAt)}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.InternalURL, "/")+"/internal/push", bytes.NewReader(reqBody))
	if err != nil {
		return downlinkResult{code: "request_error", latency: time.Since(startedAt)}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.InternalToken != "" {
		req.Header.Set(downlink.InternalTokenHeader, cfg.InternalToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return downlinkResult{code: reasonFromError(err), latency: time.Since(startedAt)}, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return downlinkResult{
		status:  resp.StatusCode,
		code:    codeFromHTTPResponse(resp.StatusCode, respBody),
		latency: time.Since(startedAt),
	}, nil
}

func payload(size int) []byte {
	if size <= 0 {
		return nil
	}

	return bytes.Repeat([]byte("x"), size)
}

func printSummary(cfg config, counts *counters, duration time.Duration) {
	totalUpstream := counts.upstreamAccepted.Load() + counts.upstreamRejected.Load()
	totalDownlink := counts.downlinkSuccess.Load() + counts.downlinkRejected.Load()
	total := totalUpstream + totalDownlink
	qps := 0.0
	if duration > 0 {
		qps = float64(total) / duration.Seconds()
	}

	fmt.Printf("mode=%s clients=%d messages_per_client=%d duration=%s", cfg.Mode, cfg.Clients, cfg.MessagesPerClient, duration.Truncate(time.Millisecond))
	if cfg.DurationMode() {
		fmt.Printf(" target_duration=%s rate=%d", cfg.RunDuration, cfg.Rate)
	}
	fmt.Printf(" qps=%.2f\n", qps)
	fmt.Printf("bind accepted=%d rejected=%d\n", counts.bindAccepted.Load(), counts.bindRejected.Load())
	fmt.Printf("upstream accepted=%d rejected=%d\n", counts.upstreamAccepted.Load(), counts.upstreamRejected.Load())
	fmt.Printf("downlink success=%d rejected=%d\n", counts.downlinkSuccess.Load(), counts.downlinkRejected.Load())
	fmt.Printf("errors send=%d decode=%d\n", counts.sendErrors.Load(), counts.decodeErrors.Load())
	printLatencySummary("bind_ack", counts.bindLatency.Summary())
	printLatencySummary("upstream_ack", counts.upstreamLatency.Summary())
	printLatencySummary("downlink_http", counts.downlinkLatency.Summary())
	printFailureSummary(counts.failures.Snapshot())
}

func classifyAck(prefix string, ack protocol.Ack) string {
	code := string(ack.Code)
	if code == "" {
		code = "unknown"
	}

	reason := strings.ToLower(ack.Reason)
	if ack.Code == protocol.AckRejected && strings.Contains(reason, "overload") {
		code = "overloaded"
	}

	return prefix + "_" + code
}

func reasonFromError(err error) string {
	if err == nil {
		return "unknown"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}

	return "request_error"
}

type latencyRecorder struct {
	mu     sync.Mutex
	values []time.Duration
}

func (r *latencyRecorder) Record(value time.Duration) {
	if value < 0 {
		return
	}

	r.mu.Lock()
	r.values = append(r.values, value)
	r.mu.Unlock()
}

func (r *latencyRecorder) Summary() latencySummary {
	r.mu.Lock()
	values := append([]time.Duration(nil), r.values...)
	r.mu.Unlock()

	if len(values) == 0 {
		return latencySummary{}
	}
	sort.Slice(values, func(i, j int) bool {
		return values[i] < values[j]
	})

	var sum time.Duration
	for _, value := range values {
		sum += value
	}

	return latencySummary{
		Count: len(values),
		Min:   values[0],
		Avg:   sum / time.Duration(len(values)),
		P50:   percentile(values, 0.50),
		P95:   percentile(values, 0.95),
		P99:   percentile(values, 0.99),
		Max:   values[len(values)-1],
	}
}

type latencySummary struct {
	Count int
	Min   time.Duration
	Avg   time.Duration
	P50   time.Duration
	P95   time.Duration
	P99   time.Duration
	Max   time.Duration
}

func percentile(values []time.Duration, p float64) time.Duration {
	if len(values) == 0 {
		return 0
	}

	index := int(math.Ceil(p*float64(len(values)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}

	return values[index]
}

func printLatencySummary(name string, summary latencySummary) {
	if summary.Count == 0 {
		return
	}

	fmt.Printf(
		"latency %s count=%d min=%s avg=%s p50=%s p95=%s p99=%s max=%s\n",
		name,
		summary.Count,
		formatLatency(summary.Min),
		formatLatency(summary.Avg),
		formatLatency(summary.P50),
		formatLatency(summary.P95),
		formatLatency(summary.P99),
		formatLatency(summary.Max),
	)
}

func formatLatency(value time.Duration) string {
	switch {
	case value >= time.Second:
		return value.Truncate(time.Millisecond).String()
	case value >= time.Millisecond:
		return value.Truncate(time.Microsecond).String()
	default:
		return value.String()
	}
}

type reasonCounts struct {
	mu     sync.Mutex
	counts map[string]int64
}

func (r *reasonCounts) Add(reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "unknown"
	}

	r.mu.Lock()
	if r.counts == nil {
		r.counts = make(map[string]int64)
	}
	r.counts[reason]++
	r.mu.Unlock()
}

func (r *reasonCounts) Snapshot() []reasonCount {
	r.mu.Lock()
	items := make([]reasonCount, 0, len(r.counts))
	for reason, count := range r.counts {
		items = append(items, reasonCount{Reason: reason, Count: count})
	}
	r.mu.Unlock()

	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].Reason < items[j].Reason
		}
		return items[i].Count > items[j].Count
	})

	return items
}

type reasonCount struct {
	Reason string
	Count  int64
}

func printFailureSummary(items []reasonCount) {
	if len(items) == 0 {
		return
	}

	fmt.Println("failure reasons:")
	for _, item := range items {
		fmt.Printf("  %s=%d\n", item.Reason, item.Count)
	}
}

func codeFromHTTPResponse(status int, body []byte) string {
	var resp struct {
		Code string `json:"code"`
	}
	if len(body) > 0 {
		_ = sonic.Unmarshal(body, &resp)
	}
	if resp.Code != "" {
		return resp.Code
	}
	if status > 0 {
		return fmt.Sprintf("status_%d", status)
	}

	return "unknown"
}

type nopZinxLogger struct{}

func (nopZinxLogger) InfoF(string, ...any) {}

func (nopZinxLogger) ErrorF(string, ...any) {}

func (nopZinxLogger) DebugF(string, ...any) {}

func (nopZinxLogger) InfoFX(context.Context, string, ...any) {}

func (nopZinxLogger) ErrorFX(context.Context, string, ...any) {}

func (nopZinxLogger) DebugFX(context.Context, string, ...any) {}
