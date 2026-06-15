package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
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
}

func main() {
	cfg := parseFlags()
	if !cfg.EnableZinxLog {
		zlog.SetLogger(nopZinxLogger{})
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
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
	fs.DurationVar(&cfg.Timeout, "timeout", 30*time.Second, "overall load test timeout")
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

	return cfg
}

func runUpstream(ctx context.Context, cfg config, counts *counters) error {
	expected := int64(cfg.Clients * cfg.MessagesPerClient)
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
			if err := sendPacket(conn, packetInput{
				msgID:     protocol.MsgIDBind,
				clientID:  clientID,
				deviceID:  deviceID,
				token:     cfg.Token,
				messageID: bindMessageID,
				body:      []byte("load-bind"),
			}); err != nil {
				counts.sendErrors.Add(1)
				counts.bindRejected.Add(1)
				state.markUpstreamSkipped(cfg.MessagesPerClient)
				conn.Stop()
			}
		})
		client.Start()
		clients = append(clients, client)
	}
	defer stopClients(clients)

	select {
	case <-ctx.Done():
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
}

func (s *upstreamState) markUpstreamAck(accepted bool) {
	if accepted {
		s.counts.upstreamAccepted.Add(1)
	} else {
		s.counts.upstreamRejected.Add(1)
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
	total := s.counts.upstreamAccepted.Load() + s.counts.upstreamRejected.Load()
	if total >= s.expected {
		s.doneOnce.Do(func() { close(s.done) })
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
		return
	}

	var ack protocol.Ack
	if err := sonic.Unmarshal(packet.Body, &ack); err != nil {
		r.state.counts.decodeErrors.Add(1)
		return
	}

	accepted := ack.Code == protocol.AckAccepted
	if ack.MessageID == r.bindMessageID {
		if accepted {
			r.state.counts.bindAccepted.Add(1)
			r.sendOnce.Do(func() {
				r.sendUpstream(request.GetConnection())
			})
			return
		}
		r.state.counts.bindRejected.Add(1)
		r.state.markUpstreamSkipped(r.state.config.MessagesPerClient)
		return
	}

	r.state.markUpstreamAck(accepted)
}

func (r *ackRouter) sendUpstream(conn ziface.IConnection) {
	for i := 0; i < r.state.config.MessagesPerClient; i++ {
		messageID := fmt.Sprintf("load-upstream-%s-%d-%d", r.deviceID, i, time.Now().UnixNano())
		if err := sendPacket(conn, packetInput{
			msgID:     uint32(r.state.config.UpstreamMsgID),
			clientID:  r.clientID,
			deviceID:  r.deviceID,
			token:     r.state.config.Token,
			messageID: messageID,
			body:      r.state.body,
		}); err != nil {
			r.state.counts.sendErrors.Add(1)
			r.state.markUpstreamAck(false)
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
					continue
				}
				status, err := postDownlink(ctx, client, cfg, job, body)
				if err != nil {
					counts.sendErrors.Add(1)
					continue
				}
				if status >= http.StatusOK && status < http.StatusMultipleChoices {
					counts.downlinkSuccess.Add(1)
				} else {
					counts.downlinkRejected.Add(1)
				}
			}
		}()
	}

	for i := 0; i < total; i++ {
		select {
		case <-ctx.Done():
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

func postDownlink(ctx context.Context, client *http.Client, cfg config, index int, body []byte) (int, error) {
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
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.InternalURL, "/")+"/internal/push", bytes.NewReader(reqBody))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.InternalToken != "" {
		req.Header.Set(downlink.InternalTokenHeader, cfg.InternalToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
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

	fmt.Printf("mode=%s clients=%d messages_per_client=%d duration=%s qps=%.2f\n", cfg.Mode, cfg.Clients, cfg.MessagesPerClient, duration.Truncate(time.Millisecond), qps)
	fmt.Printf("bind accepted=%d rejected=%d\n", counts.bindAccepted.Load(), counts.bindRejected.Load())
	fmt.Printf("upstream accepted=%d rejected=%d\n", counts.upstreamAccepted.Load(), counts.upstreamRejected.Load())
	fmt.Printf("downlink success=%d rejected=%d\n", counts.downlinkSuccess.Load(), counts.downlinkRejected.Load())
	fmt.Printf("errors send=%d decode=%d\n", counts.sendErrors.Load(), counts.decodeErrors.Load())
}

type nopZinxLogger struct{}

func (nopZinxLogger) InfoF(string, ...any) {}

func (nopZinxLogger) ErrorF(string, ...any) {}

func (nopZinxLogger) DebugF(string, ...any) {}

func (nopZinxLogger) InfoFX(context.Context, string, ...any) {}

func (nopZinxLogger) ErrorFX(context.Context, string, ...any) {}

func (nopZinxLogger) DebugFX(context.Context, string, ...any) {}
