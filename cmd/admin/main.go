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
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	sdkbackend "github.com/qiuyier/Z-Courier/pkg/sdk/backend"
	"github.com/qiuyier/Z-Courier/pkg/sdk/signing"
)

const (
	authModeToken = "token"
	authModeHMAC  = "hmac"
)

var httpClient = http.DefaultClient

type commonConfig struct {
	InternalURL   string
	AuthMode      string
	InternalToken string
	HMACKeyID     string
	HMACSecret    string
	Timeout       time.Duration
}

type routeConfig struct {
	commonConfig
	ClientID string
	DeviceID string
}

type sessionsConfig struct {
	commonConfig
	ClientID string
	Limit    int
}

type messageConfig struct {
	commonConfig
	MessageID string
}

type messagesConfig struct {
	commonConfig
	Status string
	Limit  int
}

type requeueConfig struct {
	commonConfig
	MessageID string
	Confirm   bool
}

type discardConfig struct {
	commonConfig
	MessageID string
	Reason    string
	Confirm   bool
}

func main() {
	switch {
	case len(os.Args) > 1 && os.Args[1] == "overview":
		os.Exit(runOverview(os.Args[2:]))
	case len(os.Args) > 1 && os.Args[1] == "routes":
		os.Exit(runRoutes(os.Args[2:]))
	case len(os.Args) > 1 && os.Args[1] == "route":
		os.Exit(runRoute(os.Args[2:]))
	case len(os.Args) > 1 && os.Args[1] == "sessions":
		os.Exit(runSessions(os.Args[2:]))
	case len(os.Args) > 1 && os.Args[1] == "message":
		os.Exit(runMessage(os.Args[2:]))
	case len(os.Args) > 1 && os.Args[1] == "messages":
		os.Exit(runMessages(os.Args[2:]))
	case len(os.Args) > 1 && os.Args[1] == "requeue":
		os.Exit(runRequeue(os.Args[2:]))
	case len(os.Args) > 1 && os.Args[1] == "discard":
		os.Exit(runDiscard(os.Args[2:]))
	default:
		printUsage(os.Stderr)
		os.Exit(2)
	}
}

func printUsage(out io.Writer) {
	fmt.Fprintln(out, "Usage: admin <overview|routes|route|sessions|message|messages|requeue|discard> [flags]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  overview   Show gateway identity, readiness, cluster, sessions, and dependency summary")
	fmt.Fprintln(out, "  routes     Show enabled upstream route ranges and sanitized target metadata")
	fmt.Fprintln(out, "  route      Show where one client/device would be pushed")
	fmt.Fprintln(out, "  sessions   Show local sessions, optionally filtered by client_id")
	fmt.Fprintln(out, "  message    Show one stored downlink message by message_id")
	fmt.Fprintln(out, "  messages   List stored downlink messages by delivery status")
	fmt.Fprintln(out, "  requeue    Requeue one stored downlink message, requires -confirm")
	fmt.Fprintln(out, "  discard    Discard one stored downlink message, requires -reason and -confirm")
}

func runOverview(args []string) int {
	fs := flag.NewFlagSet("overview", flag.ExitOnError)
	config := defaultCommonConfig()
	addCommonFlags(fs, &config)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: admin overview [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := overview(config); err != nil {
		fmt.Fprintf(os.Stderr, "overview failed: %v\n", err)
		return 1
	}
	return 0
}

func runRoutes(args []string) int {
	fs := flag.NewFlagSet("routes", flag.ExitOnError)
	config := defaultCommonConfig()
	addCommonFlags(fs, &config)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: admin routes [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := routes(config); err != nil {
		fmt.Fprintf(os.Stderr, "routes failed: %v\n", err)
		return 1
	}
	return 0
}

func runRoute(args []string) int {
	fs := flag.NewFlagSet("route", flag.ExitOnError)
	config := routeConfig{commonConfig: defaultCommonConfig()}
	addCommonFlags(fs, &config.commonConfig)
	fs.StringVar(&config.ClientID, "client-id", "", "client id")
	fs.StringVar(&config.DeviceID, "device-id", "", "device id")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: admin route -client-id client -device-id device [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := route(config); err != nil {
		fmt.Fprintf(os.Stderr, "route failed: %v\n", err)
		return 1
	}
	return 0
}

func runSessions(args []string) int {
	fs := flag.NewFlagSet("sessions", flag.ExitOnError)
	config := sessionsConfig{commonConfig: defaultCommonConfig(), Limit: 100}
	addCommonFlags(fs, &config.commonConfig)
	fs.StringVar(&config.ClientID, "client-id", "", "optional client id filter")
	fs.IntVar(&config.Limit, "limit", 100, "maximum sessions to return")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: admin sessions [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := sessions(config); err != nil {
		fmt.Fprintf(os.Stderr, "sessions failed: %v\n", err)
		return 1
	}
	return 0
}

func runMessage(args []string) int {
	fs := flag.NewFlagSet("message", flag.ExitOnError)
	config := messageConfig{commonConfig: defaultCommonConfig()}
	addCommonFlags(fs, &config.commonConfig)
	fs.StringVar(&config.MessageID, "message-id", "", "downlink message id")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: admin message -message-id message-id [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := message(config); err != nil {
		fmt.Fprintf(os.Stderr, "message failed: %v\n", err)
		return 1
	}
	return 0
}

func runMessages(args []string) int {
	fs := flag.NewFlagSet("messages", flag.ExitOnError)
	config := messagesConfig{commonConfig: defaultCommonConfig(), Status: string(sdkbackend.MessageStatusFailed), Limit: 100}
	addCommonFlags(fs, &config.commonConfig)
	fs.StringVar(&config.Status, "status", config.Status, "message status: pending, sent, delivered, failed, or discarded")
	fs.IntVar(&config.Limit, "limit", 100, "maximum messages to return")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: admin messages [flags]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := messages(config); err != nil {
		fmt.Fprintf(os.Stderr, "messages failed: %v\n", err)
		return 1
	}
	return 0
}

func runRequeue(args []string) int {
	fs := flag.NewFlagSet("requeue", flag.ExitOnError)
	config := requeueConfig{commonConfig: defaultCommonConfig()}
	addCommonFlags(fs, &config.commonConfig)
	fs.StringVar(&config.MessageID, "message-id", "", "downlink message id")
	fs.BoolVar(&config.Confirm, "confirm", false, "confirm the requeue mutation")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: admin requeue -message-id message-id -confirm [flags]\n")
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
	config := discardConfig{commonConfig: defaultCommonConfig()}
	addCommonFlags(fs, &config.commonConfig)
	fs.StringVar(&config.MessageID, "message-id", "", "downlink message id")
	fs.StringVar(&config.Reason, "reason", "", "operator reason for discarding the message")
	fs.BoolVar(&config.Confirm, "confirm", false, "confirm the discard mutation")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: admin discard -message-id message-id -reason reason -confirm [flags]\n")
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

func defaultCommonConfig() commonConfig {
	keyID := strings.TrimSpace(os.Getenv("ZCOURIER_ADMIN_HMAC_KEY_ID"))
	secret := os.Getenv("ZCOURIER_ADMIN_HMAC_SECRET")
	authMode := os.Getenv("ZCOURIER_ADMIN_AUTH")
	if authMode == "" {
		authMode = authModeToken
		if keyID != "" || secret != "" {
			authMode = authModeHMAC
		}
	}

	token := os.Getenv("ZCOURIER_ADMIN_INTERNAL_TOKEN")
	if token == "" && authMode == authModeToken {
		token = "dev-internal-token"
	}

	internalURL := os.Getenv("ZCOURIER_ADMIN_INTERNAL_URL")
	if internalURL == "" {
		internalURL = "http://127.0.0.1:18082"
	}

	return commonConfig{
		InternalURL:   internalURL,
		AuthMode:      authMode,
		InternalToken: token,
		HMACKeyID:     keyID,
		HMACSecret:    secret,
		Timeout:       10 * time.Second,
	}
}

func addCommonFlags(fs *flag.FlagSet, config *commonConfig) {
	fs.StringVar(&config.InternalURL, "internal-url", config.InternalURL, "gateway internal HTTP base URL")
	fs.StringVar(&config.AuthMode, "auth", config.AuthMode, "internal auth mode: token or hmac")
	fs.StringVar(&config.InternalToken, "internal-token", config.InternalToken, "gateway internal HTTP token for token auth")
	fs.StringVar(&config.HMACKeyID, "hmac-key-id", config.HMACKeyID, "HMAC key id for hmac auth")
	fs.StringVar(&config.HMACSecret, "hmac-secret", config.HMACSecret, "HMAC secret for hmac auth")
	fs.DurationVar(&config.Timeout, "timeout", config.Timeout, "request timeout")
}

func overview(config commonConfig) error {
	return requestAndPrint(config, "/internal/admin/overview")
}

func routes(config commonConfig) error {
	return requestAndPrint(config, "/internal/admin/routes")
}

func route(config routeConfig) error {
	if strings.TrimSpace(config.ClientID) == "" {
		return fmt.Errorf("client-id is required")
	}
	if strings.TrimSpace(config.DeviceID) == "" {
		return fmt.Errorf("device-id is required")
	}

	query := url.Values{}
	query.Set("client_id", strings.TrimSpace(config.ClientID))
	query.Set("device_id", strings.TrimSpace(config.DeviceID))
	return requestAndPrint(config.commonConfig, "/internal/debug/route?"+query.Encode())
}

func sessions(config sessionsConfig) error {
	if config.Limit <= 0 {
		return fmt.Errorf("limit must be greater than 0")
	}

	query := url.Values{}
	query.Set("limit", strconv.Itoa(config.Limit))
	if strings.TrimSpace(config.ClientID) != "" {
		query.Set("client_id", strings.TrimSpace(config.ClientID))
	}
	return requestAndPrint(config.commonConfig, "/internal/debug/sessions?"+query.Encode())
}

func message(config messageConfig) error {
	messageID := strings.TrimSpace(config.MessageID)
	if messageID == "" {
		return fmt.Errorf("message-id is required")
	}

	query := url.Values{}
	query.Set("message_id", messageID)
	return requestAndPrint(config.commonConfig, "/internal/message/status?"+query.Encode())
}

func messages(config messagesConfig) error {
	if config.Limit <= 0 {
		return fmt.Errorf("limit must be greater than 0")
	}
	status := sdkbackend.MessageStatus(strings.TrimSpace(config.Status))
	if status != "" && !status.Valid() {
		return fmt.Errorf("unsupported message status %q", config.Status)
	}

	query := url.Values{}
	query.Set("limit", strconv.Itoa(config.Limit))
	if status != "" {
		query.Set("status", string(status))
	}
	return requestAndPrint(config.commonConfig, "/internal/messages?"+query.Encode())
}

func requeue(config requeueConfig) error {
	messageID := strings.TrimSpace(config.MessageID)
	if messageID == "" {
		return fmt.Errorf("message-id is required")
	}
	if !config.Confirm {
		return fmt.Errorf("refusing to requeue %q without -confirm; would POST /internal/message/requeue", messageID)
	}

	request := sdkbackend.MessageActionRequest{MessageID: messageID}
	return requestAndPrintJSON(config.commonConfig, http.MethodPost, "/internal/message/requeue", request)
}

func discard(config discardConfig) error {
	messageID := strings.TrimSpace(config.MessageID)
	if messageID == "" {
		return fmt.Errorf("message-id is required")
	}
	reason := strings.TrimSpace(config.Reason)
	if reason == "" {
		return fmt.Errorf("reason is required")
	}
	if !config.Confirm {
		return fmt.Errorf("refusing to discard %q without -confirm; would POST /internal/message/discard", messageID)
	}

	request := sdkbackend.MessageActionRequest{MessageID: messageID, Reason: reason}
	return requestAndPrintJSON(config.commonConfig, http.MethodPost, "/internal/message/discard", request)
}

func requestAndPrint(config commonConfig, path string) error {
	return requestAndPrintJSON(config, http.MethodGet, path, nil)
}

func requestAndPrintJSON(config commonConfig, method, path string, body any) error {
	statusCode, responseBody, err := requestJSON(config, method, path, body)
	if err != nil {
		return err
	}

	fmt.Printf("status=%d\n", statusCode)
	if len(responseBody) > 0 {
		fmt.Printf("response=%s\n", prettyJSON(responseBody))
	}

	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("gateway returned status %d", statusCode)
	}
	return nil
}

func requestJSON(config commonConfig, method, path string, body any) (int, []byte, error) {
	if err := validateCommonConfig(config); err != nil {
		return 0, nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), config.Timeout)
	defer cancel()

	var requestBody []byte
	if body != nil {
		var err error
		requestBody, err = sonic.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("marshal request body: %w", err)
		}
	}

	requestURL := strings.TrimRight(config.InternalURL, "/") + path
	var reader io.Reader
	if requestBody != nil {
		reader = bytes.NewReader(requestBody)
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	switch strings.ToLower(strings.TrimSpace(config.AuthMode)) {
	case authModeToken:
		if config.InternalToken != "" {
			req.Header.Set(sdkbackend.InternalTokenHeader, config.InternalToken)
		}
	case authModeHMAC:
		signer, err := signing.NewSigner(signing.SignerConfig{
			KeyID:  strings.TrimSpace(config.HMACKeyID),
			Secret: []byte(config.HMACSecret),
		})
		if err != nil {
			return 0, nil, fmt.Errorf("create HMAC signer: %w", err)
		}
		if err := signer.Sign(req, requestBody); err != nil {
			return 0, nil, fmt.Errorf("sign request: %w", err)
		}
	default:
		return 0, nil, fmt.Errorf("unsupported auth mode %q", config.AuthMode)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%s %s: %w", method, requestURL, err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("read response: %w", err)
	}
	return resp.StatusCode, responseBody, nil
}

func validateCommonConfig(config commonConfig) error {
	if strings.TrimSpace(config.InternalURL) == "" {
		return fmt.Errorf("internal-url is required")
	}
	if config.Timeout <= 0 {
		return fmt.Errorf("timeout must be greater than 0")
	}
	switch strings.ToLower(strings.TrimSpace(config.AuthMode)) {
	case authModeToken:
		return nil
	case authModeHMAC:
		if strings.TrimSpace(config.HMACKeyID) == "" {
			return fmt.Errorf("hmac-key-id is required in hmac auth mode")
		}
		if config.HMACSecret == "" {
			return fmt.Errorf("hmac-secret is required in hmac auth mode")
		}
		return nil
	default:
		return fmt.Errorf("unsupported auth mode %q", config.AuthMode)
	}
}

func prettyJSON(body []byte) string {
	var value any
	if err := sonic.Unmarshal(body, &value); err != nil {
		return string(body)
	}
	pretty, err := sonic.MarshalIndent(value, "", "  ")
	if err != nil {
		return string(body)
	}
	return string(bytes.TrimSpace(pretty))
}
