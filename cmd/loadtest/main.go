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
	"path/filepath"
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
	ReportPath        string
	MinQPS            float64
	MaxP95MS          float64
	MaxP99MS          float64
	MaxErrorRate      float64
	MaxErrorRateSet   bool
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

	summary := buildSummary(cfg, counts, time.Since(startedAt), err)
	printSummary(summary)
	if reportErr := writeReport(cfg.ReportPath, summary); reportErr != nil {
		fmt.Fprintf(os.Stderr, "write report failed: %v\n", reportErr)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "loadtest failed: %v\n", err)
		os.Exit(1)
	}
	if summary.hasFailedChecks() {
		fmt.Fprintln(os.Stderr, "loadtest checks failed")
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
	fs.StringVar(&cfg.ReportPath, "report", "", "write JSON load test report to this path")
	fs.Float64Var(&cfg.MinQPS, "min-qps", 0, "fail if achieved QPS is below this value")
	fs.Float64Var(&cfg.MaxP95MS, "max-p95-ms", 0, "fail if primary latency p95 exceeds this value in milliseconds")
	fs.Float64Var(&cfg.MaxP99MS, "max-p99-ms", 0, "fail if primary latency p99 exceeds this value in milliseconds")
	fs.Float64Var(&cfg.MaxErrorRate, "max-error-rate", 0, "fail if overall error rate exceeds this fraction, e.g. 0.01 for 1%; disabled unless set")
	fs.BoolVar(&cfg.EnableZinxLog, "zinx-log", false, "enable Zinx internal logs")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: loadtest [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	fs.Visit(func(parsedFlag *flag.Flag) {
		if parsedFlag.Name == "max-error-rate" {
			cfg.MaxErrorRateSet = true
		}
	})

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
	if cfg.MinQPS < 0 {
		fmt.Fprintln(os.Stderr, "-min-qps must be greater than or equal to 0")
		os.Exit(2)
	}
	if cfg.MaxP95MS < 0 {
		fmt.Fprintln(os.Stderr, "-max-p95-ms must be greater than or equal to 0")
		os.Exit(2)
	}
	if cfg.MaxP99MS < 0 {
		fmt.Fprintln(os.Stderr, "-max-p99-ms must be greater than or equal to 0")
		os.Exit(2)
	}
	if cfg.MaxErrorRateSet && (cfg.MaxErrorRate < 0 || cfg.MaxErrorRate > 1) {
		fmt.Fprintln(os.Stderr, "-max-error-rate must be between 0 and 1")
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
	} else if expected == 0 {
		return nil
	}
	done := make(chan struct{})
	var doneOnce sync.Once
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

type summary struct {
	Mode              string                    `json:"mode"`
	Clients           int                       `json:"clients"`
	MessagesPerClient int                       `json:"messages_per_client"`
	HTTPConcurrency   int                       `json:"http_concurrency,omitempty"`
	Duration          string                    `json:"duration"`
	DurationMillis    int64                     `json:"duration_ms"`
	TargetDuration    string                    `json:"target_duration,omitempty"`
	Rate              int                       `json:"rate,omitempty"`
	QPS               float64                   `json:"qps"`
	ErrorRate         float64                   `json:"error_rate"`
	Counts            summaryCounts             `json:"counts"`
	Latency           map[string]latencySummary `json:"latency,omitempty"`
	Failures          map[string]int64          `json:"failures,omitempty"`
	Checks            []thresholdCheck          `json:"checks,omitempty"`
	Passed            bool                      `json:"passed"`
	Error             string                    `json:"error,omitempty"`
}

type summaryCounts struct {
	BindAccepted     int64 `json:"bind_accepted"`
	BindRejected     int64 `json:"bind_rejected"`
	UpstreamAccepted int64 `json:"upstream_accepted"`
	UpstreamRejected int64 `json:"upstream_rejected"`
	DownlinkSuccess  int64 `json:"downlink_success"`
	DownlinkRejected int64 `json:"downlink_rejected"`
	SendErrors       int64 `json:"send_errors"`
	DecodeErrors     int64 `json:"decode_errors"`
}

type thresholdCheck struct {
	Name     string  `json:"name"`
	Passed   bool    `json:"passed"`
	Actual   float64 `json:"actual"`
	Expected float64 `json:"expected"`
	Operator string  `json:"operator"`
	Reason   string  `json:"reason,omitempty"`
}

func buildSummary(cfg config, counts *counters, duration time.Duration, runErr error) summary {
	totalUpstream := counts.upstreamAccepted.Load() + counts.upstreamRejected.Load()
	totalDownlink := counts.downlinkSuccess.Load() + counts.downlinkRejected.Load()
	total := totalUpstream + totalDownlink
	qps := 0.0
	if duration > 0 {
		qps = float64(total) / duration.Seconds()
	}

	out := summary{
		Mode:              cfg.Mode,
		Clients:           cfg.Clients,
		MessagesPerClient: cfg.MessagesPerClient,
		HTTPConcurrency:   cfg.HTTPConcurrency,
		Duration:          duration.Truncate(time.Millisecond).String(),
		DurationMillis:    duration.Milliseconds(),
		QPS:               qps,
		Counts: summaryCounts{
			BindAccepted:     counts.bindAccepted.Load(),
			BindRejected:     counts.bindRejected.Load(),
			UpstreamAccepted: counts.upstreamAccepted.Load(),
			UpstreamRejected: counts.upstreamRejected.Load(),
			DownlinkSuccess:  counts.downlinkSuccess.Load(),
			DownlinkRejected: counts.downlinkRejected.Load(),
			SendErrors:       counts.sendErrors.Load(),
			DecodeErrors:     counts.decodeErrors.Load(),
		},
		Latency:  latencySummaries(counts),
		Failures: failureMap(counts.failures.Snapshot()),
	}
	if cfg.DurationMode() {
		out.TargetDuration = cfg.RunDuration.String()
		out.Rate = cfg.Rate
	}
	if runErr != nil {
		out.Error = runErr.Error()
	}
	out.ErrorRate = summaryErrorRate(out)
	out.Checks = evaluateChecks(cfg, out)
	out.Passed = runErr == nil && !hasFailedChecks(out.Checks)

	return out
}

func summaryErrorRate(summary summary) float64 {
	total := summary.Counts.BindAccepted +
		summary.Counts.BindRejected +
		summary.Counts.UpstreamAccepted +
		summary.Counts.UpstreamRejected +
		summary.Counts.DownlinkSuccess +
		summary.Counts.DownlinkRejected
	errors := summary.Counts.BindRejected +
		summary.Counts.UpstreamRejected +
		summary.Counts.DownlinkRejected +
		summary.Counts.SendErrors +
		summary.Counts.DecodeErrors
	if total == 0 {
		total = errors
	}
	if total == 0 {
		return 0
	}
	if errors > total {
		total = errors
	}

	return float64(errors) / float64(total)
}

func evaluateChecks(cfg config, summary summary) []thresholdCheck {
	var checks []thresholdCheck
	if cfg.MinQPS > 0 {
		checks = append(checks, thresholdCheck{
			Name:     "min_qps",
			Passed:   summary.QPS >= cfg.MinQPS,
			Actual:   summary.QPS,
			Expected: cfg.MinQPS,
			Operator: ">=",
		})
	}
	if cfg.MaxErrorRateSet {
		checks = append(checks, thresholdCheck{
			Name:     "max_error_rate",
			Passed:   summary.ErrorRate <= cfg.MaxErrorRate,
			Actual:   summary.ErrorRate,
			Expected: cfg.MaxErrorRate,
			Operator: "<=",
		})
	}
	if cfg.MaxP95MS > 0 {
		checks = append(checks, latencyThresholdCheck("max_p95_ms", summary, cfg.MaxP95MS, func(latency latencySummary) float64 {
			return latency.P95MS
		}))
	}
	if cfg.MaxP99MS > 0 {
		checks = append(checks, latencyThresholdCheck("max_p99_ms", summary, cfg.MaxP99MS, func(latency latencySummary) float64 {
			return latency.P99MS
		}))
	}

	return checks
}

func latencyThresholdCheck(name string, summary summary, expected float64, value func(latencySummary) float64) thresholdCheck {
	latencyName := primaryLatencyName(summary.Mode)
	latency, ok := summary.Latency[latencyName]
	if !ok || latency.Count == 0 {
		return thresholdCheck{
			Name:     name,
			Passed:   false,
			Expected: expected,
			Operator: "<=",
			Reason:   fmt.Sprintf("%s latency sample unavailable", latencyName),
		}
	}

	actual := value(latency)
	return thresholdCheck{
		Name:     name,
		Passed:   actual <= expected,
		Actual:   actual,
		Expected: expected,
		Operator: "<=",
	}
}

func primaryLatencyName(mode string) string {
	switch mode {
	case "downlink":
		return "downlink_http"
	default:
		return "upstream_ack"
	}
}

func (summary summary) hasFailedChecks() bool {
	return hasFailedChecks(summary.Checks)
}

func hasFailedChecks(checks []thresholdCheck) bool {
	for _, check := range checks {
		if !check.Passed {
			return true
		}
	}

	return false
}

func latencySummaries(counts *counters) map[string]latencySummary {
	items := map[string]latencySummary{
		"bind_ack":      counts.bindLatency.Summary(),
		"upstream_ack":  counts.upstreamLatency.Summary(),
		"downlink_http": counts.downlinkLatency.Summary(),
	}

	for name, item := range items {
		if item.Count == 0 {
			delete(items, name)
		}
	}
	if len(items) == 0 {
		return nil
	}

	return items
}

func failureMap(items []reasonCount) map[string]int64 {
	if len(items) == 0 {
		return nil
	}

	out := make(map[string]int64, len(items))
	for _, item := range items {
		out[item.Reason] = item.Count
	}
	return out
}

func printSummary(summary summary) {
	fmt.Printf("mode=%s clients=%d messages_per_client=%d duration=%s", summary.Mode, summary.Clients, summary.MessagesPerClient, summary.Duration)
	if summary.TargetDuration != "" {
		fmt.Printf(" target_duration=%s rate=%d", summary.TargetDuration, summary.Rate)
	}
	fmt.Printf(" qps=%.2f\n", summary.QPS)
	fmt.Printf("bind accepted=%d rejected=%d\n", summary.Counts.BindAccepted, summary.Counts.BindRejected)
	fmt.Printf("upstream accepted=%d rejected=%d\n", summary.Counts.UpstreamAccepted, summary.Counts.UpstreamRejected)
	fmt.Printf("downlink success=%d rejected=%d\n", summary.Counts.DownlinkSuccess, summary.Counts.DownlinkRejected)
	fmt.Printf("errors send=%d decode=%d\n", summary.Counts.SendErrors, summary.Counts.DecodeErrors)
	fmt.Printf("error_rate=%.4f passed=%t\n", summary.ErrorRate, summary.Passed)
	printLatencySummary("bind_ack", summary.Latency["bind_ack"])
	printLatencySummary("upstream_ack", summary.Latency["upstream_ack"])
	printLatencySummary("downlink_http", summary.Latency["downlink_http"])
	printFailureSummary(reasonCountsFromMap(summary.Failures))
	printCheckSummary(summary.Checks)
	if summary.Error != "" {
		fmt.Printf("error=%s\n", summary.Error)
	}
}

func printCheckSummary(checks []thresholdCheck) {
	if len(checks) == 0 {
		return
	}

	fmt.Println("checks:")
	for _, check := range checks {
		status := "PASS"
		if !check.Passed {
			status = "FAIL"
		}
		reason := ""
		if check.Reason != "" {
			reason = " reason=" + check.Reason
		}
		fmt.Printf("  %s %s actual=%.4f expected%s%.4f%s\n", status, check.Name, check.Actual, check.Operator, check.Expected, reason)
	}
}

func writeReport(path string, summary summary) error {
	if path == "" {
		return nil
	}

	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	data, err := sonic.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	return os.WriteFile(path, data, 0o644)
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
		Count:    len(values),
		Min:      formatLatency(values[0]),
		Avg:      formatLatency(sum / time.Duration(len(values))),
		P50:      formatLatency(percentile(values, 0.50)),
		P95:      formatLatency(percentile(values, 0.95)),
		P99:      formatLatency(percentile(values, 0.99)),
		Max:      formatLatency(values[len(values)-1]),
		MinMS:    durationMillis(values[0]),
		AvgMS:    durationMillis(sum / time.Duration(len(values))),
		P50MS:    durationMillis(percentile(values, 0.50)),
		P95MS:    durationMillis(percentile(values, 0.95)),
		P99MS:    durationMillis(percentile(values, 0.99)),
		MaxMS:    durationMillis(values[len(values)-1]),
		minValue: values[0],
		avgValue: sum / time.Duration(len(values)),
		p50Value: percentile(values, 0.50),
		p95Value: percentile(values, 0.95),
		p99Value: percentile(values, 0.99),
		maxValue: values[len(values)-1],
	}
}

type latencySummary struct {
	Count    int     `json:"count"`
	Min      string  `json:"min"`
	Avg      string  `json:"avg"`
	P50      string  `json:"p50"`
	P95      string  `json:"p95"`
	P99      string  `json:"p99"`
	Max      string  `json:"max"`
	MinMS    float64 `json:"min_ms"`
	AvgMS    float64 `json:"avg_ms"`
	P50MS    float64 `json:"p50_ms"`
	P95MS    float64 `json:"p95_ms"`
	P99MS    float64 `json:"p99_ms"`
	MaxMS    float64 `json:"max_ms"`
	minValue time.Duration
	avgValue time.Duration
	p50Value time.Duration
	p95Value time.Duration
	p99Value time.Duration
	maxValue time.Duration
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
		summary.Min,
		summary.Avg,
		summary.P50,
		summary.P95,
		summary.P99,
		summary.Max,
	)
}

func durationMillis(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
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

func reasonCountsFromMap(items map[string]int64) []reasonCount {
	if len(items) == 0 {
		return nil
	}

	out := make([]reasonCount, 0, len(items))
	for reason, count := range items {
		out = append(out, reasonCount{Reason: reason, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Reason < out[j].Reason
		}
		return out[i].Count > out[j].Count
	})

	return out
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
