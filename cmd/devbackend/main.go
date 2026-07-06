package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"go.uber.org/zap"
)

func main() {
	switch {
	case len(os.Args) > 1 && os.Args[1] == "batch":
		os.Exit(runBatch(os.Args[2:]))
	case len(os.Args) > 1 && os.Args[1] == "push":
		os.Exit(runPush(os.Args[2:]))
	case len(os.Args) > 1 && os.Args[1] == "status":
		os.Exit(runStatus(os.Args[2:]))
	case len(os.Args) > 1 && os.Args[1] == "list":
		os.Exit(runList(os.Args[2:]))
	case len(os.Args) > 1 && os.Args[1] == "requeue":
		os.Exit(runRequeue(os.Args[2:]))
	case len(os.Args) > 1 && os.Args[1] == "discard":
		os.Exit(runDiscard(os.Args[2:]))
	case len(os.Args) > 1 && os.Args[1] == "route":
		os.Exit(runRoute(os.Args[2:]))
	case len(os.Args) > 1 && os.Args[1] == "sessions":
		os.Exit(runSessions(os.Args[2:]))
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

type batchConfig struct {
	InternalURL   string
	InternalToken string
	Messages      messageFlags
	AckRequired   bool
	Timeout       time.Duration
}

type statusConfig struct {
	InternalURL   string
	InternalToken string
	MessageID     string
	Timeout       time.Duration
}

type listConfig struct {
	InternalURL   string
	InternalToken string
	Status        string
	Limit         int
	Cursor        string
	Timeout       time.Duration
}

type messageActionConfig struct {
	InternalURL   string
	InternalToken string
	MessageID     string
	Reason        string
	Timeout       time.Duration
}

type routeConfig struct {
	InternalURL   string
	InternalToken string
	ClientID      string
	DeviceID      string
	Timeout       time.Duration
}

type sessionsConfig struct {
	InternalURL   string
	InternalToken string
	ClientID      string
	Limit         int
	Timeout       time.Duration
}

type messageFlags []string

func (f *messageFlags) String() string {
	return strings.Join(*f, ";")
}

func (f *messageFlags) Set(value string) error {
	*f = append(*f, value)
	return nil
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

func runBatch(args []string) int {
	fs := flag.NewFlagSet("batch", flag.ExitOnError)
	var config batchConfig
	fs.StringVar(&config.InternalURL, "internal-url", "http://127.0.0.1:18082", "gateway internal HTTP base URL")
	fs.StringVar(&config.InternalToken, "internal-token", "dev-internal-token", "gateway internal HTTP token")
	fs.Var(&config.Messages, "message", "message in client_id,device_id,msg_id,body format; repeat for multiple messages")
	fs.BoolVar(&config.AckRequired, "ack-required", true, "whether clients should ACK the downlink packets")
	fs.DurationVar(&config.Timeout, "timeout", 10*time.Second, "batch push request timeout")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: devbackend batch -message client,device,msg_id,body [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := batch(config); err != nil {
		fmt.Fprintf(os.Stderr, "batch push failed: %v\n", err)
		return 1
	}
	return 0
}

func runStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	var config statusConfig
	fs.StringVar(&config.InternalURL, "internal-url", "http://127.0.0.1:18082", "gateway internal HTTP base URL")
	fs.StringVar(&config.InternalToken, "internal-token", "dev-internal-token", "gateway internal HTTP token")
	fs.StringVar(&config.MessageID, "message-id", "", "downlink message id")
	fs.DurationVar(&config.Timeout, "timeout", 10*time.Second, "status request timeout")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: devbackend status -message-id message-id [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := status(config); err != nil {
		fmt.Fprintf(os.Stderr, "status query failed: %v\n", err)
		return 1
	}
	return 0
}

func runList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	var config listConfig
	fs.StringVar(&config.InternalURL, "internal-url", "http://127.0.0.1:18082", "gateway internal HTTP base URL")
	fs.StringVar(&config.InternalToken, "internal-token", "dev-internal-token", "gateway internal HTTP token")
	fs.StringVar(&config.Status, "status", string(downlink.MessageStatusFailed), "downlink message status")
	fs.IntVar(&config.Limit, "limit", 100, "maximum messages to return")
	fs.StringVar(&config.Cursor, "cursor", "", "opaque cursor returned by a previous list response")
	fs.DurationVar(&config.Timeout, "timeout", 10*time.Second, "list request timeout")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: devbackend list [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := listMessages(config); err != nil {
		fmt.Fprintf(os.Stderr, "list messages failed: %v\n", err)
		return 1
	}
	return 0
}

func runRequeue(args []string) int {
	fs := flag.NewFlagSet("requeue", flag.ExitOnError)
	var config messageActionConfig
	fs.StringVar(&config.InternalURL, "internal-url", "http://127.0.0.1:18082", "gateway internal HTTP base URL")
	fs.StringVar(&config.InternalToken, "internal-token", "dev-internal-token", "gateway internal HTTP token")
	fs.StringVar(&config.MessageID, "message-id", "", "downlink message id")
	fs.DurationVar(&config.Timeout, "timeout", 10*time.Second, "requeue request timeout")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: devbackend requeue -message-id message-id [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := requeue(config); err != nil {
		fmt.Fprintf(os.Stderr, "requeue failed: %v\n", err)
		return 1
	}
	return 0
}

func runDiscard(args []string) int {
	fs := flag.NewFlagSet("discard", flag.ExitOnError)
	var config messageActionConfig
	fs.StringVar(&config.InternalURL, "internal-url", "http://127.0.0.1:18082", "gateway internal HTTP base URL")
	fs.StringVar(&config.InternalToken, "internal-token", "dev-internal-token", "gateway internal HTTP token")
	fs.StringVar(&config.MessageID, "message-id", "", "downlink message id")
	fs.StringVar(&config.Reason, "reason", "", "discard reason")
	fs.DurationVar(&config.Timeout, "timeout", 10*time.Second, "discard request timeout")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: devbackend discard -message-id message-id [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := discard(config); err != nil {
		fmt.Fprintf(os.Stderr, "discard failed: %v\n", err)
		return 1
	}
	return 0
}

func runRoute(args []string) int {
	fs := flag.NewFlagSet("route", flag.ExitOnError)
	var config routeConfig
	fs.StringVar(&config.InternalURL, "internal-url", "http://127.0.0.1:18082", "gateway internal HTTP base URL")
	fs.StringVar(&config.InternalToken, "internal-token", "dev-internal-token", "gateway internal HTTP token")
	fs.StringVar(&config.ClientID, "client-id", "", "client id")
	fs.StringVar(&config.DeviceID, "device-id", "", "device id")
	fs.DurationVar(&config.Timeout, "timeout", 10*time.Second, "route query timeout")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: devbackend route -client-id client -device-id device [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := route(config); err != nil {
		fmt.Fprintf(os.Stderr, "route query failed: %v\n", err)
		return 1
	}
	return 0
}

func runSessions(args []string) int {
	fs := flag.NewFlagSet("sessions", flag.ExitOnError)
	var config sessionsConfig
	fs.StringVar(&config.InternalURL, "internal-url", "http://127.0.0.1:18082", "gateway internal HTTP base URL")
	fs.StringVar(&config.InternalToken, "internal-token", "dev-internal-token", "gateway internal HTTP token")
	fs.StringVar(&config.ClientID, "client-id", "", "optional client id filter")
	fs.IntVar(&config.Limit, "limit", 100, "maximum sessions to return")
	fs.DurationVar(&config.Timeout, "timeout", 10*time.Second, "sessions query timeout")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: devbackend sessions [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := sessions(config); err != nil {
		fmt.Fprintf(os.Stderr, "sessions query failed: %v\n", err)
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

	status, respBody, err := postJSON(config.InternalURL, "/internal/push", config.InternalToken, reqBody, config.Timeout)
	if err != nil {
		return err
	}

	fmt.Printf("status=%d\n", status)
	fmt.Printf("message_id=%s\n", messageID)
	fmt.Printf("trace_id=%s\n", traceID)
	if len(respBody) > 0 {
		fmt.Printf("response=%s\n", string(respBody))
	}

	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return fmt.Errorf("gateway returned status %d", status)
	}
	return nil
}

func batch(config batchConfig) error {
	if config.InternalURL == "" {
		return fmt.Errorf("internal-url is required")
	}
	if len(config.Messages) == 0 {
		return fmt.Errorf("at least one -message is required")
	}

	messages := make([]downlink.PushRequest, 0, len(config.Messages))
	for i, raw := range config.Messages {
		message, err := parseBatchMessage(raw, config.AckRequired, i)
		if err != nil {
			return err
		}
		messages = append(messages, message)
	}

	reqBody, err := sonic.Marshal(downlink.BatchPushRequest{Messages: messages})
	if err != nil {
		return fmt.Errorf("marshal batch push request: %w", err)
	}

	status, respBody, err := postJSON(config.InternalURL, "/internal/push/batch", config.InternalToken, reqBody, config.Timeout)
	if err != nil {
		return err
	}

	fmt.Printf("status=%d\n", status)
	fmt.Printf("total=%d\n", len(messages))
	if len(respBody) > 0 {
		fmt.Printf("response=%s\n", string(respBody))
	}

	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return fmt.Errorf("gateway returned status %d", status)
	}
	return nil
}

func status(config statusConfig) error {
	if config.InternalURL == "" {
		return fmt.Errorf("internal-url is required")
	}
	if strings.TrimSpace(config.MessageID) == "" {
		return fmt.Errorf("message-id is required")
	}

	path := "/internal/message/status?message_id=" + url.QueryEscape(strings.TrimSpace(config.MessageID))
	statusCode, respBody, err := getJSON(config.InternalURL, path, config.InternalToken, config.Timeout)
	if err != nil {
		return err
	}

	fmt.Printf("status=%d\n", statusCode)
	if len(respBody) > 0 {
		fmt.Printf("response=%s\n", string(respBody))
	}

	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("gateway returned status %d", statusCode)
	}
	return nil
}

func listMessages(config listConfig) error {
	if config.InternalURL == "" {
		return fmt.Errorf("internal-url is required")
	}
	if strings.TrimSpace(config.Status) == "" {
		return fmt.Errorf("status is required")
	}
	if config.Limit <= 0 {
		return fmt.Errorf("limit must be greater than 0")
	}

	query := url.Values{}
	query.Set("status", strings.TrimSpace(config.Status))
	query.Set("limit", strconv.Itoa(config.Limit))
	if cursor := strings.TrimSpace(config.Cursor); cursor != "" {
		query.Set("cursor", cursor)
	}
	path := "/internal/messages?" + query.Encode()
	statusCode, respBody, err := getJSON(config.InternalURL, path, config.InternalToken, config.Timeout)
	if err != nil {
		return err
	}

	fmt.Printf("status=%d\n", statusCode)
	if len(respBody) > 0 {
		fmt.Printf("response=%s\n", string(respBody))
	}

	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("gateway returned status %d", statusCode)
	}
	return nil
}

func requeue(config messageActionConfig) error {
	return messageAction(config, "/internal/message/requeue")
}

func discard(config messageActionConfig) error {
	return messageAction(config, "/internal/message/discard")
}

func route(config routeConfig) error {
	if config.InternalURL == "" {
		return fmt.Errorf("internal-url is required")
	}
	if strings.TrimSpace(config.ClientID) == "" {
		return fmt.Errorf("client-id is required")
	}
	if strings.TrimSpace(config.DeviceID) == "" {
		return fmt.Errorf("device-id is required")
	}

	query := url.Values{}
	query.Set("client_id", strings.TrimSpace(config.ClientID))
	query.Set("device_id", strings.TrimSpace(config.DeviceID))
	path := "/internal/debug/route?" + query.Encode()
	statusCode, respBody, err := getJSON(config.InternalURL, path, config.InternalToken, config.Timeout)
	if err != nil {
		return err
	}

	fmt.Printf("status=%d\n", statusCode)
	if len(respBody) > 0 {
		fmt.Printf("response=%s\n", string(respBody))
	}

	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("gateway returned status %d", statusCode)
	}
	return nil
}

func sessions(config sessionsConfig) error {
	if config.InternalURL == "" {
		return fmt.Errorf("internal-url is required")
	}
	if config.Limit <= 0 {
		return fmt.Errorf("limit must be greater than 0")
	}

	query := url.Values{}
	query.Set("limit", strconv.Itoa(config.Limit))
	if strings.TrimSpace(config.ClientID) != "" {
		query.Set("client_id", strings.TrimSpace(config.ClientID))
	}
	path := "/internal/debug/sessions?" + query.Encode()
	statusCode, respBody, err := getJSON(config.InternalURL, path, config.InternalToken, config.Timeout)
	if err != nil {
		return err
	}

	fmt.Printf("status=%d\n", statusCode)
	if len(respBody) > 0 {
		fmt.Printf("response=%s\n", string(respBody))
	}

	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("gateway returned status %d", statusCode)
	}
	return nil
}

func messageAction(config messageActionConfig, path string) error {
	if config.InternalURL == "" {
		return fmt.Errorf("internal-url is required")
	}
	if strings.TrimSpace(config.MessageID) == "" {
		return fmt.Errorf("message-id is required")
	}

	reqBody, err := sonic.Marshal(downlink.MessageActionRequest{
		MessageID: strings.TrimSpace(config.MessageID),
		Reason:    config.Reason,
	})
	if err != nil {
		return fmt.Errorf("marshal message action request: %w", err)
	}

	statusCode, respBody, err := postJSON(config.InternalURL, path, config.InternalToken, reqBody, config.Timeout)
	if err != nil {
		return err
	}

	fmt.Printf("status=%d\n", statusCode)
	if len(respBody) > 0 {
		fmt.Printf("response=%s\n", string(respBody))
	}

	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("gateway returned status %d", statusCode)
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

func parseBatchMessage(raw string, ackRequired bool, index int) (downlink.PushRequest, error) {
	parts := strings.SplitN(raw, ",", 4)
	if len(parts) != 4 {
		return downlink.PushRequest{}, fmt.Errorf("message %d must use client_id,device_id,msg_id,body format", index)
	}

	clientID := strings.TrimSpace(parts[0])
	deviceID := strings.TrimSpace(parts[1])
	msgID, err := strconv.ParseUint(strings.TrimSpace(parts[2]), 10, 32)
	if err != nil {
		return downlink.PushRequest{}, fmt.Errorf("message %d invalid msg_id: %w", index, err)
	}
	if clientID == "" {
		return downlink.PushRequest{}, fmt.Errorf("message %d client_id is required", index)
	}
	if deviceID == "" {
		return downlink.PushRequest{}, fmt.Errorf("message %d device_id is required", index)
	}
	if msgID == 0 {
		return downlink.PushRequest{}, fmt.Errorf("message %d msg_id is required", index)
	}

	messageID := fmt.Sprintf("devbackend-batch-%d-%d", time.Now().UnixNano(), index)
	return downlink.PushRequest{
		ClientID:    clientID,
		DeviceID:    deviceID,
		MsgID:       uint32(msgID),
		MessageID:   messageID,
		TraceID:     messageID,
		AckRequired: ackRequired,
		Body:        []byte(parts[3]),
	}, nil
}

func postJSON(internalURL, path, token string, body []byte, timeout time.Duration) (int, []byte, error) {
	return requestJSON(http.MethodPost, internalURL, path, token, body, timeout)
}

func getJSON(internalURL, path, token string, timeout time.Duration) (int, []byte, error) {
	return requestJSON(http.MethodGet, internalURL, path, token, nil, timeout)
}

func requestJSON(method, internalURL, path, token string, body []byte, timeout time.Duration) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	url := strings.TrimRight(internalURL, "/") + path
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set(downlink.InternalTokenHeader, token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("read response: %w", err)
	}

	return resp.StatusCode, respBody, nil
}
