package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aceld/zinx/ziface"
	"github.com/aceld/zinx/znet"
	"github.com/bytedance/sonic"
	_ "github.com/jackc/pgx/v5/stdlib"
	nsq "github.com/nsqio/go-nsq"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/pkg/sdk/signing"
)

const (
	defaultGatewayHost  = "127.0.0.1"
	defaultGatewayPort  = 9899
	defaultInternalURL  = "http://127.0.0.1:18082"
	defaultPostgresDSN  = "postgres://zcourier:zcourier@127.0.0.1:15432/zcourier?sslmode=disable"
	downlinkPushTimeout = 10 * time.Second
	duplicateCheckDelay = 500 * time.Millisecond

	retryFairnessMsgID            = 2998
	retryFairnessHotMessages      = 8
	retryFairnessColdMessages     = 2
	defaultRetryFairnessScanLimit = 3

	internalAuthModeToken = "token"
	internalAuthModeHMAC  = "hmac"

	terminalPublisherNSQ                   = "nsq"
	terminalPublisherHTTP                  = "http"
	defaultTerminalWebhookAddress          = "127.0.0.1:18085"
	defaultTerminalWebhookPath             = "/v1/z-courier/terminal"
	defaultTerminalWebhookHMACKeyID        = "e2e-terminal-webhook"
	defaultTerminalWebhookHMACSecret       = "0123456789abcdef0123456789abcdef"
	maxTerminalWebhookBodySize       int64 = 64 << 10
)

type config struct {
	GatewayHost             string
	GatewayPort             int
	InternalURL             string
	MetricsURLs             []string
	InternalAuthMode        string
	InternalToken           string
	InternalHMACKeyID       string
	InternalHMACSecret      string
	PostgresDSN             string
	ClientID                string
	DeviceID                string
	Token                   string
	Timeout                 time.Duration
	OnlinePushDelay         time.Duration
	RequireClusterMetrics   bool
	ExpectRouteNode         string
	ExpectRouteInternalURL  string
	ExpectSessionURL        string
	ExpectSessionNode       string
	CheckReconnectRetry     bool
	CheckAdminStorage       bool
	CheckBulkRequeue        bool
	AdminSessionPeerURL     string
	ExpectPolicyName        string
	CheckQueueCapacity      bool
	ExpectPerDeviceLimit    int
	CheckRetryFairness      bool
	RetryFairnessScanLimit  int
	CheckTerminalEvent      bool
	TerminalPublisher       string
	TerminalNSQDAddress     string
	TerminalWebhookAddress  string
	TerminalWebhookPath     string
	TerminalWebhookKeyID    string
	TerminalWebhookSecret   string
	TerminalWebhookFailures int
	ExpectTerminalPolicy    string
}

func main() {
	cfg := parseFlags()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	if err := run(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "e2e failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("e2e passed")
}

func parseFlags() config {
	var cfg config
	var metricsURLRaw string
	flag.StringVar(&cfg.GatewayHost, "gateway-host", defaultGatewayHost, "gateway TCP host")
	flag.IntVar(&cfg.GatewayPort, "gateway-port", defaultGatewayPort, "gateway TCP port")
	flag.StringVar(&cfg.InternalURL, "internal-url", defaultInternalURL, "gateway internal HTTP base URL")
	flag.StringVar(&metricsURLRaw, "metrics-url", "", "comma-separated gateway metrics URLs; defaults to internal-url/metrics")
	flag.StringVar(&cfg.InternalAuthMode, "internal-auth-mode", internalAuthModeToken, "gateway internal HTTP auth mode: token or hmac")
	flag.StringVar(&cfg.InternalToken, "internal-token", "dev-internal-token", "gateway internal HTTP token")
	flag.StringVar(&cfg.InternalHMACKeyID, "internal-hmac-key-id", "", "HMAC key id for internal HTTP hmac auth")
	flag.StringVar(&cfg.InternalHMACSecret, "internal-hmac-secret", "", "HMAC secret for internal HTTP hmac auth")
	flag.StringVar(&cfg.PostgresDSN, "postgres-dsn", defaultPostgresDSN, "PostgreSQL DSN")
	flag.StringVar(&cfg.ClientID, "client-id", "e2e-client", "client id")
	flag.StringVar(&cfg.DeviceID, "device-id", "e2e-device", "device id")
	flag.StringVar(&cfg.Token, "token", "e2e-token", "client auth token")
	flag.DurationVar(&cfg.Timeout, "timeout", 30*time.Second, "overall timeout")
	flag.DurationVar(&cfg.OnlinePushDelay, "online-push-delay", 0, "delay before online downlink push after client bind")
	flag.BoolVar(&cfg.RequireClusterMetrics, "require-cluster-metrics", false, "require cluster and retry metrics to be exposed")
	flag.StringVar(&cfg.ExpectRouteNode, "expect-route-node", "", "expected cluster route gateway node via internal-url; empty disables debug route check")
	flag.StringVar(&cfg.ExpectRouteInternalURL, "expect-route-internal-url", "", "expected cluster route internal URL")
	flag.StringVar(&cfg.ExpectSessionURL, "expect-session-url", "", "gateway internal HTTP base URL expected to hold the local session; empty disables debug sessions check")
	flag.StringVar(&cfg.ExpectSessionNode, "expect-session-node", "", "expected gateway node from expect-session-url")
	flag.BoolVar(&cfg.CheckReconnectRetry, "check-reconnect-retry", false, "disconnect the client, queue a downlink, then verify reconnect flushes it")
	flag.BoolVar(&cfg.CheckAdminStorage, "check-admin-storage", false, "verify Redis-backed admin sessions and PostgreSQL admin audit storage")
	flag.BoolVar(&cfg.CheckBulkRequeue, "check-bulk-requeue", false, "verify cross-node guarded bulk requeue, shared capacity, and persistent audit state")
	flag.StringVar(&cfg.AdminSessionPeerURL, "admin-session-peer-url", "", "peer gateway internal HTTP base URL used for Redis-backed admin session lookup; defaults to expect-session-url or internal-url")
	flag.StringVar(&cfg.ExpectPolicyName, "expect-policy-name", "", "expected persisted delivery policy for MsgID 2001; empty disables the check")
	flag.BoolVar(&cfg.CheckQueueCapacity, "check-queue-capacity", false, "fill one offline device queue and verify explicit capacity rejection")
	flag.IntVar(&cfg.ExpectPerDeviceLimit, "expect-per-device-limit", 2, "expected pending-message capacity for the E2E device")
	flag.BoolVar(&cfg.CheckRetryFairness, "check-retry-fairness", false, "verify a bounded retry scan makes progress across hot and cold offline devices")
	flag.IntVar(&cfg.RetryFairnessScanLimit, "retry-fairness-scan-limit", defaultRetryFairnessScanLimit, "retry scan limit used by the fairness E2E check")
	flag.BoolVar(&cfg.CheckTerminalEvent, "check-terminal-event", false, "force a message to terminal failure and verify its published event")
	flag.StringVar(&cfg.TerminalPublisher, "terminal-publisher", terminalPublisherNSQ, "terminal event publisher under test: nsq or http")
	flag.StringVar(&cfg.TerminalNSQDAddress, "terminal-nsqd-address", "127.0.0.1:14150", "NSQ TCP address used to consume terminal events")
	flag.StringVar(&cfg.TerminalWebhookAddress, "terminal-webhook-address", defaultTerminalWebhookAddress, "HTTP address used by the terminal webhook test receiver")
	flag.StringVar(&cfg.TerminalWebhookPath, "terminal-webhook-path", defaultTerminalWebhookPath, "HTTP path used by the terminal webhook test receiver")
	flag.StringVar(&cfg.TerminalWebhookKeyID, "terminal-webhook-hmac-key-id", defaultTerminalWebhookHMACKeyID, "HMAC key id accepted by the terminal webhook test receiver")
	flag.StringVar(&cfg.TerminalWebhookSecret, "terminal-webhook-hmac-secret", defaultTerminalWebhookHMACSecret, "HMAC secret accepted by the terminal webhook test receiver")
	flag.IntVar(&cfg.TerminalWebhookFailures, "terminal-webhook-failures", 0, "number of verified HTTP failures injected before accepting each terminal event")
	flag.StringVar(&cfg.ExpectTerminalPolicy, "expect-terminal-policy", "integration-terminal", "expected policy for the terminal-event E2E message")
	flag.Parse()

	cfg.InternalURL = strings.TrimRight(cfg.InternalURL, "/")
	cfg.InternalAuthMode = strings.ToLower(strings.TrimSpace(cfg.InternalAuthMode))
	cfg.TerminalPublisher = strings.ToLower(strings.TrimSpace(cfg.TerminalPublisher))
	cfg.TerminalWebhookAddress = strings.TrimSpace(cfg.TerminalWebhookAddress)
	cfg.TerminalWebhookPath = strings.TrimSpace(cfg.TerminalWebhookPath)
	cfg.TerminalWebhookKeyID = strings.TrimSpace(cfg.TerminalWebhookKeyID)
	cfg.ExpectSessionURL = strings.TrimRight(cfg.ExpectSessionURL, "/")
	cfg.AdminSessionPeerURL = strings.TrimRight(cfg.AdminSessionPeerURL, "/")
	cfg.MetricsURLs = parseMetricsURLs(metricsURLRaw, cfg.InternalURL+"/metrics")
	return cfg
}

func parseMetricsURLs(raw, fallback string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{fallback}
	}

	parts := strings.Split(raw, ",")
	urls := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		urls = append(urls, part)
	}
	if len(urls) == 0 {
		return []string{fallback}
	}

	return urls
}

func validateInternalAuthConfig(cfg config) error {
	if cfg.CheckQueueCapacity && cfg.ExpectPerDeviceLimit <= 0 {
		return fmt.Errorf("expect-per-device-limit must be greater than 0 when check-queue-capacity is enabled")
	}
	if cfg.CheckRetryFairness {
		if cfg.RetryFairnessScanLimit != defaultRetryFairnessScanLimit {
			return fmt.Errorf("retry-fairness-scan-limit must be %d for the deterministic fairness fixture", defaultRetryFairnessScanLimit)
		}
		if cfg.ExpectPerDeviceLimit < retryFairnessHotMessages {
			return fmt.Errorf(
				"expect-per-device-limit must be at least %d when check-retry-fairness is enabled",
				retryFairnessHotMessages,
			)
		}
	}
	if cfg.CheckBulkRequeue {
		if !cfg.CheckAdminStorage {
			return fmt.Errorf("check-bulk-requeue requires check-admin-storage")
		}
		if !cfg.CheckQueueCapacity {
			return fmt.Errorf("check-bulk-requeue requires check-queue-capacity")
		}
		if cfg.ExpectPerDeviceLimit < 2 {
			return fmt.Errorf("expect-per-device-limit must be at least 2 when check-bulk-requeue is enabled")
		}
		if cfg.AdminSessionPeerURL == "" && cfg.ExpectSessionURL == "" {
			return fmt.Errorf("check-bulk-requeue requires admin-session-peer-url or expect-session-url")
		}
		if strings.TrimSpace(cfg.ExpectTerminalPolicy) == "" {
			return fmt.Errorf("expect-terminal-policy is required when check-bulk-requeue is enabled")
		}
	}
	if cfg.CheckTerminalEvent {
		if strings.TrimSpace(cfg.ExpectTerminalPolicy) == "" {
			return fmt.Errorf("expect-terminal-policy is required when check-terminal-event is enabled")
		}
		switch cfg.TerminalPublisher {
		case terminalPublisherNSQ:
			if strings.TrimSpace(cfg.TerminalNSQDAddress) == "" {
				return fmt.Errorf("terminal-nsqd-address is required for the NSQ terminal publisher")
			}
		case terminalPublisherHTTP:
			if cfg.TerminalWebhookAddress == "" {
				return fmt.Errorf("terminal-webhook-address is required for the HTTP terminal publisher")
			}
			if !strings.HasPrefix(cfg.TerminalWebhookPath, "/") {
				return fmt.Errorf("terminal-webhook-path must start with /")
			}
			if cfg.TerminalWebhookFailures < 0 {
				return fmt.Errorf("terminal-webhook-failures must be greater than or equal to 0")
			}
			if _, err := signing.NewVerifier(signing.VerifierConfig{
				Keys: map[string][]byte{cfg.TerminalWebhookKeyID: []byte(cfg.TerminalWebhookSecret)},
			}); err != nil {
				return fmt.Errorf("create terminal webhook HMAC verifier: %w", err)
			}
		default:
			return fmt.Errorf("unsupported terminal publisher %q", cfg.TerminalPublisher)
		}
	}
	switch cfg.InternalAuthMode {
	case internalAuthModeToken:
		return nil
	case internalAuthModeHMAC:
		if strings.TrimSpace(cfg.InternalHMACKeyID) == "" {
			return fmt.Errorf("internal-hmac-key-id is required in hmac auth mode")
		}
		if cfg.InternalHMACSecret == "" {
			return fmt.Errorf("internal-hmac-secret is required in hmac auth mode")
		}
		_, err := signing.NewSigner(signing.SignerConfig{
			KeyID:  strings.TrimSpace(cfg.InternalHMACKeyID),
			Secret: []byte(cfg.InternalHMACSecret),
		})
		if err != nil {
			return fmt.Errorf("create internal HMAC signer: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported internal auth mode %q", cfg.InternalAuthMode)
	}
}

func applyInternalAuth(request *http.Request, body []byte, cfg config) error {
	switch cfg.InternalAuthMode {
	case internalAuthModeToken:
		if cfg.InternalToken != "" {
			request.Header.Set(downlink.InternalTokenHeader, cfg.InternalToken)
		}
		return nil
	case internalAuthModeHMAC:
		signer, err := signing.NewSigner(signing.SignerConfig{
			KeyID:  strings.TrimSpace(cfg.InternalHMACKeyID),
			Secret: []byte(cfg.InternalHMACSecret),
		})
		if err != nil {
			return fmt.Errorf("create internal HMAC signer: %w", err)
		}
		if err := signer.Sign(request, body); err != nil {
			return fmt.Errorf("sign internal request: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported internal auth mode %q", cfg.InternalAuthMode)
	}
}

func run(ctx context.Context, cfg config) error {
	if err := validateInternalAuthConfig(cfg); err != nil {
		return err
	}
	if err := waitHTTP(ctx, cfg.InternalURL+"/metrics"); err != nil {
		return fmt.Errorf("wait gateway metrics: %w", err)
	}

	db, err := sql.Open("pgx", cfg.PostgresDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := waitPostgres(ctx, db); err != nil {
		return err
	}

	runID := time.Now().UnixNano()
	offlineMessageID := fmt.Sprintf("e2e-%d-offline", runID)
	onlineMessageID := fmt.Sprintf("e2e-%d-online", runID)
	reconnectMessageID := fmt.Sprintf("e2e-%d-reconnect", runID)
	terminalMessageID := fmt.Sprintf("e2e-%d-terminal", runID)
	upstreamMessageID := fmt.Sprintf("e2e-%d-upstream", runID)
	offlineBody := []byte("offline-before-client")
	onlineBody := []byte("online-after-client")

	var terminalCollector terminalEventCollector
	if cfg.CheckTerminalEvent {
		terminalCollector, err = newTerminalEventCollector(ctx, cfg, runID)
		if err != nil {
			return fmt.Errorf("start terminal event collector: %w", err)
		}
		defer terminalCollector.Close()
	}
	if cfg.CheckQueueCapacity {
		if err := checkQueueCapacity(ctx, cfg, db, runID); err != nil {
			return err
		}
	}
	if cfg.CheckRetryFairness {
		if err := checkRetryFairness(ctx, cfg, db, runID); err != nil {
			return err
		}
	}
	var adminStorage adminStorageFixture
	if cfg.CheckAdminStorage {
		adminStorage, err = checkAdminStorage(ctx, cfg, db)
		if err != nil {
			return err
		}
		fmt.Println("admin storage verified")
	}
	if cfg.CheckBulkRequeue {
		if err := checkBulkRequeue(ctx, cfg, db, runID, adminStorage); err != nil {
			return err
		}
	}

	fmt.Println("checking offline queue and idempotency path")
	offlineCreated, err := pushDownlink(ctx, cfg, offlineMessageID, offlineBody, http.StatusAccepted)
	if err != nil {
		return fmt.Errorf("push offline downlink: %w", err)
	}
	if err := validatePushOutcome(offlineCreated, offlineMessageID, downlink.SubmissionStateCreated, downlink.MessageStatusPending); err != nil {
		return fmt.Errorf("validate offline downlink creation: %w", err)
	}
	offlineExisting, err := pushDownlink(ctx, cfg, offlineMessageID, offlineBody, http.StatusOK)
	if err != nil {
		return fmt.Errorf("replay offline downlink: %w", err)
	}
	if err := validatePushOutcome(offlineExisting, offlineMessageID, downlink.SubmissionStateExisting, downlink.MessageStatusPending); err != nil {
		return fmt.Errorf("validate offline downlink replay: %w", err)
	}
	offlineConflict, err := pushDownlink(ctx, cfg, offlineMessageID, []byte("offline-conflict"), http.StatusConflict)
	if err != nil {
		return fmt.Errorf("conflict offline downlink: %w", err)
	}
	if err := validatePushConflict(offlineConflict, offlineMessageID); err != nil {
		return fmt.Errorf("validate offline downlink conflict: %w", err)
	}
	if err := waitMessageStatus(ctx, db, offlineMessageID, string(downlink.MessageStatusPending)); err != nil {
		return fmt.Errorf("wait offline message pending: %w", err)
	}
	if cfg.ExpectPolicyName != "" {
		if err := waitMessagePolicy(ctx, db, offlineMessageID, cfg.ExpectPolicyName); err != nil {
			return fmt.Errorf("wait offline message policy: %w", err)
		}
		status, err := getMessageStatus(ctx, cfg, offlineMessageID)
		if err != nil {
			return fmt.Errorf("query offline message policy: %w", err)
		}
		if status.PolicyName != cfg.ExpectPolicyName {
			return fmt.Errorf("offline message policy_name = %q, want %q", status.PolicyName, cfg.ExpectPolicyName)
		}
		fmt.Printf("delivery policy verified: %s\n", status.PolicyName)
	}

	client := newE2EClient(cfg)
	if err := client.Start(ctx); err != nil {
		return err
	}
	defer func() {
		if client != nil {
			client.Stop()
		}
	}()

	if err := client.WaitAck(ctx, client.bindMessageID, protocol.AckAccepted); err != nil {
		return fmt.Errorf("wait bind ack: %w", err)
	}
	fmt.Println("client bound")

	if err := checkDebugCluster(ctx, cfg); err != nil {
		return err
	}

	offlinePacket, err := client.WaitDownlinkPacket(ctx, offlineMessageID)
	if err != nil {
		return fmt.Errorf("wait offline downlink flush: %w", err)
	}
	if !bytes.Equal(offlinePacket.Body, offlineBody) {
		return fmt.Errorf("offline downlink body = %q, want %q", offlinePacket.Body, offlineBody)
	}
	if err := waitMessageStatus(ctx, db, offlineMessageID, string(downlink.MessageStatusDelivered)); err != nil {
		return fmt.Errorf("wait offline message delivered status: %w", err)
	}
	if err := client.ExpectNoDownlink(ctx, offlineMessageID, duplicateCheckDelay); err != nil {
		return fmt.Errorf("offline idempotency delivery check: %w", err)
	}
	fmt.Println("offline message delivered")

	if cfg.OnlinePushDelay > 0 {
		fmt.Printf("waiting before online push: %s\n", cfg.OnlinePushDelay)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(cfg.OnlinePushDelay):
		}
	}

	fmt.Println("checking online push and idempotency path")
	onlineCreated, err := pushDownlink(ctx, cfg, onlineMessageID, onlineBody, http.StatusOK)
	if err != nil {
		return fmt.Errorf("push online downlink: %w", err)
	}
	if err := validatePushOutcome(onlineCreated, onlineMessageID, downlink.SubmissionStateCreated, downlink.MessageStatusSent); err != nil {
		return fmt.Errorf("validate online downlink creation: %w", err)
	}
	onlinePacket, err := client.WaitDownlinkPacket(ctx, onlineMessageID)
	if err != nil {
		return fmt.Errorf("wait online downlink: %w", err)
	}
	if !bytes.Equal(onlinePacket.Body, onlineBody) {
		return fmt.Errorf("online downlink body = %q, want %q", onlinePacket.Body, onlineBody)
	}
	if err := waitMessageStatus(ctx, db, onlineMessageID, string(downlink.MessageStatusDelivered)); err != nil {
		return fmt.Errorf("wait online message delivered status: %w", err)
	}
	onlineExisting, err := pushDownlink(ctx, cfg, onlineMessageID, onlineBody, http.StatusOK)
	if err != nil {
		return fmt.Errorf("replay online downlink: %w", err)
	}
	if err := validatePushOutcome(onlineExisting, onlineMessageID, downlink.SubmissionStateExisting, downlink.MessageStatusDelivered); err != nil {
		return fmt.Errorf("validate online downlink replay: %w", err)
	}
	onlineConflict, err := pushDownlink(ctx, cfg, onlineMessageID, []byte("online-conflict"), http.StatusConflict)
	if err != nil {
		return fmt.Errorf("conflict online downlink: %w", err)
	}
	if err := validatePushConflict(onlineConflict, onlineMessageID); err != nil {
		return fmt.Errorf("validate online downlink conflict: %w", err)
	}
	if err := client.ExpectNoDownlink(ctx, onlineMessageID, duplicateCheckDelay); err != nil {
		return fmt.Errorf("online idempotency delivery check: %w", err)
	}
	fmt.Println("online message delivered")

	if cfg.CheckReconnectRetry {
		reconnected, err := checkReconnectRetry(ctx, cfg, db, client, reconnectMessageID)
		if err != nil {
			return err
		}
		client = reconnected
	}

	if cfg.CheckTerminalEvent {
		if err := checkTerminalEvent(ctx, cfg, db, terminalCollector, terminalMessageID); err != nil {
			return err
		}
	}

	fmt.Println("checking NSQ upstream publish path")
	if err := client.SendUpstream(upstreamMessageID, 2001, []byte("nsq-upstream")); err != nil {
		return err
	}
	if err := client.WaitAck(ctx, upstreamMessageID, protocol.AckAccepted); err != nil {
		return fmt.Errorf("wait upstream ack: %w", err)
	}
	fmt.Println("nsq upstream accepted")

	if err := checkMetrics(ctx, cfg); err != nil {
		return err
	}
	fmt.Println("metrics exposed")

	return nil
}

type debugRouteResponse struct {
	Code              string             `json:"code"`
	GatewayNode       string             `json:"gateway_node"`
	ClientID          string             `json:"client_id"`
	DeviceID          string             `json:"device_id"`
	LocalSessionFound bool               `json:"local_session_found"`
	ClusterEnabled    bool               `json:"cluster_enabled"`
	ClusterRouteFound bool               `json:"cluster_route_found"`
	ClusterRoute      *debugClusterRoute `json:"cluster_route"`
}

type debugClusterRoute struct {
	ClientID     string `json:"client_id"`
	DeviceID     string `json:"device_id"`
	SessionID    string `json:"session_id"`
	GatewayNode  string `json:"gateway_node"`
	InternalAddr string `json:"internal_addr"`
	TokenID      string `json:"token_id"`
}

type debugSessionsResponse struct {
	Code          string         `json:"code"`
	GatewayNode   string         `json:"gateway_node"`
	ClientID      string         `json:"client_id"`
	Total         int            `json:"total"`
	UniqueClients int            `json:"unique_clients"`
	Sessions      []debugSession `json:"sessions"`
}

type debugSession struct {
	SessionID   string `json:"session_id"`
	ClientID    string `json:"client_id"`
	DeviceID    string `json:"device_id"`
	TokenID     string `json:"token_id"`
	GatewayNode string `json:"gateway_node"`
}

type adminDiagnosticsResponse struct {
	Code         string              `json:"code"`
	GatewayNode  string              `json:"gateway_node"`
	AdminConsole adminConsoleSummary `json:"admin_console"`
}

type adminConsoleSummary struct {
	Session adminConsoleSessionSummary `json:"session"`
	Audit   adminConsoleAuditSummary   `json:"audit"`
}

type adminConsoleSessionSummary struct {
	Enabled         bool   `json:"enabled"`
	CookieName      string `json:"cookie_name"`
	StorageType     string `json:"storage_type"`
	RedisConfigured bool   `json:"redis_configured"`
}

type adminConsoleAuditSummary struct {
	StorageType        string `json:"storage_type"`
	StoreConfigured    bool   `json:"store_configured"`
	PostgresConfigured bool   `json:"postgres_configured"`
}

type adminSessionResponse struct {
	Code        string            `json:"code"`
	Reason      string            `json:"reason,omitempty"`
	GatewayNode string            `json:"gateway_node"`
	Session     *adminSessionInfo `json:"session,omitempty"`
}

type adminSessionInfo struct {
	SessionID   string `json:"session_id"`
	Principal   string `json:"principal"`
	Role        string `json:"role"`
	CSRFToken   string `json:"csrf_token,omitempty"`
	ExpiresInMS int64  `json:"expires_in_ms"`
}

type adminSessionLoginResult struct {
	Response adminSessionResponse
	Cookie   *http.Cookie
}

type adminStorageFixture struct {
	PeerURL            string
	PrimaryDiagnostics adminDiagnosticsResponse
	PeerDiagnostics    adminDiagnosticsResponse
	Login              adminSessionLoginResult
	PeerCookie         *http.Cookie
}

func checkAdminStorage(ctx context.Context, cfg config, db *sql.DB) (adminStorageFixture, error) {
	peerURL := cfg.AdminSessionPeerURL
	if peerURL == "" {
		peerURL = cfg.ExpectSessionURL
	}
	if peerURL == "" {
		peerURL = cfg.InternalURL
	}

	primaryDiagnostics, err := fetchAdminDiagnostics(ctx, cfg, cfg.InternalURL)
	if err != nil {
		return adminStorageFixture{}, fmt.Errorf("fetch primary admin diagnostics: %w", err)
	}
	if err := validateAdminStorageDiagnostics(primaryDiagnostics, true); err != nil {
		return adminStorageFixture{}, fmt.Errorf("primary admin diagnostics: %w", err)
	}

	peerDiagnostics, err := fetchAdminDiagnostics(ctx, cfg, peerURL)
	if err != nil {
		return adminStorageFixture{}, fmt.Errorf("fetch peer admin diagnostics: %w", err)
	}
	if err := validateAdminStorageDiagnostics(peerDiagnostics, true); err != nil {
		return adminStorageFixture{}, fmt.Errorf("peer admin diagnostics: %w", err)
	}

	login, err := loginAdminSession(ctx, cfg, cfg.InternalURL, primaryDiagnostics.AdminConsole.Session.CookieName)
	if err != nil {
		return adminStorageFixture{}, fmt.Errorf("login admin session: %w", err)
	}
	sessionID := login.Response.Session.SessionID
	if err := waitAdminAuditEvent(ctx, db, "admin_session_login", "success", sessionID, primaryDiagnostics.GatewayNode); err != nil {
		return adminStorageFixture{}, fmt.Errorf("wait admin audit login event: %w", err)
	}

	if err := verifyAdminSessionMe(ctx, cfg, cfg.InternalURL, login.Cookie, sessionID, primaryDiagnostics.GatewayNode); err != nil {
		return adminStorageFixture{}, fmt.Errorf("verify primary admin session: %w", err)
	}

	peerCookie := *login.Cookie
	peerCookie.Name = peerDiagnostics.AdminConsole.Session.CookieName
	if err := verifyAdminSessionMe(ctx, cfg, peerURL, &peerCookie, sessionID, peerDiagnostics.GatewayNode); err != nil {
		return adminStorageFixture{}, fmt.Errorf("verify peer admin session via redis: %w", err)
	}

	return adminStorageFixture{
		PeerURL:            peerURL,
		PrimaryDiagnostics: primaryDiagnostics,
		PeerDiagnostics:    peerDiagnostics,
		Login:              login,
		PeerCookie:         &peerCookie,
	}, nil
}

func fetchAdminDiagnostics(ctx context.Context, cfg config, baseURL string) (adminDiagnosticsResponse, error) {
	var resp adminDiagnosticsResponse
	if err := getInternalJSON(ctx, cfg, baseURL, "/internal/admin/diagnostics", &resp); err != nil {
		return adminDiagnosticsResponse{}, err
	}
	return resp, nil
}

func validateAdminStorageDiagnostics(resp adminDiagnosticsResponse, requirePostgresAudit bool) error {
	if resp.Code != "ok" {
		return fmt.Errorf("diagnostics code = %q, want ok", resp.Code)
	}
	session := resp.AdminConsole.Session
	if !session.Enabled {
		return fmt.Errorf("admin session is disabled")
	}
	if session.StorageType != "redis" || !session.RedisConfigured {
		return fmt.Errorf("admin session store = %s redis_configured=%v, want redis true", session.StorageType, session.RedisConfigured)
	}
	if strings.TrimSpace(session.CookieName) == "" {
		return fmt.Errorf("admin session cookie name is empty")
	}

	audit := resp.AdminConsole.Audit
	if !audit.StoreConfigured {
		return fmt.Errorf("admin audit store is not configured")
	}
	if requirePostgresAudit && (audit.StorageType != "postgres" || !audit.PostgresConfigured) {
		return fmt.Errorf("admin audit store = %s postgres_configured=%v, want postgres true", audit.StorageType, audit.PostgresConfigured)
	}
	return nil
}

func loginAdminSession(ctx context.Context, cfg config, baseURL string, cookieName string) (adminSessionLoginResult, error) {
	requestCtx, cancel := context.WithTimeout(ctx, downlinkPushTimeout)
	defer cancel()

	body := []byte(`{}`)
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, baseURL+"/internal/admin/session/login", bytes.NewReader(body))
	if err != nil {
		return adminSessionLoginResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := applyInternalAuth(req, body, cfg); err != nil {
		return adminSessionLoginResult{}, err
	}

	var resp adminSessionResponse
	cookies, err := doAdminSessionRequest(req, http.StatusOK, &resp)
	if err != nil {
		return adminSessionLoginResult{}, err
	}
	if resp.Code != "ok" || resp.Session == nil {
		return adminSessionLoginResult{}, fmt.Errorf("login response = %+v, want ok session", resp)
	}
	if resp.Session.SessionID == "" {
		return adminSessionLoginResult{}, fmt.Errorf("login session_id is empty")
	}

	cookie := findCookie(cookies, cookieName)
	if cookie == nil {
		return adminSessionLoginResult{}, fmt.Errorf("login response missing cookie %q", cookieName)
	}
	if strings.TrimSpace(cookie.Value) == "" {
		return adminSessionLoginResult{}, fmt.Errorf("login cookie %q is empty", cookieName)
	}
	return adminSessionLoginResult{Response: resp, Cookie: cookie}, nil
}

func verifyAdminSessionMe(ctx context.Context, cfg config, baseURL string, cookie *http.Cookie, wantSessionID string, wantGatewayNode string) error {
	requestCtx, cancel := context.WithTimeout(ctx, downlinkPushTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, baseURL+"/internal/admin/session/me", nil)
	if err != nil {
		return err
	}
	req.AddCookie(cookie)

	var resp adminSessionResponse
	if _, err := doAdminSessionRequest(req, http.StatusOK, &resp); err != nil {
		return err
	}
	if resp.Code != "ok" || resp.Session == nil {
		return fmt.Errorf("me response = %+v, want ok session", resp)
	}
	if wantGatewayNode != "" && resp.GatewayNode != wantGatewayNode {
		return fmt.Errorf("me gateway_node = %q, want %q", resp.GatewayNode, wantGatewayNode)
	}
	if resp.Session.SessionID != wantSessionID {
		return fmt.Errorf("me session_id = %q, want %q", resp.Session.SessionID, wantSessionID)
	}
	if resp.Session.CSRFToken == "" {
		return fmt.Errorf("me csrf_token is empty")
	}
	return nil
}

func doAdminSessionRequest(req *http.Request, wantStatus int, target any) ([]*http.Cookie, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != wantStatus {
		return nil, fmt.Errorf("%s %s status = %d, want %d, body = %s", req.Method, req.URL.String(), resp.StatusCode, wantStatus, string(respBody))
	}
	if err := sonic.Unmarshal(respBody, target); err != nil {
		return nil, err
	}
	return resp.Cookies(), nil
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			copy := *cookie
			return &copy
		}
	}
	return nil
}

func waitAdminAuditEvent(ctx context.Context, db *sql.DB, action, result, sessionID, gatewayNode string) error {
	return waitUntil(ctx, func() (bool, error) {
		var count int
		err := db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM z_courier_admin_audit_events
WHERE action = $1
  AND result = $2
  AND admin_session_id = $3
  AND gateway_node = $4
`, action, result, sessionID, gatewayNode).Scan(&count)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return count > 0, nil
	})
}

func checkBulkRequeue(ctx context.Context, cfg config, db *sql.DB, runID int64, admin adminStorageFixture) error {
	fmt.Println("checking cross-node bulk requeue path")
	if admin.Login.Response.Session == nil || admin.PeerCookie == nil {
		return fmt.Errorf("bulk requeue admin session fixture is incomplete")
	}
	messagePrefix := fmt.Sprintf("e2e-%d-bulk-requeue-", runID)
	defer cleanupMessagesByPrefix(db, messagePrefix)

	deviceID := fmt.Sprintf("%s-bulk-requeue-offline-%d", cfg.DeviceID, runID)
	failedIDs := []string{messagePrefix + "failed-1", messagePrefix + "failed-2"}
	for _, messageID := range failedIDs {
		response, err := pushDownlinkTarget(
			ctx,
			cfg,
			deviceID,
			2999,
			messageID,
			[]byte("bulk-requeue-terminal"),
			http.StatusAccepted,
		)
		if err != nil {
			return fmt.Errorf("push bulk requeue terminal message %s: %w", messageID, err)
		}
		if err := validatePushOutcome(response, messageID, downlink.SubmissionStateCreated, downlink.MessageStatusPending); err != nil {
			return fmt.Errorf("validate bulk requeue terminal message %s: %w", messageID, err)
		}
	}

	store, err := downlink.NewPostgresStore(ctx, downlink.PostgresStoreConfig{DSN: cfg.PostgresDSN})
	if err != nil {
		return fmt.Errorf("open bulk requeue fixture store: %w", err)
	}
	defer store.Close()
	for _, messageID := range failedIDs {
		if err := store.MarkFailed(ctx, messageID, downlink.TerminalTransition{
			Reason:      downlink.TerminalReasonMaxAttempts,
			GatewayNode: "e2e-bulk-requeue-fixture",
			At:          time.Now().UTC(),
			Attempted:   true,
			Publish:     true,
		}); err != nil {
			return fmt.Errorf("mark bulk requeue message %s failed: %w", messageID, err)
		}
		if err := waitMessageStatus(ctx, db, messageID, string(downlink.MessageStatusFailed)); err != nil {
			return fmt.Errorf("wait bulk requeue message %s failed: %w", messageID, err)
		}
	}

	fillerCount := cfg.ExpectPerDeviceLimit - 1
	for index := 0; index < fillerCount; index++ {
		messageID := fmt.Sprintf("%sfiller-%02d", messagePrefix, index+1)
		response, err := pushDownlinkTarget(
			ctx,
			cfg,
			deviceID,
			2001,
			messageID,
			[]byte("bulk-requeue-capacity-filler"),
			http.StatusAccepted,
		)
		if err != nil {
			return fmt.Errorf("push bulk requeue capacity filler %s: %w", messageID, err)
		}
		if err := validatePushOutcome(response, messageID, downlink.SubmissionStateCreated, downlink.MessageStatusPending); err != nil {
			return fmt.Errorf("validate bulk requeue capacity filler %s: %w", messageID, err)
		}
	}

	response, err := bulkRequeueWithAdminSession(ctx, admin, failedIDs)
	if err != nil {
		return err
	}
	if err := validateBulkRequeueResponse(response, failedIDs, cfg.ExpectTerminalPolicy, cfg.ExpectPerDeviceLimit); err != nil {
		return err
	}

	for _, node := range []struct {
		name string
		url  string
	}{
		{name: admin.PrimaryDiagnostics.GatewayNode, url: cfg.InternalURL},
		{name: admin.PeerDiagnostics.GatewayNode, url: admin.PeerURL},
	} {
		requeued, err := getMessageStatusAt(ctx, cfg, node.url, failedIDs[0])
		if err != nil {
			return fmt.Errorf("query requeued message from %s: %w", node.name, err)
		}
		if requeued.Status != downlink.MessageStatusPending || requeued.Attempts != 0 ||
			requeued.PolicyName != cfg.ExpectTerminalPolicy || requeued.TerminalReason != "" {
			return fmt.Errorf("requeued message from %s = %+v", node.name, requeued)
		}

		rejected, err := getMessageStatusAt(ctx, cfg, node.url, failedIDs[1])
		if err != nil {
			return fmt.Errorf("query capacity-rejected message from %s: %w", node.name, err)
		}
		if rejected.Status != downlink.MessageStatusFailed || rejected.PolicyName != cfg.ExpectTerminalPolicy ||
			rejected.TerminalReason != downlink.TerminalReasonMaxAttempts {
			return fmt.Errorf("capacity-rejected message from %s = %+v", node.name, rejected)
		}
	}

	var pending int
	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM z_courier_downlink_messages
WHERE client_id = $1
  AND device_id = $2
  AND status = $3
`, cfg.ClientID, deviceID, string(downlink.MessageStatusPending)).Scan(&pending); err != nil {
		return fmt.Errorf("count bulk requeue pending messages: %w", err)
	}
	if pending != cfg.ExpectPerDeviceLimit {
		return fmt.Errorf("bulk requeue pending messages = %d, want %d", pending, cfg.ExpectPerDeviceLimit)
	}

	sessionID := admin.Login.Response.Session.SessionID
	if err := waitBulkRequeueAudit(
		ctx,
		db,
		sessionID,
		admin.PeerDiagnostics.GatewayNode,
		failedIDs[0],
		failedIDs[1],
	); err != nil {
		return fmt.Errorf("wait bulk requeue audit: %w", err)
	}

	fmt.Printf(
		"bulk requeue verified: origin=%s executor=%s success=%d failed=%d pending=%d\n",
		admin.PrimaryDiagnostics.GatewayNode,
		admin.PeerDiagnostics.GatewayNode,
		response.Success,
		response.Failed,
		pending,
	)
	return nil
}

func bulkRequeueWithAdminSession(
	ctx context.Context,
	admin adminStorageFixture,
	messageIDs []string,
) (downlink.BulkRequeueResponse, error) {
	var response downlink.BulkRequeueResponse
	body, err := sonic.Marshal(downlink.BulkRequeueRequest{MessageIDs: messageIDs})
	if err != nil {
		return response, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, downlinkPushTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(
		requestCtx,
		http.MethodPost,
		admin.PeerURL+"/internal/messages/requeue",
		bytes.NewReader(body),
	)
	if err != nil {
		return response, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ZCourier-CSRF-Token", admin.Login.Response.Session.CSRFToken)
	req.AddCookie(admin.PeerCookie)
	if _, err := doAdminSessionRequest(req, http.StatusMultiStatus, &response); err != nil {
		return downlink.BulkRequeueResponse{}, fmt.Errorf("bulk requeue request: %w", err)
	}
	return response, nil
}

func validateBulkRequeueResponse(
	response downlink.BulkRequeueResponse,
	messageIDs []string,
	policyName string,
	capacityLimit int,
) error {
	if len(messageIDs) != 2 {
		return fmt.Errorf("bulk requeue message IDs = %d, want 2", len(messageIDs))
	}
	if response.Code != "partial_failure" || response.Total != 2 || response.Success != 1 || response.Failed != 1 ||
		len(response.Results) != 2 {
		return fmt.Errorf("bulk requeue response = %+v, want one success and one failure", response)
	}
	success := response.Results[0]
	if success.Code != "ok" || success.MessageID != messageIDs[0] || success.Status != downlink.MessageStatusPending ||
		success.Attempts != 0 || success.PolicyName != policyName || success.TerminalReason != "" {
		return fmt.Errorf("bulk requeue success = %+v", success)
	}
	rejected := response.Results[1]
	if rejected.Code != "queue_capacity_exceeded" || rejected.MessageID != messageIDs[1] ||
		rejected.Status != downlink.MessageStatusFailed || rejected.PolicyName != policyName ||
		rejected.TerminalReason != downlink.TerminalReasonMaxAttempts ||
		rejected.CapacityScope != downlink.QueueCapacityScopeDevice || rejected.CapacityLimit != capacityLimit ||
		rejected.CapacityPending != capacityLimit {
		return fmt.Errorf("bulk requeue capacity failure = %+v", rejected)
	}
	return nil
}

func waitBulkRequeueAudit(
	ctx context.Context,
	db *sql.DB,
	sessionID string,
	gatewayNode string,
	successMessageID string,
	failedMessageID string,
) error {
	return waitUntil(ctx, func() (bool, error) {
		var summaryCount int
		var successCount int
		var failedCount int
		err := db.QueryRowContext(ctx, `
SELECT
  COUNT(*) FILTER (
    WHERE action = 'downlink_message_bulk_requeue'
      AND result = 'partial_failure'
      AND http_status = 207
      AND details->>'total' = '2'
      AND details->>'success' = '1'
      AND details->>'failed' = '1'
  ),
  COUNT(*) FILTER (
    WHERE action = 'downlink_message_action'
      AND result = 'success'
      AND http_status = 200
      AND message_id = $3
      AND details->>'action' = 'requeue'
      AND details->>'batch' = 'true'
      AND details->>'batch_index' = '0'
      AND details->>'message_status' = 'pending'
  ),
  COUNT(*) FILTER (
    WHERE action = 'downlink_message_action'
      AND result = 'queue_capacity_exceeded'
      AND http_status = 429
      AND message_id = $4
      AND details->>'action' = 'requeue'
      AND details->>'batch' = 'true'
      AND details->>'batch_index' = '1'
      AND details->>'message_status' = 'failed'
  )
FROM z_courier_admin_audit_events
WHERE admin_session_id = $1
  AND gateway_node = $2
  AND path = '/internal/messages/requeue'
`, sessionID, gatewayNode, successMessageID, failedMessageID).Scan(&summaryCount, &successCount, &failedCount)
		if err != nil {
			return false, err
		}
		return summaryCount == 1 && successCount == 1 && failedCount == 1, nil
	})
}

func checkDebugCluster(ctx context.Context, cfg config) error {
	checked := false
	if cfg.ExpectRouteNode != "" {
		if err := waitDebugRoute(ctx, cfg); err != nil {
			return err
		}
		checked = true
	}
	if cfg.ExpectSessionURL != "" {
		if err := waitDebugSessions(ctx, cfg); err != nil {
			return err
		}
		checked = true
	}
	if checked {
		fmt.Println("cluster debug route verified")
	}

	return nil
}

func checkReconnectRetry(ctx context.Context, cfg config, db *sql.DB, client *e2eClient, messageID string) (*e2eClient, error) {
	if cfg.ExpectSessionURL == "" {
		return nil, fmt.Errorf("check reconnect retry requires expect-session-url")
	}

	fmt.Println("checking reconnect retry path")
	client.Stop()
	if err := waitDebugSessionsGone(ctx, cfg); err != nil {
		return nil, fmt.Errorf("wait disconnected session cleanup: %w", err)
	}

	if _, err := pushDownlink(ctx, cfg, messageID, []byte("queued-while-disconnected"), http.StatusAccepted); err != nil {
		return nil, fmt.Errorf("push reconnect retry downlink: %w", err)
	}
	if err := waitMessagePendingAttempt(ctx, db, messageID); err != nil {
		return nil, fmt.Errorf("wait reconnect retry message pending: %w", err)
	}
	fmt.Println("reconnect retry message queued")

	reconnected := newE2EClient(cfg)
	if err := reconnected.Start(ctx); err != nil {
		return nil, fmt.Errorf("reconnect client: %w", err)
	}
	if err := reconnected.WaitAck(ctx, reconnected.bindMessageID, protocol.AckAccepted); err != nil {
		reconnected.Stop()
		return nil, fmt.Errorf("wait reconnect bind ack: %w", err)
	}
	if err := waitDebugSessions(ctx, cfg); err != nil {
		reconnected.Stop()
		return nil, fmt.Errorf("wait reconnect local session: %w", err)
	}
	if err := reconnected.WaitDownlink(ctx, messageID); err != nil {
		reconnected.Stop()
		return nil, fmt.Errorf("wait reconnect retry downlink: %w", err)
	}
	if err := waitMessageStatus(ctx, db, messageID, string(downlink.MessageStatusDelivered)); err != nil {
		reconnected.Stop()
		return nil, fmt.Errorf("wait reconnect retry delivered status: %w", err)
	}
	fmt.Println("reconnect retry message delivered")

	return reconnected, nil
}

func waitDebugRoute(ctx context.Context, cfg config) error {
	var lastErr error
	err := waitUntil(ctx, func() (bool, error) {
		resp, err := fetchDebugRoute(ctx, cfg)
		if err != nil {
			lastErr = err
			return false, nil
		}
		if err := validateDebugRoute(cfg, resp); err != nil {
			lastErr = err
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		if lastErr != nil {
			return fmt.Errorf("wait debug route: %w; last check: %v", err, lastErr)
		}
		return fmt.Errorf("wait debug route: %w", err)
	}

	return nil
}

func fetchDebugRoute(ctx context.Context, cfg config) (debugRouteResponse, error) {
	query := url.Values{}
	query.Set("client_id", cfg.ClientID)
	query.Set("device_id", cfg.DeviceID)

	var resp debugRouteResponse
	if err := getInternalJSON(ctx, cfg, cfg.InternalURL, "/internal/debug/route?"+query.Encode(), &resp); err != nil {
		return debugRouteResponse{}, err
	}

	return resp, nil
}

func validateDebugRoute(cfg config, resp debugRouteResponse) error {
	if resp.Code != "ok" {
		return fmt.Errorf("debug route code = %q, want ok", resp.Code)
	}
	if resp.LocalSessionFound {
		return fmt.Errorf("debug route local_session_found = true, want false on %s", cfg.InternalURL)
	}
	if !resp.ClusterEnabled {
		return fmt.Errorf("debug route cluster_enabled = false, want true")
	}
	if !resp.ClusterRouteFound || resp.ClusterRoute == nil {
		return fmt.Errorf("debug route cluster route not found")
	}
	if resp.ClusterRoute.ClientID != cfg.ClientID || resp.ClusterRoute.DeviceID != cfg.DeviceID {
		return fmt.Errorf("debug route target = %s/%s, want %s/%s", resp.ClusterRoute.ClientID, resp.ClusterRoute.DeviceID, cfg.ClientID, cfg.DeviceID)
	}
	if resp.ClusterRoute.GatewayNode != cfg.ExpectRouteNode {
		return fmt.Errorf("debug route gateway_node = %q, want %q", resp.ClusterRoute.GatewayNode, cfg.ExpectRouteNode)
	}
	if cfg.ExpectRouteInternalURL != "" && resp.ClusterRoute.InternalAddr != cfg.ExpectRouteInternalURL {
		return fmt.Errorf("debug route internal_addr = %q, want %q", resp.ClusterRoute.InternalAddr, cfg.ExpectRouteInternalURL)
	}
	if resp.ClusterRoute.SessionID == "" {
		return fmt.Errorf("debug route session_id is empty")
	}

	return nil
}

func waitDebugSessions(ctx context.Context, cfg config) error {
	var lastErr error
	err := waitUntil(ctx, func() (bool, error) {
		resp, err := fetchDebugSessions(ctx, cfg)
		if err != nil {
			lastErr = err
			return false, nil
		}
		if err := validateDebugSessions(cfg, resp); err != nil {
			lastErr = err
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		if lastErr != nil {
			return fmt.Errorf("wait debug sessions: %w; last check: %v", err, lastErr)
		}
		return fmt.Errorf("wait debug sessions: %w", err)
	}

	return nil
}

func waitDebugSessionsGone(ctx context.Context, cfg config) error {
	var lastErr error
	err := waitUntil(ctx, func() (bool, error) {
		resp, err := fetchDebugSessions(ctx, cfg)
		if err != nil {
			lastErr = err
			return false, nil
		}
		if err := validateDebugSessionsGone(cfg, resp); err != nil {
			lastErr = err
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		if lastErr != nil {
			return fmt.Errorf("wait debug sessions gone: %w; last check: %v", err, lastErr)
		}
		return fmt.Errorf("wait debug sessions gone: %w", err)
	}

	return nil
}

func fetchDebugSessions(ctx context.Context, cfg config) (debugSessionsResponse, error) {
	query := url.Values{}
	query.Set("client_id", cfg.ClientID)
	query.Set("limit", "10")

	var resp debugSessionsResponse
	if err := getInternalJSON(ctx, cfg, cfg.ExpectSessionURL, "/internal/debug/sessions?"+query.Encode(), &resp); err != nil {
		return debugSessionsResponse{}, err
	}

	return resp, nil
}

func validateDebugSessions(cfg config, resp debugSessionsResponse) error {
	if resp.Code != "ok" {
		return fmt.Errorf("debug sessions code = %q, want ok", resp.Code)
	}
	if cfg.ExpectSessionNode != "" && resp.GatewayNode != cfg.ExpectSessionNode {
		return fmt.Errorf("debug sessions gateway_node = %q, want %q", resp.GatewayNode, cfg.ExpectSessionNode)
	}
	if resp.Total == 0 {
		return fmt.Errorf("debug sessions total = 0, want local session")
	}
	if resp.UniqueClients == 0 {
		return fmt.Errorf("debug sessions unique_clients = 0, want local client")
	}
	for _, found := range resp.Sessions {
		if found.ClientID != cfg.ClientID || found.DeviceID != cfg.DeviceID {
			continue
		}
		if cfg.ExpectSessionNode != "" && found.GatewayNode != cfg.ExpectSessionNode {
			return fmt.Errorf("debug session gateway_node = %q, want %q", found.GatewayNode, cfg.ExpectSessionNode)
		}
		if found.SessionID == "" {
			return fmt.Errorf("debug session session_id is empty")
		}
		return nil
	}

	return fmt.Errorf("debug sessions missing %s/%s in %d sessions", cfg.ClientID, cfg.DeviceID, len(resp.Sessions))
}

func validateDebugSessionsGone(cfg config, resp debugSessionsResponse) error {
	if resp.Code != "ok" {
		return fmt.Errorf("debug sessions code = %q, want ok", resp.Code)
	}
	if cfg.ExpectSessionNode != "" && resp.GatewayNode != cfg.ExpectSessionNode {
		return fmt.Errorf("debug sessions gateway_node = %q, want %q", resp.GatewayNode, cfg.ExpectSessionNode)
	}
	for _, found := range resp.Sessions {
		if found.ClientID == cfg.ClientID && found.DeviceID == cfg.DeviceID {
			return fmt.Errorf("debug session %s/%s is still present", cfg.ClientID, cfg.DeviceID)
		}
	}

	return nil
}

func getInternalJSON(ctx context.Context, cfg config, baseURL, path string, target any) error {
	requestCtx, cancel := context.WithTimeout(ctx, downlinkPushTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return err
	}
	if err := applyInternalAuth(req, nil, cfg); err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("GET %s%s status = %d, body = %s", baseURL, path, resp.StatusCode, string(respBody))
	}
	if err := sonic.Unmarshal(respBody, target); err != nil {
		return err
	}

	return nil
}

func waitHTTP(ctx context.Context, url string) error {
	return waitUntil(ctx, func() (bool, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return false, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false, nil
		}
		defer resp.Body.Close()
		return resp.StatusCode >= 200 && resp.StatusCode < 500, nil
	})
}

func waitPostgres(ctx context.Context, db *sql.DB) error {
	return waitUntil(ctx, func() (bool, error) {
		pingCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if err := db.PingContext(pingCtx); err != nil {
			return false, nil
		}
		return true, nil
	})
}

func pushDownlink(ctx context.Context, cfg config, messageID string, body []byte, wantStatus int) (downlink.PushResponse, error) {
	return pushDownlinkTarget(ctx, cfg, cfg.DeviceID, 2001, messageID, body, wantStatus)
}

func pushDownlinkTarget(
	ctx context.Context,
	cfg config,
	deviceID string,
	msgID uint32,
	messageID string,
	body []byte,
	wantStatus int,
) (downlink.PushResponse, error) {
	var zero downlink.PushResponse
	requestCtx, cancel := context.WithTimeout(ctx, downlinkPushTimeout)
	defer cancel()

	reqBody, err := sonic.Marshal(downlink.PushRequest{
		ClientID:    cfg.ClientID,
		DeviceID:    deviceID,
		MsgID:       msgID,
		MessageID:   messageID,
		TraceID:     messageID,
		AckRequired: true,
		Body:        body,
	})
	if err != nil {
		return zero, err
	}

	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, cfg.InternalURL+"/internal/push", bytes.NewReader(reqBody))
	if err != nil {
		return zero, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := applyInternalAuth(req, reqBody, cfg); err != nil {
		return zero, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		return zero, fmt.Errorf("push %s status = %d, want %d, body = %s", messageID, resp.StatusCode, wantStatus, string(respBody))
	}

	var pushResp downlink.PushResponse
	if err := sonic.Unmarshal(respBody, &pushResp); err != nil {
		return zero, err
	}
	if wantStatus < http.StatusBadRequest && pushResp.Code != "ok" {
		return zero, fmt.Errorf("push %s code = %q, reason = %s", messageID, pushResp.Code, pushResp.Reason)
	}

	return pushResp, nil
}

func validatePushOutcome(response downlink.PushResponse, messageID, submissionState string, messageStatus downlink.MessageStatus) error {
	if response.Code != "ok" {
		return fmt.Errorf("code = %q, want ok: %s", response.Code, response.Reason)
	}
	if response.MessageID != messageID {
		return fmt.Errorf("message_id = %q, want %q", response.MessageID, messageID)
	}
	if response.SubmissionState != submissionState {
		return fmt.Errorf("submission_state = %q, want %q", response.SubmissionState, submissionState)
	}
	if response.MessageStatus != messageStatus {
		return fmt.Errorf("message_status = %q, want %q", response.MessageStatus, messageStatus)
	}
	return nil
}

func validatePushConflict(response downlink.PushResponse, messageID string) error {
	if response.Code != "message_id_conflict" {
		return fmt.Errorf("code = %q, want message_id_conflict", response.Code)
	}
	if response.MessageID != messageID {
		return fmt.Errorf("message_id = %q, want %q", response.MessageID, messageID)
	}
	return nil
}

func checkQueueCapacity(ctx context.Context, cfg config, db *sql.DB, runID int64) error {
	fmt.Println("checking per-device queue capacity path")
	messagePrefix := fmt.Sprintf("e2e-%d-capacity-", runID)
	defer cleanupMessagesByPrefix(db, messagePrefix)
	deviceID := fmt.Sprintf("%s-capacity-offline-%d", cfg.DeviceID, runID)
	acceptedIDs := make([]string, 0, cfg.ExpectPerDeviceLimit)
	for index := 0; index < cfg.ExpectPerDeviceLimit; index++ {
		messageID := fmt.Sprintf("e2e-%d-capacity-%d", runID, index+1)
		accepted, err := pushDownlinkTarget(
			ctx,
			cfg,
			deviceID,
			2001,
			messageID,
			[]byte(fmt.Sprintf("capacity-%d", index+1)),
			http.StatusAccepted,
		)
		if err != nil {
			return fmt.Errorf("push capacity message %d: %w", index+1, err)
		}
		if err := validatePushOutcome(accepted, messageID, downlink.SubmissionStateCreated, downlink.MessageStatusPending); err != nil {
			return fmt.Errorf("validate capacity message %d: %w", index+1, err)
		}
		acceptedIDs = append(acceptedIDs, messageID)
	}
	rejectedID := fmt.Sprintf("e2e-%d-capacity-rejected", runID)

	replay, err := pushDownlinkTarget(ctx, cfg, deviceID, 2001, acceptedIDs[0], []byte("capacity-1"), http.StatusOK)
	if err != nil {
		return fmt.Errorf("replay capacity message: %w", err)
	}
	if err := validatePushOutcome(replay, acceptedIDs[0], downlink.SubmissionStateExisting, downlink.MessageStatusPending); err != nil {
		return fmt.Errorf("validate capacity replay: %w", err)
	}
	rejectedBody := []byte(fmt.Sprintf("capacity-%d", cfg.ExpectPerDeviceLimit+1))
	rejected, err := pushDownlinkTarget(ctx, cfg, deviceID, 2001, rejectedID, rejectedBody, http.StatusTooManyRequests)
	if err != nil {
		return fmt.Errorf("push rejected capacity message: %w", err)
	}
	if rejected.Code != "queue_capacity_exceeded" || rejected.CapacityScope != downlink.QueueCapacityScopeDevice ||
		rejected.CapacityLimit != cfg.ExpectPerDeviceLimit || rejected.CapacityPending != cfg.ExpectPerDeviceLimit {
		return fmt.Errorf("capacity rejection = %+v", rejected)
	}
	var persisted int
	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM z_courier_downlink_messages
WHERE message_id = $1
`, rejectedID).Scan(&persisted); err != nil {
		return fmt.Errorf("query rejected capacity message: %w", err)
	}
	if persisted != 0 {
		return fmt.Errorf("rejected capacity message persisted rows = %d", persisted)
	}
	fmt.Printf("queue capacity rejection verified: scope=%s limit=%d\n", rejected.CapacityScope, rejected.CapacityLimit)
	return nil
}

func checkRetryFairness(ctx context.Context, cfg config, db *sql.DB, runID int64) error {
	fmt.Println("checking hot-device retry fairness path")
	messagePrefix := fmt.Sprintf("e2e-%d-fair-", runID)
	defer cleanupMessagesByPrefix(db, messagePrefix)

	fixtures := []struct {
		name     string
		deviceID string
		count    int
	}{
		{name: "hot", deviceID: fmt.Sprintf("%s-fair-hot-%d", cfg.DeviceID, runID), count: retryFairnessHotMessages},
		{name: "cold-a", deviceID: fmt.Sprintf("%s-fair-cold-a-%d", cfg.DeviceID, runID), count: retryFairnessColdMessages},
		{name: "cold-b", deviceID: fmt.Sprintf("%s-fair-cold-b-%d", cfg.DeviceID, runID), count: retryFairnessColdMessages},
	}

	totalMessages := 0
	for round := 0; round < retryFairnessHotMessages; round++ {
		for _, fixture := range fixtures {
			if round >= fixture.count {
				continue
			}
			messageID := fmt.Sprintf("%s%s-%02d", messagePrefix, fixture.name, round+1)
			response, err := pushDownlinkTarget(
				ctx,
				cfg,
				fixture.deviceID,
				retryFairnessMsgID,
				messageID,
				[]byte("retry-fairness"),
				http.StatusAccepted,
			)
			if err != nil {
				return fmt.Errorf("push retry fairness message %s: %w", messageID, err)
			}
			if err := validatePushOutcome(response, messageID, downlink.SubmissionStateCreated, downlink.MessageStatusPending); err != nil {
				return fmt.Errorf("validate retry fairness message %s: %w", messageID, err)
			}
			totalMessages++
		}
	}

	result, err := db.ExecContext(ctx, `
UPDATE z_courier_downlink_messages
SET created_at = TIMESTAMPTZ '1900-01-01 00:00:00+00',
    next_retry_at = NOW() - INTERVAL '1 second',
    claim_owner = '',
    claim_until = NULL,
    updated_at = NOW()
WHERE message_id LIKE $1
  AND status = $2
`, messagePrefix+"%", string(downlink.MessageStatusPending))
	if err != nil {
		return fmt.Errorf("make retry fairness messages due: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count due retry fairness messages: %w", err)
	}
	if updated != int64(totalMessages) {
		return fmt.Errorf("due retry fairness messages = %d, want %d", updated, totalMessages)
	}

	scan, err := triggerRetryScan(ctx, cfg, cfg.RetryFairnessScanLimit)
	if err != nil {
		return fmt.Errorf("trigger retry fairness scan: %w", err)
	}
	if err := validateRetryFairnessScan(scan, cfg.RetryFairnessScanLimit); err != nil {
		return err
	}

	for _, fixture := range fixtures {
		var progressed int
		if err := db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM z_courier_downlink_messages
WHERE message_id LIKE $1
  AND device_id = $2
  AND attempts >= 2
`, messagePrefix+"%", fixture.deviceID).Scan(&progressed); err != nil {
			return fmt.Errorf("query retry fairness progress for %s: %w", fixture.name, err)
		}
		if progressed != 1 {
			return fmt.Errorf("retry fairness device %s progressed messages = %d, want 1", fixture.name, progressed)
		}
	}

	fmt.Printf(
		"retry fairness verified: backlog=%d:%d:%d scanned=%d selected_devices=%d max_per_device=%d\n",
		retryFairnessHotMessages,
		retryFairnessColdMessages,
		retryFairnessColdMessages,
		scan.Scanned,
		scan.SelectedDevices,
		scan.MaxPerDevice,
	)
	return nil
}

func triggerRetryScan(ctx context.Context, cfg config, limit int) (downlink.RetryScanResponse, error) {
	var zero downlink.RetryScanResponse
	body, err := sonic.Marshal(downlink.RetryScanRequest{Limit: limit})
	if err != nil {
		return zero, err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		cfg.InternalURL+"/internal/messages/retry/scan",
		bytes.NewReader(body),
	)
	if err != nil {
		return zero, err
	}
	request.Header.Set("Content-Type", "application/json")
	if err := applyInternalAuth(request, body, cfg); err != nil {
		return zero, err
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return zero, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return zero, err
	}
	if response.StatusCode != http.StatusOK {
		return zero, fmt.Errorf("retry scan status = %d, body = %s", response.StatusCode, string(responseBody))
	}
	if err := sonic.Unmarshal(responseBody, &zero); err != nil {
		return downlink.RetryScanResponse{}, err
	}
	return zero, nil
}

func validateRetryFairnessScan(scan downlink.RetryScanResponse, limit int) error {
	if scan.Code != "ok" || scan.Limit != limit || scan.Scanned != limit || scan.Sent != 0 ||
		scan.Queued != limit || scan.Failed != 0 || scan.SelectionMode != downlink.RetrySelectionModeFair ||
		scan.SelectedDevices != limit || scan.MaxPerDevice != 1 {
		return fmt.Errorf(
			"retry fairness scan = %+v, want limit/scanned/queued/devices=%d mode=%s max_per_device=1",
			scan,
			limit,
			downlink.RetrySelectionModeFair,
		)
	}
	return nil
}

func cleanupMessagesByPrefix(db *sql.DB, prefix string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = db.ExecContext(ctx, "DELETE FROM z_courier_downlink_messages WHERE message_id LIKE $1", prefix+"%")
}

type terminalEventCollector interface {
	Close()
	Wait(context.Context, string) (downlink.TerminalEvent, []byte, error)
}

type terminalNSQEventCollector struct {
	consumer *nsq.Consumer
	messages chan []byte
}

func newTerminalEventCollector(ctx context.Context, cfg config, runID int64) (terminalEventCollector, error) {
	switch cfg.TerminalPublisher {
	case terminalPublisherNSQ:
		return newTerminalNSQEventCollector(ctx, cfg.TerminalNSQDAddress, runID)
	case terminalPublisherHTTP:
		return newTerminalHTTPEventCollector(cfg)
	default:
		return nil, fmt.Errorf("unsupported terminal publisher %q", cfg.TerminalPublisher)
	}
}

func newTerminalNSQEventCollector(ctx context.Context, address string, runID int64) (*terminalNSQEventCollector, error) {
	consumer, err := nsq.NewConsumer(
		"downlink_terminal_events",
		fmt.Sprintf("z-courier-e2e-%d#ephemeral", runID),
		nsq.NewConfig(),
	)
	if err != nil {
		return nil, err
	}
	collector := &terminalNSQEventCollector{
		consumer: consumer,
		messages: make(chan []byte, 64),
	}
	consumer.AddHandler(nsq.HandlerFunc(func(message *nsq.Message) error {
		body := bytes.Clone(message.Body)
		select {
		case collector.messages <- body:
		default:
		}
		return nil
	}))
	if err := consumer.ConnectToNSQD(address); err != nil {
		consumer.Stop()
		return nil, err
	}
	if err := waitUntil(ctx, func() (bool, error) {
		return consumer.Stats().Connections > 0, nil
	}); err != nil {
		consumer.Stop()
		return nil, err
	}
	return collector, nil
}

func (c *terminalNSQEventCollector) Close() {
	if c == nil || c.consumer == nil {
		return
	}
	c.consumer.Stop()
	<-c.consumer.StopChan
}

func (c *terminalNSQEventCollector) Wait(ctx context.Context, messageID string) (downlink.TerminalEvent, []byte, error) {
	for {
		select {
		case body := <-c.messages:
			var event downlink.TerminalEvent
			if err := sonic.Unmarshal(body, &event); err != nil {
				return downlink.TerminalEvent{}, nil, err
			}
			if event.MessageID == messageID {
				return event, body, nil
			}
		case <-ctx.Done():
			return downlink.TerminalEvent{}, nil, ctx.Err()
		}
	}
}

type terminalHTTPEventCollector struct {
	server           *http.Server
	listener         net.Listener
	path             string
	verifier         *signing.Verifier
	failuresPerEvent int
	messages         chan []byte
	errors           chan error

	mu        sync.Mutex
	attempts  map[string]int
	successes map[string]int
}

func newTerminalHTTPEventCollector(cfg config) (*terminalHTTPEventCollector, error) {
	verifier, err := signing.NewVerifier(signing.VerifierConfig{
		Keys: map[string][]byte{cfg.TerminalWebhookKeyID: []byte(cfg.TerminalWebhookSecret)},
	})
	if err != nil {
		return nil, fmt.Errorf("create terminal webhook verifier: %w", err)
	}
	listener, err := net.Listen("tcp", cfg.TerminalWebhookAddress)
	if err != nil {
		return nil, fmt.Errorf("listen for terminal webhook: %w", err)
	}
	collector := &terminalHTTPEventCollector{
		listener:         listener,
		path:             cfg.TerminalWebhookPath,
		verifier:         verifier,
		failuresPerEvent: cfg.TerminalWebhookFailures,
		messages:         make(chan []byte, 64),
		errors:           make(chan error, 1),
		attempts:         make(map[string]int),
		successes:        make(map[string]int),
	}
	collector.server = &http.Server{
		Handler:           http.HandlerFunc(collector.handle),
		ReadHeaderTimeout: 2 * time.Second,
	}
	go func() {
		if err := collector.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case collector.errors <- err:
			default:
			}
		}
	}()
	return collector, nil
}

func (c *terminalHTTPEventCollector) handle(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.Path != c.path {
		http.NotFound(w, request)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, request.Body, maxTerminalWebhookBodySize))
	if err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := c.verifier.Verify(request, body); err != nil {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	var event downlink.TerminalEvent
	if err := sonic.Unmarshal(body, &event); err != nil || event.EventID == "" {
		http.Error(w, "invalid event", http.StatusBadRequest)
		return
	}

	c.mu.Lock()
	c.attempts[event.EventID]++
	attempt := c.attempts[event.EventID]
	if attempt <= c.failuresPerEvent {
		c.mu.Unlock()
		http.Error(w, "injected failure", http.StatusServiceUnavailable)
		return
	}
	c.successes[event.EventID]++
	c.mu.Unlock()

	select {
	case c.messages <- bytes.Clone(body):
	default:
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *terminalHTTPEventCollector) Close() {
	if c == nil || c.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.server.Shutdown(ctx)
}

func (c *terminalHTTPEventCollector) Wait(ctx context.Context, messageID string) (downlink.TerminalEvent, []byte, error) {
	for {
		select {
		case body := <-c.messages:
			var event downlink.TerminalEvent
			if err := sonic.Unmarshal(body, &event); err != nil {
				return downlink.TerminalEvent{}, nil, err
			}
			if event.MessageID == messageID {
				return event, body, nil
			}
		case err := <-c.errors:
			return downlink.TerminalEvent{}, nil, err
		case <-ctx.Done():
			return downlink.TerminalEvent{}, nil, ctx.Err()
		}
	}
}

func (c *terminalHTTPEventCollector) counts(eventID string) (int, int) {
	if c == nil {
		return 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attempts[eventID], c.successes[eventID]
}

func checkTerminalEvent(
	ctx context.Context,
	cfg config,
	db *sql.DB,
	collector terminalEventCollector,
	messageID string,
) error {
	fmt.Println("checking terminal failure event path")
	response, err := pushDownlinkTarget(
		ctx,
		cfg,
		cfg.DeviceID+"-terminal-offline",
		2999,
		messageID,
		[]byte("terminal-body-must-not-be-exported"),
		http.StatusAccepted,
	)
	if err != nil {
		return fmt.Errorf("push terminal test message: %w", err)
	}
	if err := validatePushOutcome(response, messageID, downlink.SubmissionStateCreated, downlink.MessageStatusPending); err != nil {
		return fmt.Errorf("validate terminal test message: %w", err)
	}
	if cfg.CheckRetryFairness {
		result, err := db.ExecContext(ctx, `
UPDATE z_courier_downlink_messages
SET created_at = TIMESTAMPTZ '1900-01-02 00:00:00+00',
    next_retry_at = NOW() - INTERVAL '1 second',
    claim_owner = '',
    claim_until = NULL,
    updated_at = NOW()
WHERE message_id = $1
  AND status = $2
`, messageID, string(downlink.MessageStatusPending))
		if err != nil {
			return fmt.Errorf("make terminal test message due: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count due terminal test message: %w", err)
		}
		if updated != 1 {
			return fmt.Errorf("due terminal test messages = %d, want 1", updated)
		}
		scan, err := triggerRetryScan(ctx, cfg, 1)
		if err != nil {
			return fmt.Errorf("trigger terminal test retry scan: %w", err)
		}
		if scan.Code != "ok" || scan.Scanned != 1 || scan.Failed != 1 ||
			scan.SelectionMode != downlink.RetrySelectionModeFair || scan.SelectedDevices != 1 || scan.MaxPerDevice != 1 {
			return fmt.Errorf("terminal retry scan = %+v, want one fair terminal failure", scan)
		}
	}
	if err := waitMessageStatus(ctx, db, messageID, string(downlink.MessageStatusFailed)); err != nil {
		return fmt.Errorf("wait terminal test message failed: %w", err)
	}

	event, raw, err := collector.Wait(ctx, messageID)
	if err != nil {
		return fmt.Errorf("wait terminal %s event: %w", cfg.TerminalPublisher, err)
	}
	if bytes.Contains(raw, []byte(`"body"`)) || bytes.Contains(raw, []byte("terminal-body-must-not-be-exported")) {
		return fmt.Errorf("terminal event leaked message body: %s", raw)
	}
	if event.TerminalStatus != downlink.MessageStatusFailed ||
		event.TerminalReason != downlink.TerminalReasonMaxAttempts ||
		event.PolicyName != cfg.ExpectTerminalPolicy ||
		event.MessageID != messageID {
		return fmt.Errorf("terminal event = %+v", event)
	}
	if event.EventID != messageID+":"+string(downlink.MessageStatusFailed) {
		return fmt.Errorf("terminal event id = %q, want stable message/status identity", event.EventID)
	}
	if err := waitUntil(ctx, func() (bool, error) {
		status, err := getMessageStatus(ctx, cfg, messageID)
		if err != nil {
			return false, err
		}
		return status.TerminalReason == downlink.TerminalReasonMaxAttempts &&
			status.TerminalPublishStatus == string(downlink.TerminalPublicationPublished), nil
	}); err != nil {
		return fmt.Errorf("wait terminal publication status: %w", err)
	}
	if httpCollector, ok := collector.(*terminalHTTPEventCollector); ok {
		timer := time.NewTimer(duplicateCheckDelay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
		attempts, successes := httpCollector.counts(event.EventID)
		if attempts != cfg.TerminalWebhookFailures+1 || successes != 1 {
			return fmt.Errorf(
				"terminal webhook deliveries = attempts:%d successes:%d, want %d/1",
				attempts,
				successes,
				cfg.TerminalWebhookFailures+1,
			)
		}
	}
	fmt.Printf("terminal event published: publisher=%s message_id=%s policy=%s\n", cfg.TerminalPublisher, messageID, event.PolicyName)
	return nil
}

func waitMessageStatus(ctx context.Context, db *sql.DB, messageID, wantStatus string) error {
	return waitUntil(ctx, func() (bool, error) {
		var status string
		err := db.QueryRowContext(ctx, `
SELECT status
FROM z_courier_downlink_messages
WHERE message_id = $1
`, messageID).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if status != wantStatus {
			return false, nil
		}
		return true, nil
	})
}

func waitMessagePolicy(ctx context.Context, db *sql.DB, messageID, wantPolicy string) error {
	return waitUntil(ctx, func() (bool, error) {
		var policyName string
		err := db.QueryRowContext(ctx, `
SELECT policy_name
FROM z_courier_downlink_messages
WHERE message_id = $1
`, messageID).Scan(&policyName)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return policyName == wantPolicy, nil
	})
}

func getMessageStatus(ctx context.Context, cfg config, messageID string) (downlink.MessageStatusResponse, error) {
	return getMessageStatusAt(ctx, cfg, cfg.InternalURL, messageID)
}

func getMessageStatusAt(ctx context.Context, cfg config, baseURL string, messageID string) (downlink.MessageStatusResponse, error) {
	var response downlink.MessageStatusResponse
	query := url.Values{"message_id": []string{messageID}}
	if err := getInternalJSON(ctx, cfg, baseURL, "/internal/message/status?"+query.Encode(), &response); err != nil {
		return downlink.MessageStatusResponse{}, err
	}
	return response, nil
}

func waitMessagePendingAttempt(ctx context.Context, db *sql.DB, messageID string) error {
	return waitUntil(ctx, func() (bool, error) {
		var status string
		var attempts int
		var lastError string
		err := db.QueryRowContext(ctx, `
SELECT status, attempts, last_error
FROM z_courier_downlink_messages
WHERE message_id = $1
`, messageID).Scan(&status, &attempts, &lastError)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if status != string(downlink.MessageStatusPending) {
			return false, nil
		}
		if attempts == 0 || strings.TrimSpace(lastError) == "" {
			return false, nil
		}
		return true, nil
	})
}

func checkMetrics(ctx context.Context, cfg config) error {
	var body []byte
	for _, url := range cfg.MetricsURLs {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		respBody, readErr := io.ReadAll(resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("metrics url %s status = %d", url, resp.StatusCode)
		}
		body = append(body, respBody...)
	}

	for _, name := range []string{
		"z_courier_downlink_push_total",
		"z_courier_downlink_ack_total",
		"z_courier_sessions_online",
		"z_courier_clients_online",
	} {
		if !bytes.Contains(body, []byte(name)) {
			return fmt.Errorf("metrics missing %s", name)
		}
	}
	if err := checkIdempotencyMetrics(string(body)); err != nil {
		return err
	}

	if cfg.RequireClusterMetrics {
		for _, name := range []string{
			"z_courier_cluster_registry_lookup_total",
			"z_courier_cluster_peer_push_total",
			"z_courier_cluster_peer_signature_total",
			"z_courier_downlink_retry_scan_total",
			"z_courier_downlink_retry_claim_duration_seconds",
		} {
			if !bytes.Contains(body, []byte(name)) {
				return fmt.Errorf("cluster metrics missing %s", name)
			}
		}
	}

	if cfg.CheckReconnectRetry {
		if err := checkReconnectRetryMetrics(string(body), cfg); err != nil {
			return err
		}
	}
	if cfg.CheckQueueCapacity {
		value, found, err := sumMetricSamples(string(body), "z_courier_downlink_queue_capacity_rejected_total", map[string]string{
			"scope": downlink.QueueCapacityScopeDevice,
		})
		if err != nil {
			return err
		}
		if !found || value < 1 {
			return fmt.Errorf("queue capacity rejection metric = %.4f, found = %t, want >= 1", value, found)
		}
	}
	if cfg.CheckRetryFairness {
		if err := checkRetryFairnessMetrics(string(body)); err != nil {
			return err
		}
	}
	if cfg.CheckAdminStorage {
		if err := checkAdminStorageMetrics(string(body)); err != nil {
			return err
		}
	}
	if cfg.CheckBulkRequeue {
		if err := checkBulkRequeueMetrics(string(body)); err != nil {
			return err
		}
	}

	return nil
}

type metricExpectation struct {
	Name   string
	Labels map[string]string
	Min    float64
}

func checkIdempotencyMetrics(metricsText string) error {
	expectations := []metricExpectation{
		{
			Name:   "z_courier_downlink_push_total",
			Labels: map[string]string{"msg_id": "2001", "result": "idempotent_replay"},
			Min:    2,
		},
		{
			Name:   "z_courier_downlink_push_total",
			Labels: map[string]string{"msg_id": "2001", "result": "message_id_conflict"},
			Min:    2,
		},
	}

	for _, expectation := range expectations {
		value, found, err := sumMetricSamples(metricsText, expectation.Name, expectation.Labels)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("idempotency metric %s%s not found", expectation.Name, formatMetricLabels(expectation.Labels))
		}
		if value < expectation.Min {
			return fmt.Errorf("idempotency metric %s%s = %.4f, want >= %.4f", expectation.Name, formatMetricLabels(expectation.Labels), value, expectation.Min)
		}
	}

	return nil
}

func checkReconnectRetryMetrics(metricsText string, cfg config) error {
	expectations := []metricExpectation{
		{
			Name:   "z_courier_downlink_push_total",
			Labels: map[string]string{"msg_id": "2001", "result": "queued"},
			Min:    1,
		},
		{
			Name:   "z_courier_cluster_registry_lookup_total",
			Labels: map[string]string{"result": "hit"},
			Min:    1,
		},
		{
			Name:   "z_courier_cluster_registry_unbind_total",
			Labels: map[string]string{"result": "success"},
			Min:    1,
		},
		{
			Name:   "z_courier_downlink_retry_scan_total",
			Labels: map[string]string{"result": "success"},
			Min:    1,
		},
		{
			Name: "z_courier_downlink_retry_claim_duration_seconds_count",
			Min:  1,
		},
	}
	if cfg.ExpectRouteNode != "" {
		expectations = append(expectations, metricExpectation{
			Name:   "z_courier_cluster_peer_push_total",
			Labels: map[string]string{"target_node": cfg.ExpectRouteNode, "result": "success"},
			Min:    1,
		})
		expectations = append(expectations, metricExpectation{
			Name:   "z_courier_cluster_peer_signature_total",
			Labels: map[string]string{"result": "success"},
			Min:    1,
		})
	}

	for _, expectation := range expectations {
		value, found, err := sumMetricSamples(metricsText, expectation.Name, expectation.Labels)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("reconnect retry metric %s%s not found", expectation.Name, formatMetricLabels(expectation.Labels))
		}
		if value < expectation.Min {
			return fmt.Errorf("reconnect retry metric %s%s = %.4f, want >= %.4f", expectation.Name, formatMetricLabels(expectation.Labels), value, expectation.Min)
		}
	}

	return nil
}

func checkRetryFairnessMetrics(metricsText string) error {
	expectations := []metricExpectation{
		{
			Name:   "z_courier_downlink_retry_selected_devices_sum",
			Labels: map[string]string{"mode": downlink.RetrySelectionModeFair},
			Min:    defaultRetryFairnessScanLimit,
		},
		{
			Name:   "z_courier_downlink_retry_max_per_device_sum",
			Labels: map[string]string{"mode": downlink.RetrySelectionModeFair},
			Min:    1,
		},
		{
			Name:   "z_courier_downlink_retry_claim_messages_total",
			Labels: map[string]string{"result": "success"},
			Min:    defaultRetryFairnessScanLimit,
		},
	}

	for _, expectation := range expectations {
		value, found, err := sumMetricSamples(metricsText, expectation.Name, expectation.Labels)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("retry fairness metric %s%s not found", expectation.Name, formatMetricLabels(expectation.Labels))
		}
		if value < expectation.Min {
			return fmt.Errorf(
				"retry fairness metric %s%s = %.4f, want >= %.4f",
				expectation.Name,
				formatMetricLabels(expectation.Labels),
				value,
				expectation.Min,
			)
		}
	}
	return nil
}

func checkAdminStorageMetrics(metricsText string) error {
	expectations := []metricExpectation{
		{
			Name:   "z_courier_admin_audit_write_total",
			Labels: map[string]string{"store": "postgres", "result": "success"},
			Min:    1,
		},
		{
			Name:   "z_courier_admin_session_store_operation_total",
			Labels: map[string]string{"store": "redis", "operation": "save", "result": "success"},
			Min:    1,
		},
		{
			Name:   "z_courier_admin_session_store_operation_total",
			Labels: map[string]string{"store": "redis", "operation": "lookup", "result": "hit"},
			Min:    1,
		},
		{
			Name: "z_courier_admin_audit_write_duration_seconds_count",
			Min:  1,
		},
		{
			Name: "z_courier_admin_session_store_operation_duration_seconds_count",
			Min:  1,
		},
	}

	for _, expectation := range expectations {
		value, found, err := sumMetricSamples(metricsText, expectation.Name, expectation.Labels)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("admin storage metric %s%s not found", expectation.Name, formatMetricLabels(expectation.Labels))
		}
		if value < expectation.Min {
			return fmt.Errorf("admin storage metric %s%s = %.4f, want >= %.4f", expectation.Name, formatMetricLabels(expectation.Labels), value, expectation.Min)
		}
	}

	return nil
}

func checkBulkRequeueMetrics(metricsText string) error {
	expectations := []metricExpectation{
		{
			Name:   "z_courier_downlink_bulk_requeue_total",
			Labels: map[string]string{"result": "partial_failure"},
			Min:    1,
		},
		{
			Name:   "z_courier_downlink_requeue_total",
			Labels: map[string]string{"result": "success"},
			Min:    1,
		},
		{
			Name:   "z_courier_downlink_requeue_total",
			Labels: map[string]string{"result": "queue_capacity_exceeded"},
			Min:    1,
		},
		{
			Name:   "z_courier_admin_action_total",
			Labels: map[string]string{"action": "downlink_message_bulk_requeue", "result": "partial_failure"},
			Min:    1,
		},
	}

	for _, expectation := range expectations {
		value, found, err := sumMetricSamples(metricsText, expectation.Name, expectation.Labels)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("bulk requeue metric %s%s not found", expectation.Name, formatMetricLabels(expectation.Labels))
		}
		if value < expectation.Min {
			return fmt.Errorf(
				"bulk requeue metric %s%s = %.4f, want >= %.4f",
				expectation.Name,
				formatMetricLabels(expectation.Labels),
				value,
				expectation.Min,
			)
		}
	}
	return nil
}

func sumMetricSamples(metricsText, name string, wantLabels map[string]string) (float64, bool, error) {
	var sum float64
	var found bool
	for _, line := range strings.Split(metricsText, "\n") {
		sampleName, labels, value, ok, err := parseMetricSample(line)
		if err != nil {
			return 0, false, err
		}
		if !ok || sampleName != name || !metricLabelsMatch(labels, wantLabels) {
			continue
		}

		found = true
		sum += value
	}

	return sum, found, nil
}

func parseMetricSample(line string) (string, map[string]string, float64, bool, error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", nil, 0, false, nil
	}

	var name string
	labels := map[string]string{}
	var valueRaw string
	if braceStart := strings.IndexByte(line, '{'); braceStart >= 0 {
		braceEnd := strings.IndexByte(line[braceStart:], '}')
		if braceEnd < 0 {
			return "", nil, 0, false, fmt.Errorf("invalid metric line: %s", line)
		}
		braceEnd += braceStart
		name = line[:braceStart]
		parsed, err := parseMetricLabels(line[braceStart+1 : braceEnd])
		if err != nil {
			return "", nil, 0, false, err
		}
		labels = parsed
		valueRaw = strings.TrimSpace(line[braceEnd+1:])
	} else {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return "", nil, 0, false, fmt.Errorf("invalid metric line: %s", line)
		}
		name = fields[0]
		valueRaw = fields[1]
	}

	fields := strings.Fields(valueRaw)
	if len(fields) == 0 {
		return "", nil, 0, false, fmt.Errorf("missing metric value: %s", line)
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return "", nil, 0, false, fmt.Errorf("parse metric value %q: %w", fields[0], err)
	}

	return name, labels, value, true, nil
}

func parseMetricLabels(raw string) (map[string]string, error) {
	labels := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return labels, nil
	}

	for _, part := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("invalid metric label %q", part)
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"`)
		labels[key] = value
	}

	return labels, nil
}

func metricLabelsMatch(got, want map[string]string) bool {
	for key, wantValue := range want {
		if got[key] != wantValue {
			return false
		}
	}

	return true
}

func formatMetricLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	parts := make([]string, 0, len(labels))
	for key, value := range labels {
		parts = append(parts, key+"="+strconv.Quote(value))
	}
	sort.Strings(parts)

	return "{" + strings.Join(parts, ",") + "}"
}

func waitUntil(ctx context.Context, check func() (bool, error)) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		ok, err := check()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type e2eClient struct {
	cfg           config
	client        ziface.IClient
	bindMessageID string

	mu       sync.RWMutex
	conn     ziface.IConnection
	ackCh    chan protocol.Ack
	downlink chan *protocol.Packet
}

func newE2EClient(cfg config) *e2eClient {
	return &e2eClient{
		cfg:           cfg,
		bindMessageID: fmt.Sprintf("e2e-%d-bind", time.Now().UnixNano()),
		ackCh:         make(chan protocol.Ack, 32),
		downlink:      make(chan *protocol.Packet, 32),
	}
}

func (c *e2eClient) Start(ctx context.Context) error {
	client := znet.NewClient(c.cfg.GatewayHost, c.cfg.GatewayPort)
	c.client = client

	client.AddRouter(protocol.MsgIDAck, &ackRouter{client: c})
	client.AddRouter(2001, &downlinkRouter{client: c})
	client.SetOnConnStart(func(conn ziface.IConnection) {
		c.mu.Lock()
		c.conn = conn
		c.mu.Unlock()
		if err := c.SendUpstream(c.bindMessageID, protocol.MsgIDBind, []byte("e2e-bind")); err != nil {
			fmt.Fprintf(os.Stderr, "send bind failed: %v\n", err)
			conn.Stop()
		}
	})

	client.Start()

	return waitUntil(ctx, func() (bool, error) {
		c.mu.RLock()
		connected := c.conn != nil
		c.mu.RUnlock()
		if connected {
			return true, nil
		}

		select {
		case err := <-client.GetErrChan():
			return false, err
		default:
			return false, nil
		}
	})
}

func (c *e2eClient) Stop() {
	if c.client != nil {
		c.client.Stop()
	}
	c.mu.Lock()
	c.conn = nil
	c.mu.Unlock()
}

func (c *e2eClient) SendUpstream(messageID string, msgID uint32, body []byte) error {
	packet := protocol.NewPacket(msgID, body)
	packet.ClientID = c.cfg.ClientID
	packet.DeviceID = c.cfg.DeviceID
	packet.Token = c.cfg.Token
	packet.MessageID = messageID
	packet.TraceID = messageID
	packet.Timestamp = time.Now().UnixMilli()
	packet.Flags = protocol.FlagAckRequired

	data, err := protocol.Encode(packet)
	if err != nil {
		return err
	}

	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	if conn == nil {
		return fmt.Errorf("client is not connected")
	}

	return conn.SendMsg(packet.MsgID, data)
}

func (c *e2eClient) WaitAck(ctx context.Context, messageID string, code protocol.AckCode) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ack := <-c.ackCh:
			if ack.MessageID != messageID {
				continue
			}
			if ack.Code != code {
				return fmt.Errorf("ack %s code = %q, want %q, reason = %s", messageID, ack.Code, code, ack.Reason)
			}
			return nil
		}
	}
}

func (c *e2eClient) WaitDownlink(ctx context.Context, messageID string) error {
	_, err := c.WaitDownlinkPacket(ctx, messageID)
	return err
}

func (c *e2eClient) WaitDownlinkPacket(ctx context.Context, messageID string) (*protocol.Packet, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case packet := <-c.downlink:
			if packet.MessageID == messageID {
				return packet, nil
			}
		}
	}
}

func (c *e2eClient) ExpectNoDownlink(ctx context.Context, messageID string, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case packet := <-c.downlink:
		return fmt.Errorf("received unexpected downlink message_id=%q while checking %q", packet.MessageID, messageID)
	case <-timer.C:
		return nil
	}
}

func (c *e2eClient) handleAck(data []byte) {
	packet, err := protocol.Decode(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "decode gateway ack failed: %v\n", err)
		return
	}

	var ack protocol.Ack
	if err := sonic.Unmarshal(packet.Body, &ack); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal gateway ack failed: %v\n", err)
		return
	}

	c.ackCh <- ack
}

func (c *e2eClient) handleDownlink(conn ziface.IConnection, data []byte) {
	packet, err := protocol.Decode(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "decode downlink failed: %v\n", err)
		return
	}

	if packet.Flags&protocol.FlagAckRequired != 0 && packet.MessageID != "" {
		if err := c.sendDownlinkAck(conn, packet); err != nil {
			fmt.Fprintf(os.Stderr, "send downlink ack failed: %v\n", err)
		}
	}

	c.downlink <- packet
}

func (c *e2eClient) sendDownlinkAck(conn ziface.IConnection, origin *protocol.Packet) error {
	body, err := sonic.Marshal(downlink.ClientAckRequest{
		MessageID: origin.MessageID,
		Code:      downlink.ClientAckCodeDelivered,
	})
	if err != nil {
		return err
	}

	packet := protocol.NewPacket(protocol.MsgIDDownlinkAck, body)
	packet.ClientID = origin.ClientID
	packet.DeviceID = origin.DeviceID
	packet.SessionID = origin.SessionID
	packet.MessageID = origin.MessageID
	packet.TraceID = origin.TraceID
	packet.Token = c.cfg.Token
	packet.Timestamp = time.Now().UnixMilli()

	data, err := protocol.Encode(packet)
	if err != nil {
		return err
	}

	return conn.SendMsg(packet.MsgID, data)
}

type ackRouter struct {
	znet.BaseRouter
	client *e2eClient
}

func (r ackRouter) Handle(request ziface.IRequest) {
	r.client.handleAck(request.GetData())
}

type downlinkRouter struct {
	znet.BaseRouter
	client *e2eClient
}

func (r downlinkRouter) Handle(request ziface.IRequest) {
	r.client.handleDownlink(request.GetConnection(), request.GetData())
}
