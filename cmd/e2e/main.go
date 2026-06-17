package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aceld/zinx/ziface"
	"github.com/aceld/zinx/znet"
	"github.com/bytedance/sonic"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/protocol"
)

const (
	defaultGatewayHost  = "127.0.0.1"
	defaultGatewayPort  = 9899
	defaultInternalURL  = "http://127.0.0.1:18082"
	defaultPostgresDSN  = "postgres://zcourier:zcourier@127.0.0.1:15432/zcourier?sslmode=disable"
	downlinkPushTimeout = 10 * time.Second
)

type config struct {
	GatewayHost            string
	GatewayPort            int
	InternalURL            string
	MetricsURLs            []string
	InternalToken          string
	PostgresDSN            string
	ClientID               string
	DeviceID               string
	Token                  string
	Timeout                time.Duration
	OnlinePushDelay        time.Duration
	RequireClusterMetrics  bool
	ExpectRouteNode        string
	ExpectRouteInternalURL string
	ExpectSessionURL       string
	ExpectSessionNode      string
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
	flag.StringVar(&cfg.InternalToken, "internal-token", "dev-internal-token", "gateway internal HTTP token")
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
	flag.Parse()

	cfg.InternalURL = strings.TrimRight(cfg.InternalURL, "/")
	cfg.ExpectSessionURL = strings.TrimRight(cfg.ExpectSessionURL, "/")
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

func run(ctx context.Context, cfg config) error {
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
	upstreamMessageID := fmt.Sprintf("e2e-%d-upstream", runID)

	fmt.Println("checking offline queue path")
	if err := pushDownlink(ctx, cfg, offlineMessageID, []byte("offline-before-client"), http.StatusAccepted); err != nil {
		return fmt.Errorf("push offline downlink: %w", err)
	}
	if err := waitMessageStatus(ctx, db, offlineMessageID, string(downlink.MessageStatusPending)); err != nil {
		return fmt.Errorf("wait offline message pending: %w", err)
	}

	client := newE2EClient(cfg)
	if err := client.Start(ctx); err != nil {
		return err
	}
	defer client.Stop()

	if err := client.WaitAck(ctx, client.bindMessageID, protocol.AckAccepted); err != nil {
		return fmt.Errorf("wait bind ack: %w", err)
	}
	fmt.Println("client bound")

	if err := checkDebugCluster(ctx, cfg); err != nil {
		return err
	}

	if err := client.WaitDownlink(ctx, offlineMessageID); err != nil {
		return fmt.Errorf("wait offline downlink flush: %w", err)
	}
	if err := waitMessageStatus(ctx, db, offlineMessageID, string(downlink.MessageStatusDelivered)); err != nil {
		return fmt.Errorf("wait offline message delivered status: %w", err)
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

	fmt.Println("checking online push path")
	if err := pushDownlink(ctx, cfg, onlineMessageID, []byte("online-after-client"), http.StatusOK); err != nil {
		return fmt.Errorf("push online downlink: %w", err)
	}
	if err := client.WaitDownlink(ctx, onlineMessageID); err != nil {
		return fmt.Errorf("wait online downlink: %w", err)
	}
	if err := waitMessageStatus(ctx, db, onlineMessageID, string(downlink.MessageStatusDelivered)); err != nil {
		return fmt.Errorf("wait online message delivered status: %w", err)
	}
	fmt.Println("online message delivered")

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
	if err := getInternalJSON(ctx, cfg.InternalURL, "/internal/debug/route?"+query.Encode(), cfg.InternalToken, &resp); err != nil {
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

func fetchDebugSessions(ctx context.Context, cfg config) (debugSessionsResponse, error) {
	query := url.Values{}
	query.Set("client_id", cfg.ClientID)
	query.Set("limit", "10")

	var resp debugSessionsResponse
	if err := getInternalJSON(ctx, cfg.ExpectSessionURL, "/internal/debug/sessions?"+query.Encode(), cfg.InternalToken, &resp); err != nil {
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

func getInternalJSON(ctx context.Context, baseURL, path, token string, target any) error {
	requestCtx, cancel := context.WithTimeout(ctx, downlinkPushTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set(downlink.InternalTokenHeader, token)

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

func pushDownlink(ctx context.Context, cfg config, messageID string, body []byte, wantStatus int) error {
	requestCtx, cancel := context.WithTimeout(ctx, downlinkPushTimeout)
	defer cancel()

	reqBody, err := sonic.Marshal(downlink.PushRequest{
		ClientID:    cfg.ClientID,
		DeviceID:    cfg.DeviceID,
		MsgID:       2001,
		MessageID:   messageID,
		TraceID:     messageID,
		AckRequired: true,
		Body:        body,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, cfg.InternalURL+"/internal/push", bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(downlink.InternalTokenHeader, cfg.InternalToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		return fmt.Errorf("push %s status = %d, want %d, body = %s", messageID, resp.StatusCode, wantStatus, string(respBody))
	}

	var pushResp downlink.PushResponse
	if err := sonic.Unmarshal(respBody, &pushResp); err != nil {
		return err
	}
	if pushResp.Code != "ok" {
		return fmt.Errorf("push %s code = %q, reason = %s", messageID, pushResp.Code, pushResp.Reason)
	}

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

	if cfg.RequireClusterMetrics {
		for _, name := range []string{
			"z_courier_cluster_registry_lookup_total",
			"z_courier_cluster_peer_push_total",
			"z_courier_downlink_retry_scan_total",
			"z_courier_downlink_retry_claim_duration_seconds",
		} {
			if !bytes.Contains(body, []byte(name)) {
				return fmt.Errorf("cluster metrics missing %s", name)
			}
		}
	}

	return nil
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
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case packet := <-c.downlink:
			if packet.MessageID == messageID {
				return nil
			}
		}
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
