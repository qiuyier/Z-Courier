package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/qiuyier/Z-Courier/internal/auth"
	"github.com/qiuyier/Z-Courier/internal/cluster"
	"github.com/qiuyier/Z-Courier/internal/downlink"
)

const (
	adminCheckStatusOK       = "ok"
	adminCheckStatusDegraded = "degraded"
	adminCheckStatusFailed   = "failed"
	adminCheckStatusSkipped  = "skipped"

	defaultAdminCheckTimeout = 2 * time.Second
	maxAdminCheckTimeout     = 30 * time.Second
)

type adminCheckResponse struct {
	Code        string             `json:"code"`
	GatewayNode string             `json:"gateway_node"`
	Status      string             `json:"status"`
	Timeout     string             `json:"timeout"`
	Checks      []adminCheckResult `json:"checks"`
}

type adminCheckResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Target  string `json:"target,omitempty"`
	Latency string `json:"latency,omitempty"`
	Error   string `json:"error,omitempty"`
}

type adminCheckHandler struct {
	config     adminHandlerConfig
	service    *downlink.Service
	registry   cluster.OnlineRegistry
	httpClient *http.Client
}

type dependencyPinger interface {
	Ping(context.Context) error
}

func newAdminCheckHandler(config Config, service *downlink.Service, registry cluster.OnlineRegistry) http.Handler {
	return &adminCheckHandler{
		config:     adminConfig(config, nil, registry),
		service:    service,
		registry:   registry,
		httpClient: adminCheckHTTPClient(),
	}
}

func (h *adminCheckHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminJSON(w, http.StatusMethodNotAllowed, adminCheckResponse{Code: "method_not_allowed", GatewayNode: h.config.gatewayNode})
		return
	}
	if !h.config.authorized(r) {
		writeAdminJSON(w, http.StatusUnauthorized, adminCheckResponse{Code: "unauthorized", GatewayNode: h.config.gatewayNode})
		return
	}

	timeout, err := adminCheckTimeout(r)
	if err != nil {
		writeAdminJSON(w, http.StatusBadRequest, adminCheckResponse{
			Code:        "bad_request",
			GatewayNode: h.config.gatewayNode,
			Status:      adminCheckStatusFailed,
			Checks: []adminCheckResult{{
				Name:   "request",
				Status: adminCheckStatusFailed,
				Error:  err.Error(),
			}},
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	checks := h.runChecks(ctx)
	writeAdminJSON(w, http.StatusOK, adminCheckResponse{
		Code:        "ok",
		GatewayNode: h.config.gatewayNode,
		Status:      aggregateAdminCheckStatus(checks),
		Timeout:     durationString(timeout),
		Checks:      checks,
	})
}

func (h *adminCheckHandler) runChecks(ctx context.Context) []adminCheckResult {
	config := h.config.config
	checks := []adminCheckResult{
		h.checkAuthVerifier(ctx),
		h.checkDownlinkStore(ctx),
		h.checkClusterRegistry(ctx),
	}

	for _, route := range config.UpstreamRoutes {
		switch {
		case route.HTTP != nil:
			checks = append(checks, h.checkHTTPUpstream(ctx, route))
		case route.NSQ != nil:
			checks = append(checks, h.checkNSQUpstream(ctx, route))
		}
	}
	return checks
}

func (h *adminCheckHandler) checkAuthVerifier(ctx context.Context) adminCheckResult {
	verifier := h.config.config.Verifier
	name := "auth_verifier"
	target := auth.ProviderName(verifier)
	if verifier == nil {
		return adminCheckResult{Name: name, Status: adminCheckStatusFailed, Target: target, Error: "auth verifier is not configured"}
	}

	pinger, ok := verifier.(dependencyPinger)
	if !ok {
		return adminCheckResult{Name: name, Status: adminCheckStatusSkipped, Target: target, Error: "auth provider does not expose an active health probe"}
	}

	return runAdminDependencyCheck(ctx, name, target, "auth provider ping failed", func(ctx context.Context) error {
		err := pinger.Ping(ctx)
		if errors.Is(err, auth.ErrHealthCheckUnsupported) {
			return err
		}
		return err
	})
}

func (h *adminCheckHandler) checkDownlinkStore(ctx context.Context) adminCheckResult {
	name := "downlink_store"
	if h.service == nil || !h.service.HasStore() {
		return adminCheckResult{Name: name, Status: adminCheckStatusSkipped, Target: h.config.config.DownlinkStorage.Type, Error: "downlink store is not configured"}
	}

	store := h.service.Store()
	pinger, ok := store.(dependencyPinger)
	if !ok {
		return adminCheckResult{Name: name, Status: adminCheckStatusSkipped, Target: h.config.config.DownlinkStorage.Type, Error: "downlink store does not expose an active health probe"}
	}
	return runAdminDependencyCheck(ctx, name, h.config.config.DownlinkStorage.Type, "downlink store ping failed", pinger.Ping)
}

func (h *adminCheckHandler) checkClusterRegistry(ctx context.Context) adminCheckResult {
	name := "cluster_registry"
	if !h.config.config.Cluster.Enabled {
		return adminCheckResult{Name: name, Status: adminCheckStatusSkipped, Target: h.config.config.Cluster.Registry.Type, Error: "cluster is disabled"}
	}
	if h.registry == nil {
		return adminCheckResult{Name: name, Status: adminCheckStatusFailed, Target: h.config.config.Cluster.Registry.Type, Error: "cluster registry is not attached"}
	}

	pinger, ok := h.registry.(dependencyPinger)
	if !ok {
		return adminCheckResult{Name: name, Status: adminCheckStatusSkipped, Target: h.config.config.Cluster.Registry.Type, Error: "cluster registry does not expose an active health probe"}
	}
	return runAdminDependencyCheck(ctx, name, h.config.config.Cluster.Registry.Type, "cluster registry ping failed", pinger.Ping)
}

func (h *adminCheckHandler) checkHTTPUpstream(ctx context.Context, route UpstreamRouteConfig) adminCheckResult {
	name := "http_upstream:" + route.Name
	target := sanitizeAdminURL(route.HTTP.URL)
	startedAt := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, route.HTTP.URL, nil)
	if err != nil {
		return adminCheckResult{Name: name, Status: adminCheckStatusFailed, Target: target, Latency: durationString(time.Since(startedAt)), Error: "invalid http upstream URL"}
	}
	if route.HTTP.Token != "" {
		req.Header.Set("X-ZCourier-Internal-Token", route.HTTP.Token)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return adminCheckResult{Name: name, Status: adminCheckStatusFailed, Target: target, Latency: durationString(time.Since(startedAt)), Error: "http upstream request failed"}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))

	status := adminCheckStatusOK
	errorText := ""
	if resp.StatusCode >= http.StatusInternalServerError {
		status = adminCheckStatusDegraded
		errorText = "http upstream returned " + resp.Status
	}
	return adminCheckResult{Name: name, Status: status, Target: target, Latency: durationString(time.Since(startedAt)), Error: errorText}
}

func (h *adminCheckHandler) checkNSQUpstream(ctx context.Context, route UpstreamRouteConfig) adminCheckResult {
	name := "nsq_upstream:" + route.Name
	addresses := adminNSQAddresses(route.NSQ)
	target := strings.Join(addresses, ",")
	if len(addresses) == 0 {
		return adminCheckResult{Name: name, Status: adminCheckStatusFailed, Error: "nsq upstream has no nsqd address"}
	}

	startedAt := time.Now()
	var failed []string
	for _, address := range addresses {
		dialer := net.Dialer{}
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			failed = append(failed, address)
			continue
		}
		_ = conn.Close()
	}

	status := adminCheckStatusOK
	errorText := ""
	if len(failed) == len(addresses) {
		status = adminCheckStatusFailed
		errorText = "all nsq addresses failed"
	} else if len(failed) > 0 {
		status = adminCheckStatusDegraded
		errorText = "some nsq addresses failed: " + strings.Join(failed, ",")
	}

	return adminCheckResult{Name: name, Status: status, Target: target, Latency: durationString(time.Since(startedAt)), Error: errorText}
}

func runAdminDependencyCheck(ctx context.Context, name, target, failureMessage string, check func(context.Context) error) adminCheckResult {
	startedAt := time.Now()
	err := check(ctx)
	result := adminCheckResult{
		Name:    name,
		Status:  adminCheckStatusOK,
		Target:  target,
		Latency: durationString(time.Since(startedAt)),
	}
	if errors.Is(err, auth.ErrHealthCheckUnsupported) {
		result.Status = adminCheckStatusSkipped
		result.Error = "dependency does not expose an active health probe"
		return result
	}
	if err != nil {
		result.Status = adminCheckStatusFailed
		result.Error = adminCheckErrorMessage(err, failureMessage)
	}
	return result
}

func aggregateAdminCheckStatus(checks []adminCheckResult) string {
	status := adminCheckStatusOK
	for _, check := range checks {
		switch check.Status {
		case adminCheckStatusFailed:
			return adminCheckStatusFailed
		case adminCheckStatusDegraded:
			status = adminCheckStatusDegraded
		}
	}
	return status
}

func adminCheckTimeout(r *http.Request) (time.Duration, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("timeout"))
	if raw == "" {
		return defaultAdminCheckTimeout, nil
	}

	timeout, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("timeout must be a duration such as 2s")
	}
	if timeout <= 0 {
		return 0, fmt.Errorf("timeout must be greater than 0")
	}
	if timeout > maxAdminCheckTimeout {
		return 0, fmt.Errorf("timeout must be less than or equal to %s", maxAdminCheckTimeout)
	}
	return timeout, nil
}

func adminNSQAddresses(config *NSQUpstreamConfig) []string {
	if config == nil {
		return nil
	}
	addresses := append([]string(nil), config.Addresses...)
	if len(addresses) == 0 && config.Address != "" {
		addresses = append(addresses, config.Address)
	}

	out := make([]string, 0, len(addresses))
	seen := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		out = append(out, address)
	}
	return out
}

func adminCheckHTTPClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func adminCheckErrorMessage(err error, fallback string) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "dependency check timed out"
	case errors.Is(err, context.Canceled):
		return "dependency check was canceled"
	default:
		return fallback
	}
}
