package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"go.uber.org/zap"
)

func main() {
	switch {
	case len(os.Args) > 1 && os.Args[1] == "push":
		os.Exit(runPush(os.Args[2:]))
	case len(os.Args) > 1 && os.Args[1] == "serve":
		os.Exit(runServe(os.Args[2:]))
	default:
		os.Exit(runServe(os.Args[1:]))
	}
}

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:18081", "development backend listen address")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: devbackend [serve] [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	logger, err := zap.NewDevelopment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "create logger: %v\n", err)
		return 1
	}
	defer func() {
		_ = logger.Sync()
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/gateway/upstream", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()

		body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		logger.Info(
			"received upstream packet",
			zap.String("trace_id", r.Header.Get("X-ZCourier-Trace-ID")),
			zap.String("body", string(body)),
		)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"code":"ok"}`))
	})

	server := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("starting development backend", zap.String("addr", *addr))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("development backend stopped unexpectedly", zap.Error(err))
		}
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	sig := <-signals
	fmt.Printf("exit signal: %s\n", sig)
	_ = server.Close()
	return 0
}

type pushConfig struct {
	InternalURL   string
	InternalToken string
	ClientID      string
	DeviceID      string
	MsgID         uint
	MessageID     string
	TraceID       string
	AckRequired   bool
	Body          string
	BodyFile      string
	Timeout       time.Duration
}

func runPush(args []string) int {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	var config pushConfig
	fs.StringVar(&config.InternalURL, "internal-url", "http://127.0.0.1:18082", "gateway internal HTTP base URL")
	fs.StringVar(&config.InternalToken, "internal-token", "dev-internal-token", "gateway internal HTTP token")
	fs.StringVar(&config.ClientID, "client-id", "dev-client", "target client id")
	fs.StringVar(&config.DeviceID, "device-id", "device-1", "target device id")
	fs.UintVar(&config.MsgID, "msg-id", 2001, "downlink message id")
	fs.StringVar(&config.MessageID, "message-id", "", "downlink message idempotency key; generated when empty")
	fs.StringVar(&config.TraceID, "trace-id", "", "trace id; defaults to message-id")
	fs.BoolVar(&config.AckRequired, "ack-required", true, "whether the client should ACK the downlink packet")
	fs.StringVar(&config.Body, "body", "devbackend-push", "downlink body text")
	fs.StringVar(&config.BodyFile, "body-file", "", "read downlink body bytes from file instead of -body")
	fs.DurationVar(&config.Timeout, "timeout", 10*time.Second, "push request timeout")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: devbackend push [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := push(config); err != nil {
		fmt.Fprintf(os.Stderr, "push failed: %v\n", err)
		return 1
	}
	return 0
}

func push(config pushConfig) error {
	if config.InternalURL == "" {
		return fmt.Errorf("internal-url is required")
	}
	if config.ClientID == "" {
		return fmt.Errorf("client-id is required")
	}
	if config.DeviceID == "" {
		return fmt.Errorf("device-id is required")
	}
	if config.MsgID == 0 {
		return fmt.Errorf("msg-id is required")
	}

	body, err := pushBody(config)
	if err != nil {
		return err
	}

	messageID := config.MessageID
	if messageID == "" {
		messageID = fmt.Sprintf("devbackend-push-%d", time.Now().UnixNano())
	}
	traceID := config.TraceID
	if traceID == "" {
		traceID = messageID
	}

	reqBody, err := sonic.Marshal(downlink.PushRequest{
		ClientID:    config.ClientID,
		DeviceID:    config.DeviceID,
		MsgID:       uint32(config.MsgID),
		MessageID:   messageID,
		TraceID:     traceID,
		AckRequired: config.AckRequired,
		Body:        body,
	})
	if err != nil {
		return fmt.Errorf("marshal push request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), config.Timeout)
	defer cancel()

	url := strings.TrimRight(config.InternalURL, "/") + "/internal/push"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if config.InternalToken != "" {
		req.Header.Set(downlink.InternalTokenHeader, config.InternalToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("post %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	fmt.Printf("status=%d\n", resp.StatusCode)
	fmt.Printf("message_id=%s\n", messageID)
	fmt.Printf("trace_id=%s\n", traceID)
	if len(respBody) > 0 {
		fmt.Printf("response=%s\n", string(respBody))
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("gateway returned status %d", resp.StatusCode)
	}
	return nil
}

func pushBody(config pushConfig) ([]byte, error) {
	if config.BodyFile == "" {
		return []byte(config.Body), nil
	}

	body, err := os.ReadFile(config.BodyFile)
	if err != nil {
		return nil, fmt.Errorf("read body file: %w", err)
	}
	return body, nil
}
