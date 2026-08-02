package server

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/auth"
	"github.com/qiuyier/Z-Courier/internal/cluster"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/pipeline"
	"github.com/qiuyier/Z-Courier/internal/resilience"
)

type adminHandlerConfig struct {
	gatewayNode    string
	internalToken  string
	config         Config
	health         *gatewayHealth
	registry       cluster.OnlineRegistry
	clusterEnabled bool
}

type adminOverviewResponse struct {
	Code         string                   `json:"code"`
	GatewayNode  string                   `json:"gateway_node"`
	Readiness    adminReadiness           `json:"readiness"`
	Sessions     adminSessionSummary      `json:"sessions"`
	Cluster      adminClusterSummary      `json:"cluster"`
	InternalHTTP adminInternalHTTPSummary `json:"internal_http"`
	AdminConsole adminConsoleSummary      `json:"admin_console"`
	Downlink     adminDownlinkSummary     `json:"downlink"`
	Upstream     adminUpstreamSummary     `json:"upstream"`
	Dependencies []adminDependency        `json:"dependencies"`
}

type adminReadiness struct {
	Ready         bool      `json:"ready"`
	Status        string    `json:"status"`
	DrainingSince time.Time `json:"draining_since,omitempty"`
	DrainDuration string    `json:"drain_duration,omitempty"`
}

type adminSessionSummary struct {
	Online        int `json:"online"`
	UniqueClients int `json:"unique_clients"`
}

type adminClusterSummary struct {
	Enabled              bool   `json:"enabled"`
	InternalAddr         string `json:"internal_addr,omitempty"`
	RegistryType         string `json:"registry_type,omitempty"`
	RegistryTTL          string `json:"registry_ttl,omitempty"`
	RouteRefreshInterval string `json:"route_refresh_interval,omitempty"`
	PeerAuthMode         string `json:"peer_auth_mode,omitempty"`
}

type adminInternalHTTPSummary struct {
	Enabled            bool   `json:"enabled"`
	Addr               string `json:"addr,omitempty"`
	AuthMode           string `json:"auth_mode,omitempty"`
	MaxRequestBodySize int64  `json:"max_request_body_size,omitempty"`
	MaxInFlight        int    `json:"max_in_flight,omitempty"`
}

type adminConsoleSummary struct {
	Enabled    bool                       `json:"enabled"`
	Path       string                     `json:"path,omitempty"`
	AssetsDir  string                     `json:"assets_dir,omitempty"`
	Monitoring adminMonitoringSummary     `json:"monitoring"`
	Session    adminConsoleSessionSummary `json:"session"`
	Audit      adminConsoleAuditSummary   `json:"audit"`
}

type adminMonitoringSummary struct {
	PrometheusURL string `json:"prometheus_url,omitempty"`
	GrafanaURL    string `json:"grafana_url,omitempty"`
	DashboardURL  string `json:"dashboard_url,omitempty"`
}

type adminConsoleSessionSummary struct {
	Enabled         bool     `json:"enabled"`
	TTL             string   `json:"ttl,omitempty"`
	CookieName      string   `json:"cookie_name,omitempty"`
	CookieSecure    bool     `json:"cookie_secure"`
	CookieSameSite  string   `json:"cookie_same_site,omitempty"`
	Role            string   `json:"role,omitempty"`
	Permissions     []string `json:"permissions,omitempty"`
	StorageType     string   `json:"storage_type,omitempty"`
	RedisConfigured bool     `json:"redis_configured"`
}

type adminConsoleAuditSummary struct {
	StorageType        string `json:"storage_type"`
	Capacity           int    `json:"capacity,omitempty"`
	StoreConfigured    bool   `json:"store_configured"`
	PostgresConfigured bool   `json:"postgres_configured"`
}

type adminDownlinkSummary struct {
	StorageType                      string   `json:"storage_type"`
	StoreConfigured                  bool     `json:"store_configured"`
	PolicyCount                      int      `json:"policy_count"`
	PolicyNames                      []string `json:"policy_names"`
	MaxPendingGlobal                 int      `json:"max_pending_global"`
	MaxPendingPerDevice              int      `json:"max_pending_per_device"`
	TerminalPublisher                string   `json:"terminal_publisher"`
	TerminalTopic                    string   `json:"terminal_topic,omitempty"`
	TerminalRetryInterval            string   `json:"terminal_retry_interval,omitempty"`
	TerminalRetryDelay               string   `json:"terminal_retry_delay,omitempty"`
	RetryInterval                    string   `json:"retry_interval,omitempty"`
	RetryDelay                       string   `json:"retry_delay,omitempty"`
	RetryJitter                      string   `json:"retry_jitter,omitempty"`
	AckTimeout                       string   `json:"ack_timeout,omitempty"`
	RetryLease                       string   `json:"retry_lease,omitempty"`
	MaxAttempts                      int      `json:"max_attempts,omitempty"`
	ScanLimit                        int      `json:"scan_limit,omitempty"`
	BindFlushLimit                   int      `json:"bind_flush_limit,omitempty"`
	RetryFairnessEnabled             bool     `json:"retry_fairness_enabled"`
	RetryFairnessCandidateMultiplier int      `json:"retry_fairness_candidate_multiplier"`
}

type adminUpstreamSummary struct {
	Routes int `json:"routes"`
}

type adminDiagnosticsResponse struct {
	Code          string                        `json:"code"`
	GatewayNode   string                        `json:"gateway_node"`
	Runtime       adminRuntimeDiagnostics       `json:"runtime"`
	Readiness     adminReadiness                `json:"readiness"`
	Sessions      adminSessionSummary           `json:"sessions"`
	Auth          adminAuthDiagnostics          `json:"auth"`
	InternalHTTP  adminInternalHTTPSummary      `json:"internal_http"`
	AdminConsole  adminConsoleSummary           `json:"admin_console"`
	Cluster       adminClusterSummary           `json:"cluster"`
	Downlink      adminDownlinkSummary          `json:"downlink"`
	Upstream      adminUpstreamDiagnostics      `json:"upstream"`
	Capacity      adminCapacityDiagnostics      `json:"capacity"`
	TrafficPolicy adminTrafficPolicyDiagnostics `json:"traffic_policy"`
	RouteReload   adminRouteReloadDiagnostics   `json:"route_reload"`
	Dependencies  []adminDependency             `json:"dependencies"`
	Warnings      []adminDiagnosticWarning      `json:"warnings,omitempty"`
}

type adminRouteReloadDiagnostics struct {
	Enabled            bool                      `json:"enabled"`
	Closed             bool                      `json:"closed"`
	DrainTimeoutMS     int64                     `json:"drain_timeout_ms,omitempty"`
	OperationsInFlight int                       `json:"operations_in_flight"`
	LastStartedAt      *time.Time                `json:"last_started_at,omitempty"`
	LastSuccessAt      *time.Time                `json:"last_success_at,omitempty"`
	LastFailureAt      *time.Time                `json:"last_failure_at,omitempty"`
	LastAttempt        *adminRouteReloadAttempt  `json:"last_attempt,omitempty"`
	RecentAttempts     []adminRouteReloadAttempt `json:"recent_attempts,omitempty"`
	Active             *adminRouteGeneration     `json:"active,omitempty"`
	Retiring           *adminRouteGeneration     `json:"retiring,omitempty"`
}

type adminRuntimeDiagnostics struct {
	Started   bool      `json:"started"`
	StartedAt time.Time `json:"started_at,omitempty"`
	Uptime    string    `json:"uptime,omitempty"`
}

type adminAuthDiagnostics struct {
	Provider       string `json:"provider"`
	CacheWrapped   bool   `json:"cache_wrapped,omitempty"`
	VerifierLoaded bool   `json:"verifier_loaded"`
}

type adminUpstreamDiagnostics struct {
	Routes             int                         `json:"routes"`
	HTTPRoutes         int                         `json:"http_routes"`
	NSQRoutes          int                         `json:"nsq_routes"`
	RoutesWithCapacity int                         `json:"routes_with_capacity_limit"`
	HTTPRouteStates    []adminUpstreamRouteRuntime `json:"http_route_states,omitempty"`
}

type adminUpstreamRouteRuntime struct {
	Name                string                         `json:"name"`
	TargetType          string                         `json:"target_type"`
	Status              string                         `json:"status"`
	ConsecutiveFailures int                            `json:"consecutive_failures,omitempty"`
	LastReason          string                         `json:"last_reason,omitempty"`
	LastFailureAt       time.Time                      `json:"last_failure_at,omitempty"`
	LastSuccessAt       time.Time                      `json:"last_success_at,omitempty"`
	UpdatedAt           time.Time                      `json:"updated_at,omitempty"`
	Discovery           *adminUpstreamDiscoveryRuntime `json:"discovery,omitempty"`
}

type adminUpstreamDiscoveryRuntime struct {
	Type                     string     `json:"type"`
	ResolvedEndpoints        int        `json:"resolved_endpoints"`
	UnhealthyEndpoints       int        `json:"unhealthy_endpoints"`
	LastRefreshResult        string     `json:"last_refresh_result,omitempty"`
	LastRefreshDuration      string     `json:"last_refresh_duration,omitempty"`
	LastRefreshAt            *time.Time `json:"last_refresh_at,omitempty"`
	LastSelectionResult      string     `json:"last_selection_result,omitempty"`
	LastSelectionAt          *time.Time `json:"last_selection_at,omitempty"`
	CooldownSkippedTotal     uint64     `json:"cooldown_skipped_total"`
	LastCooldownSkippedAt    *time.Time `json:"last_cooldown_skipped_at,omitempty"`
	LastEndpointFailureClass string     `json:"last_endpoint_failure_class,omitempty"`
	LastEndpointFailureAt    *time.Time `json:"last_endpoint_failure_at,omitempty"`
	LastForwardResult        string     `json:"last_forward_result,omitempty"`
	LastForwardAttempts      *int       `json:"last_forward_attempts,omitempty"`
	LastForwardAt            *time.Time `json:"last_forward_at,omitempty"`
	LastFailoverDecision     string     `json:"last_failover_decision,omitempty"`
	LastFailoverAt           *time.Time `json:"last_failover_at,omitempty"`
}

type adminCapacityDiagnostics struct {
	InternalHTTPMaxInFlight int    `json:"internal_http_max_in_flight,omitempty"`
	UpstreamLimitedRoutes   int    `json:"upstream_limited_routes,omitempty"`
	RateLimitEnabled        bool   `json:"rate_limit_enabled"`
	RateLimitMaxRequests    int    `json:"rate_limit_max_requests,omitempty"`
	RateLimitWindow         string `json:"rate_limit_window,omitempty"`
}

type adminTrafficPolicyDiagnostics struct {
	Enabled           bool                                  `json:"enabled"`
	Mode              string                                `json:"mode,omitempty"`
	StoreStatus       string                                `json:"store_status"`
	PolicyCount       int                                   `json:"policy_count"`
	PolicyNames       []string                              `json:"policy_names"`
	DefaultPolicy     string                                `json:"default_policy,omitempty"`
	KeyScope          string                                `json:"key_scope,omitempty"`
	IdleTTL           string                                `json:"idle_ttl,omitempty"`
	FailureMode       string                                `json:"failure_mode,omitempty"`
	NoMatchTotal      uint64                                `json:"no_match_total"`
	Decisions         adminTrafficPolicyDecisionTotals      `json:"decisions"`
	LastResult        string                                `json:"last_result,omitempty"`
	LastState         string                                `json:"last_state,omitempty"`
	LastDecisionAt    *time.Time                            `json:"last_decision_at,omitempty"`
	LastSuccessAt     *time.Time                            `json:"last_success_at,omitempty"`
	LastUnavailableAt *time.Time                            `json:"last_unavailable_at,omitempty"`
	Local             *adminTrafficPolicyLocalDiagnostics   `json:"local,omitempty"`
	Policies          []adminTrafficPolicyPolicyDiagnostics `json:"policies,omitempty"`
}

type adminTrafficPolicyDecisionTotals struct {
	Allowed              uint64 `json:"allowed"`
	RateLimited          uint64 `json:"rate_limited"`
	Overloaded           uint64 `json:"overloaded"`
	AdmissionUnavailable uint64 `json:"admission_unavailable"`
}

type adminTrafficPolicyLocalDiagnostics struct {
	LiveKeys    int     `json:"live_keys"`
	MaxKeys     int     `json:"max_keys"`
	Utilization float64 `json:"utilization"`
}

type adminTrafficPolicyPolicyDiagnostics struct {
	Name           string                           `json:"name"`
	Priority       int                              `json:"priority"`
	KeyScope       string                           `json:"key_scope"`
	Capacity       int                              `json:"capacity"`
	RefillTokens   int                              `json:"refill_tokens"`
	RefillInterval string                           `json:"refill_interval"`
	SelectionTotal uint64                           `json:"selection_total"`
	Decisions      adminTrafficPolicyDecisionTotals `json:"decisions"`
	LastResult     string                           `json:"last_result,omitempty"`
	LastState      string                           `json:"last_state,omitempty"`
	LastDecisionAt *time.Time                       `json:"last_decision_at,omitempty"`
}

type adminDiagnosticWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type adminDependency struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type adminRoutesResponse struct {
	Code        string       `json:"code"`
	GatewayNode string       `json:"gateway_node"`
	Total       int          `json:"total"`
	Routes      []adminRoute `json:"routes"`
}

type adminRoute struct {
	Name        string          `json:"name"`
	MsgIDMin    uint32          `json:"msg_id_min"`
	MsgIDMax    uint32          `json:"msg_id_max,omitempty"`
	TargetType  string          `json:"target_type"`
	MaxInFlight int             `json:"max_in_flight,omitempty"`
	HTTP        *adminHTTPRoute `json:"http,omitempty"`
	NSQ         *adminNSQRoute  `json:"nsq,omitempty"`
}

type adminHTTPRoute struct {
	URL     string `json:"url"`
	Timeout string `json:"timeout,omitempty"`
}

type adminNSQRoute struct {
	Addresses     []string `json:"addresses,omitempty"`
	Topic         string   `json:"topic"`
	DialTimeout   string   `json:"dial_timeout,omitempty"`
	ReadTimeout   string   `json:"read_timeout,omitempty"`
	WriteTimeout  string   `json:"write_timeout,omitempty"`
	PublishMode   string   `json:"publish_mode,omitempty"`
	RetryAttempts int      `json:"retry_attempts,omitempty"`
}

func newAdminOverviewHandler(config Config, health *gatewayHealth, registry cluster.OnlineRegistry) http.Handler {
	return &adminOverviewHandler{config: adminConfig(config, health, registry)}
}

func newAdminRoutesHandler(config Config) http.Handler {
	return &adminRoutesHandler{
		config:  adminConfig(config, nil, nil),
		control: config.routeControl,
	}
}

func newAdminDiagnosticsHandler(config Config, health *gatewayHealth, registry cluster.OnlineRegistry, runtime *gatewayRuntime, downlinkHasStore bool) http.Handler {
	handlerConfig := adminConfig(config, health, registry)
	if runtime == nil {
		runtime = newGatewayRuntime()
	}
	return &adminDiagnosticsHandler{
		config:           handlerConfig,
		runtime:          runtime,
		downlinkHasStore: downlinkHasStore,
	}
}

func adminConfig(config Config, health *gatewayHealth, registry cluster.OnlineRegistry) adminHandlerConfig {
	return adminHandlerConfig{
		gatewayNode:    config.GatewayNode,
		internalToken:  config.InternalToken,
		config:         config,
		health:         health,
		registry:       registry,
		clusterEnabled: config.Cluster.Enabled && registry != nil,
	}
}

type adminOverviewHandler struct {
	config adminHandlerConfig
}

func (h *adminOverviewHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminJSON(w, http.StatusMethodNotAllowed, adminOverviewResponse{Code: "method_not_allowed", GatewayNode: h.config.gatewayNode})
		return
	}
	if !h.config.authorized(r) {
		writeAdminJSON(w, http.StatusUnauthorized, adminOverviewResponse{Code: "unauthorized", GatewayNode: h.config.gatewayNode})
		return
	}

	config := h.config.config
	now := time.Now()
	readiness := adminReadinessFromHealth(h.config.health, now)

	sessions := adminSessionSummary{}
	if config.Sessions != nil {
		sessions.Online = config.Sessions.Len()
		sessions.UniqueClients = config.Sessions.UniqueClientLen()
	}

	resp := adminOverviewResponse{
		Code:         "ok",
		GatewayNode:  h.config.gatewayNode,
		Readiness:    readiness,
		Sessions:     sessions,
		Cluster:      adminClusterFromConfig(config),
		InternalHTTP: adminInternalHTTPFromConfig(config),
		AdminConsole: adminConsoleFromConfig(config),
		Downlink:     adminDownlinkFromConfig(config),
		Upstream:     adminUpstreamSummary{Routes: len(config.UpstreamRoutes)},
		Dependencies: adminDependencies(config, h.config.registry, h.config.clusterEnabled),
	}
	writeAdminJSON(w, http.StatusOK, resp)
}

type adminRoutesHandler struct {
	config  adminHandlerConfig
	control *routeControl
}

type adminDiagnosticsHandler struct {
	config           adminHandlerConfig
	runtime          *gatewayRuntime
	downlinkHasStore bool
}

func (h *adminRoutesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminJSON(w, http.StatusMethodNotAllowed, adminRoutesResponse{Code: "method_not_allowed", GatewayNode: h.config.gatewayNode})
		return
	}
	if !h.config.authorized(r) {
		writeAdminJSON(w, http.StatusUnauthorized, adminRoutesResponse{Code: "unauthorized", GatewayNode: h.config.gatewayNode})
		return
	}

	activeRoutes := h.config.config.UpstreamRoutes
	if routes := h.control.ActiveRoutes(); routes != nil {
		activeRoutes = routes
	}
	routes := make([]adminRoute, 0, len(activeRoutes))
	for _, route := range activeRoutes {
		routes = append(routes, adminRouteFromConfig(route))
	}
	writeAdminJSON(w, http.StatusOK, adminRoutesResponse{
		Code:        "ok",
		GatewayNode: h.config.gatewayNode,
		Total:       len(routes),
		Routes:      routes,
	})
}

func (h *adminDiagnosticsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminJSON(w, http.StatusMethodNotAllowed, adminDiagnosticsResponse{Code: "method_not_allowed", GatewayNode: h.config.gatewayNode})
		return
	}
	if !h.config.authorized(r) {
		writeAdminJSON(w, http.StatusUnauthorized, adminDiagnosticsResponse{Code: "unauthorized", GatewayNode: h.config.gatewayNode})
		return
	}

	config := h.config.config
	now := time.Now()
	trafficPolicy := adminTrafficPolicyFromConfig(config)
	routeReload := adminRouteReloadDiagnosticsFromControl(config.routeControl)
	warnings := adminDiagnosticWarnings(config, h.config.registry, h.downlinkHasStore, trafficPolicy)
	warnings = append(warnings, adminRouteReloadDiagnosticWarnings(routeReload)...)
	resp := adminDiagnosticsResponse{
		Code:          "ok",
		GatewayNode:   h.config.gatewayNode,
		Runtime:       adminRuntimeFromRuntime(h.runtime, now),
		Readiness:     adminReadinessFromHealth(h.config.health, now),
		Sessions:      adminSessionsFromConfig(config),
		Auth:          adminAuthFromConfig(config),
		InternalHTTP:  adminInternalHTTPFromConfig(config),
		AdminConsole:  adminConsoleFromConfig(config),
		Cluster:       adminClusterFromConfig(config),
		Downlink:      adminDownlinkFromConfig(config),
		Upstream:      adminUpstreamDiagnosticsFromConfig(config),
		Capacity:      adminCapacityFromConfig(config),
		TrafficPolicy: trafficPolicy,
		RouteReload:   routeReload,
		Dependencies:  adminDiagnosticDependencies(config, h.config.registry, h.config.clusterEnabled, h.downlinkHasStore, trafficPolicy),
		Warnings:      warnings,
	}
	writeAdminJSON(w, http.StatusOK, resp)
}

func adminRouteReloadDiagnosticsFromControl(control *routeControl) adminRouteReloadDiagnostics {
	snapshot := control.Status()
	status := routeControlStatusResponse("", snapshot)
	return adminRouteReloadDiagnostics{
		Enabled:            snapshot.Enabled,
		Closed:             snapshot.Closed,
		DrainTimeoutMS:     status.DrainTimeoutMS,
		OperationsInFlight: status.OperationsInFlight,
		LastStartedAt:      status.LastStartedAt,
		LastSuccessAt:      status.LastSuccessAt,
		LastFailureAt:      status.LastFailureAt,
		LastAttempt:        status.LastAttempt,
		RecentAttempts:     status.RecentAttempts,
		Active:             status.Active,
		Retiring:           status.Retiring,
	}
}

func adminRouteReloadDiagnosticWarnings(diagnostics adminRouteReloadDiagnostics) []adminDiagnosticWarning {
	warnings := make([]adminDiagnosticWarning, 0, 2)
	if diagnostics.LastAttempt != nil && !routeReloadResultSuccessful(diagnostics.LastAttempt.Result) {
		warnings = append(warnings, adminDiagnosticWarning{
			Code: "route_reload_last_attempt_failed",
			Message: "the latest route reload operation failed during " +
				diagnostics.LastAttempt.Stage + ": " + diagnostics.LastAttempt.Reason,
		})
	}
	if diagnostics.Retiring != nil && diagnostics.Retiring.Slow {
		warnings = append(warnings, adminDiagnosticWarning{
			Code:    "route_generation_slow_retirement",
			Message: "an upstream route generation is still retiring after its configured drain timeout",
		})
	}
	return warnings
}

func routeReloadResultSuccessful(result string) bool {
	return result == "validated" || result == "reloaded"
}

func (c adminHandlerConfig) authorized(r *http.Request) bool {
	return c.internalToken == "" || r.Header.Get(downlink.InternalTokenHeader) == c.internalToken
}

func adminRuntimeFromRuntime(runtime *gatewayRuntime, now time.Time) adminRuntimeDiagnostics {
	startedAt, started := runtime.StartedAt()
	out := adminRuntimeDiagnostics{Started: started}
	if started {
		out.StartedAt = startedAt.UTC()
		out.Uptime = durationString(runtime.Uptime(now))
	}
	return out
}

func adminReadinessFromHealth(health *gatewayHealth, now time.Time) adminReadiness {
	readiness := adminReadiness{Ready: health.Ready(), Status: "ready"}
	if !readiness.Ready {
		readiness.Status = "draining"
		if since, ok := health.DrainingSince(); ok {
			readiness.DrainingSince = since.UTC()
			readiness.DrainDuration = durationString(health.DrainDuration(now))
		}
	}
	return readiness
}

func adminSessionsFromConfig(config Config) adminSessionSummary {
	sessions := adminSessionSummary{}
	if config.Sessions != nil {
		sessions.Online = config.Sessions.Len()
		sessions.UniqueClients = config.Sessions.UniqueClientLen()
	}
	return sessions
}

func adminAuthFromConfig(config Config) adminAuthDiagnostics {
	return adminAuthDiagnostics{
		Provider:       auth.ProviderName(config.Verifier),
		VerifierLoaded: config.Verifier != nil,
	}
}

func adminClusterFromConfig(config Config) adminClusterSummary {
	return adminClusterSummary{
		Enabled:              config.Cluster.Enabled,
		InternalAddr:         config.Cluster.InternalAddr,
		RegistryType:         config.Cluster.Registry.Type,
		RegistryTTL:          durationString(config.Cluster.Registry.TTL),
		RouteRefreshInterval: durationString(config.Cluster.RouteRefreshInterval),
		PeerAuthMode:         config.Cluster.Peer.Auth.Mode,
	}
}

func adminInternalHTTPFromConfig(config Config) adminInternalHTTPSummary {
	return adminInternalHTTPSummary{
		Enabled:            !config.DisableInternalHTTP && config.InternalHTTPAddr != "",
		Addr:               config.InternalHTTPAddr,
		AuthMode:           config.InternalHTTPAuth.Mode,
		MaxRequestBodySize: config.InternalMaxRequestBodySize,
		MaxInFlight:        config.InternalPushMaxInFlight,
	}
}

func adminConsoleFromConfig(config Config) adminConsoleSummary {
	return adminConsoleSummary{
		Enabled:   config.AdminConsole.Enabled,
		Path:      config.AdminConsole.Path,
		AssetsDir: config.AdminConsole.AssetsDir,
		Monitoring: adminMonitoringSummary{
			PrometheusURL: config.AdminConsole.Monitoring.PrometheusURL,
			GrafanaURL:    config.AdminConsole.Monitoring.GrafanaURL,
			DashboardURL:  config.AdminConsole.Monitoring.DashboardURL,
		},
		Session: adminConsoleSessionSummary{
			Enabled:         config.AdminConsole.Session.Enabled,
			TTL:             durationString(config.AdminConsole.Session.TTL),
			CookieName:      config.AdminConsole.Session.CookieName,
			CookieSecure:    config.AdminConsole.Session.CookieSecure,
			CookieSameSite:  config.AdminConsole.Session.CookieSameSite,
			Role:            normalizeAdminRole(config.AdminConsole.Session.Role),
			Permissions:     adminPermissionsForRole(config.AdminConsole.Session.Role),
			StorageType:     config.AdminConsole.Session.Store.Type,
			RedisConfigured: config.AdminConsole.Session.Store.Redis.Addr != "",
		},
		Audit: adminConsoleAuditSummary{
			StorageType:        config.AdminAuditStorage.Type,
			Capacity:           config.AdminAuditStorage.Capacity,
			StoreConfigured:    config.AdminAudit != nil,
			PostgresConfigured: config.AdminAuditStorage.Postgres.DSN != "",
		},
	}
}

func adminDownlinkFromConfig(config Config) adminDownlinkSummary {
	policyNames := make([]string, 1, len(config.DownlinkPolicies)+1)
	policyNames[0] = downlink.DefaultDeliveryPolicyName
	for _, rule := range config.DownlinkPolicies {
		policyNames = append(policyNames, rule.Policy.Name)
	}
	return adminDownlinkSummary{
		StorageType:                      config.DownlinkStorage.Type,
		StoreConfigured:                  config.DownlinkStore != nil,
		PolicyCount:                      len(policyNames),
		PolicyNames:                      policyNames,
		MaxPendingGlobal:                 config.DownlinkCapacity.MaxPendingGlobal,
		MaxPendingPerDevice:              config.DownlinkCapacity.MaxPendingPerDevice,
		TerminalPublisher:                config.DownlinkTerminal.PublisherType,
		TerminalTopic:                    config.DownlinkTerminal.NSQ.Topic,
		TerminalRetryInterval:            durationString(config.DownlinkTerminal.RetryInterval),
		TerminalRetryDelay:               durationString(config.DownlinkTerminal.RetryDelay),
		RetryInterval:                    durationString(config.DownlinkDelivery.RetryInterval),
		RetryDelay:                       durationString(config.DownlinkDelivery.RetryDelay),
		RetryJitter:                      durationString(config.DownlinkDelivery.RetryJitter),
		AckTimeout:                       durationString(config.DownlinkDelivery.AckTimeout),
		RetryLease:                       durationString(config.DownlinkDelivery.RetryLease),
		MaxAttempts:                      config.DownlinkDelivery.MaxAttempts,
		ScanLimit:                        config.DownlinkDelivery.ScanLimit,
		BindFlushLimit:                   config.DownlinkDelivery.BindFlushLimit,
		RetryFairnessEnabled:             config.DownlinkDelivery.RetryFairness.Enabled,
		RetryFairnessCandidateMultiplier: config.DownlinkDelivery.RetryFairness.CandidateMultiplier,
	}
}

func adminDependencies(config Config, registry cluster.OnlineRegistry, clusterEnabled bool) []adminDependency {
	dependencies := []adminDependency{{
		Name:   "downlink_store",
		Status: "configured",
	}}
	if config.DownlinkStore == nil {
		dependencies[0].Status = "not_configured"
		dependencies[0].Reason = "downlink storage is memory-only or disabled"
	}

	clusterStatus := adminDependency{Name: "cluster_registry", Status: "disabled"}
	if config.Cluster.Enabled {
		clusterStatus.Status = "configured"
		if !clusterEnabled || registry == nil {
			clusterStatus.Status = "not_configured"
			clusterStatus.Reason = "cluster is enabled but no registry is attached"
		}
	}
	return append(dependencies, clusterStatus)
}

func adminDiagnosticDependencies(
	config Config,
	registry cluster.OnlineRegistry,
	clusterEnabled bool,
	downlinkHasStore bool,
	trafficPolicy adminTrafficPolicyDiagnostics,
) []adminDependency {
	dependencies := []adminDependency{
		{Name: "auth_verifier", Status: "configured", Reason: auth.ProviderName(config.Verifier)},
		{Name: "downlink_store", Status: "configured", Reason: config.DownlinkStorage.Type},
		{Name: "admin_audit_store", Status: "configured", Reason: adminStorageType(config.AdminAuditStorage.Type)},
		{Name: "admin_session_store", Status: "disabled", Reason: adminStorageType(config.AdminConsole.Session.Store.Type)},
		{Name: "cluster_registry", Status: "disabled"},
		{Name: "http_upstream", Status: "not_configured"},
		{Name: "nsq_upstream", Status: "not_configured"},
	}
	if config.Verifier == nil {
		dependencies[0].Status = "not_configured"
		dependencies[0].Reason = "auth verifier is nil"
	}
	if !downlinkHasStore {
		dependencies[1].Status = "not_configured"
		dependencies[1].Reason = "downlink storage is disabled"
	}
	if config.AdminAudit == nil {
		dependencies[2].Status = "not_configured"
		dependencies[2].Reason = "admin audit store is not configured"
	} else if adminStorageType(config.AdminAuditStorage.Type) == "postgres" && strings.TrimSpace(config.AdminAuditStorage.Postgres.DSN) == "" {
		dependencies[2].Status = "not_configured"
		dependencies[2].Reason = "admin audit postgres dsn is not configured"
	}
	if config.AdminConsole.Session.Enabled {
		dependencies[3].Status = "configured"
		dependencies[3].Reason = adminStorageType(config.AdminConsole.Session.Store.Type)
		if config.AdminSessions == nil {
			dependencies[3].Status = "not_configured"
			dependencies[3].Reason = "admin session store is not configured"
		} else if adminStorageType(config.AdminConsole.Session.Store.Type) == "redis" && strings.TrimSpace(config.AdminConsole.Session.Store.Redis.Addr) == "" {
			dependencies[3].Status = "not_configured"
			dependencies[3].Reason = "admin session redis addr is not configured"
		}
	}
	if config.Cluster.Enabled {
		dependencies[4].Status = "configured"
		dependencies[4].Reason = config.Cluster.Registry.Type
		if !clusterEnabled || registry == nil {
			dependencies[4].Status = "not_configured"
			dependencies[4].Reason = "cluster is enabled but no registry is attached"
		}
	}
	upstream := adminUpstreamDiagnosticsFromConfig(config)
	if upstream.HTTPRoutes > 0 {
		dependencies[5].Status = "configured"
		dependencies[5].Reason = "configured routes: " + intString(upstream.HTTPRoutes)
		unavailable, degraded := adminUpstreamStateCounts(upstream.HTTPRouteStates)
		switch {
		case unavailable > 0:
			dependencies[5].Status = "unavailable"
			dependencies[5].Reason = "unavailable routes: " + intString(unavailable) + "/" + intString(upstream.HTTPRoutes)
		case degraded > 0:
			dependencies[5].Status = "degraded"
			dependencies[5].Reason = "degraded routes: " + intString(degraded) + "/" + intString(upstream.HTTPRoutes)
		}
	}
	if upstream.NSQRoutes > 0 {
		dependencies[6].Status = "configured"
		dependencies[6].Reason = "configured routes: " + intString(upstream.NSQRoutes)
	}
	return append(dependencies, adminTrafficPolicyDependency(trafficPolicy))
}

func adminTrafficPolicyDependency(trafficPolicy adminTrafficPolicyDiagnostics) adminDependency {
	dependency := adminDependency{
		Name:   "traffic_policy_store",
		Status: trafficPolicy.StoreStatus,
		Reason: trafficPolicy.Mode,
	}
	switch trafficPolicy.StoreStatus {
	case "disabled":
		dependency.Reason = ""
	case "not_configured":
		dependency.Reason = "traffic policy runtime is not attached"
	case "degraded":
		if trafficPolicy.Local != nil {
			dependency.Reason = "local keys: " +
				intString(trafficPolicy.Local.LiveKeys) +
				"/" +
				intString(trafficPolicy.Local.MaxKeys)
		}
	case "unavailable":
		dependency.Reason = "last admission decision: admission_unavailable"
	}
	return dependency
}

func adminStorageType(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return "memory"
	}
	return raw
}

func adminUpstreamDiagnosticsFromConfig(config Config) adminUpstreamDiagnostics {
	out := adminUpstreamDiagnostics{Routes: len(config.UpstreamRoutes)}
	for _, route := range config.UpstreamRoutes {
		switch {
		case route.HTTP != nil:
			out.HTTPRoutes++
			if snapshot, ok := config.UpstreamRuntime.snapshot(route.Name); ok {
				out.HTTPRouteStates = append(out.HTTPRouteStates, adminUpstreamRouteRuntimeFromSnapshot(snapshot))
			}
		case route.NSQ != nil:
			out.NSQRoutes++
		}
		if route.MaxInFlight > 0 {
			out.RoutesWithCapacity++
		}
	}
	return out
}

func adminUpstreamRouteRuntimeFromSnapshot(snapshot upstreamRouteRuntimeSnapshot) adminUpstreamRouteRuntime {
	dependency := snapshot.Snapshot
	out := adminUpstreamRouteRuntime{
		Name:                snapshot.RouteName,
		TargetType:          snapshot.TargetType,
		Status:              dependency.Status,
		ConsecutiveFailures: dependency.ConsecutiveFailures,
		LastReason:          dependency.LastReason,
		LastFailureAt:       dependency.LastFailureAt.UTC(),
		LastSuccessAt:       dependency.LastSuccessAt.UTC(),
		UpdatedAt:           dependency.UpdatedAt.UTC(),
	}
	if snapshot.Discovery != nil {
		discovery := snapshot.Discovery
		lastRefreshDuration := ""
		if discovery.LastRefreshResult != "" {
			lastRefreshDuration = discovery.LastRefreshDuration.String()
		}
		out.Discovery = &adminUpstreamDiscoveryRuntime{
			Type:                     discovery.Type,
			ResolvedEndpoints:        discovery.ResolvedEndpoints,
			UnhealthyEndpoints:       discovery.UnhealthyEndpoints,
			LastRefreshResult:        discovery.LastRefreshResult,
			LastRefreshDuration:      lastRefreshDuration,
			LastRefreshAt:            optionalAdminTime(discovery.LastRefreshAt),
			LastSelectionResult:      discovery.LastSelectionResult,
			LastSelectionAt:          optionalAdminTime(discovery.LastSelectionAt),
			CooldownSkippedTotal:     discovery.CooldownSkippedTotal,
			LastCooldownSkippedAt:    optionalAdminTime(discovery.LastCooldownSkippedAt),
			LastEndpointFailureClass: discovery.LastEndpointFailureClass,
			LastEndpointFailureAt:    optionalAdminTime(discovery.LastEndpointFailureAt),
			LastForwardResult:        discovery.LastForwardResult,
			LastForwardAt:            optionalAdminTime(discovery.LastForwardAt),
			LastFailoverDecision:     discovery.LastFailoverDecision,
			LastFailoverAt:           optionalAdminTime(discovery.LastFailoverDecisionAt),
		}
		if discovery.LastForwardResult != "" {
			attempts := discovery.LastForwardAttempts
			out.Discovery.LastForwardAttempts = &attempts
		}
	}
	return out
}

func optionalAdminTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	utc := value.UTC()
	return &utc
}

func adminUpstreamStateCounts(states []adminUpstreamRouteRuntime) (unavailable int, degraded int) {
	for _, state := range states {
		switch state.Status {
		case resilience.DependencyStatusUnavailable:
			unavailable++
		case resilience.DependencyStatusDegraded:
			degraded++
		}
	}
	return unavailable, degraded
}

func adminCapacityFromConfig(config Config) adminCapacityDiagnostics {
	return adminCapacityDiagnostics{
		InternalHTTPMaxInFlight: config.InternalPushMaxInFlight,
		UpstreamLimitedRoutes:   adminUpstreamDiagnosticsFromConfig(config).RoutesWithCapacity,
		RateLimitEnabled:        config.Pipeline.RateLimit.Enabled,
		RateLimitMaxRequests:    config.Pipeline.RateLimit.MaxRequests,
		RateLimitWindow:         durationString(config.Pipeline.RateLimit.Window),
	}
}

func adminTrafficPolicyFromConfig(config Config) adminTrafficPolicyDiagnostics {
	trafficConfig := config.Pipeline.TrafficPolicies
	out := adminTrafficPolicyDiagnostics{
		Enabled:     trafficConfig.Enabled,
		StoreStatus: "disabled",
		PolicyCount: len(trafficConfig.Policies),
		PolicyNames: make([]string, 0, len(trafficConfig.Policies)),
	}
	if !trafficConfig.Enabled {
		return out
	}

	out.Mode = normalizedAdminTrafficPolicyMode(trafficConfig.Mode)
	out.StoreStatus = "not_configured"
	out.DefaultPolicy = trafficConfig.DefaultPolicy
	out.IdleTTL = durationString(trafficConfig.IdleTTL)
	if out.Mode == pipeline.TrafficPolicyModeRedis {
		out.FailureMode = trafficConfig.Redis.FailureMode
	}

	var snapshot pipeline.TrafficPolicyRuntimeSnapshot
	runtimeAttached := config.TrafficPolicyRuntime != nil
	if runtimeAttached {
		snapshot = config.TrafficPolicyRuntime.Snapshot()
		out.StoreStatus = "configured"
		out.NoMatchTotal = snapshot.NoMatchTotal
		out.Decisions = adminTrafficPolicyDecisionTotalsFromSnapshot(snapshot.Decisions)
		out.LastResult = string(snapshot.LastResult)
		out.LastState = snapshot.LastState
		out.LastDecisionAt = optionalAdminTime(snapshot.LastDecisionAt)
		out.LastSuccessAt = optionalAdminTime(snapshot.LastSuccessAt)
		out.LastUnavailableAt = optionalAdminTime(snapshot.LastUnavailableAt)
		if snapshot.LastResult == pipeline.QuotaDecisionAdmissionUnavailable {
			out.StoreStatus = "unavailable"
		}
	}

	if out.Mode == pipeline.TrafficPolicyModeLocal {
		liveKeys := snapshot.LocalKeys
		maxKeys := trafficConfig.MaxKeys
		if runtimeAttached && snapshot.LocalKeyLimit > 0 {
			maxKeys = snapshot.LocalKeyLimit
		}
		out.Local = &adminTrafficPolicyLocalDiagnostics{
			LiveKeys:    liveKeys,
			MaxKeys:     maxKeys,
			Utilization: trafficPolicyKeyUtilization(liveKeys, maxKeys),
		}
		if out.StoreStatus == "configured" && out.Local.Utilization >= 0.8 {
			out.StoreStatus = "degraded"
		}
	}

	runtimePolicies := make(map[string]pipeline.TrafficPolicyRuntimePolicySnapshot, len(snapshot.Policies))
	for _, policy := range snapshot.Policies {
		runtimePolicies[policy.Name] = policy
	}
	out.Policies = make([]adminTrafficPolicyPolicyDiagnostics, 0, len(trafficConfig.Policies))
	for _, configured := range trafficConfig.Policies {
		if out.KeyScope == "" {
			out.KeyScope = configured.Key
		} else if out.KeyScope != configured.Key {
			out.KeyScope = "mixed"
		}
		out.PolicyNames = append(out.PolicyNames, configured.Name)
		runtime := runtimePolicies[configured.Name]
		out.Policies = append(out.Policies, adminTrafficPolicyPolicyDiagnostics{
			Name:           configured.Name,
			Priority:       configured.Priority,
			KeyScope:       configured.Key,
			Capacity:       configured.TokenBucket.Capacity,
			RefillTokens:   configured.TokenBucket.RefillTokens,
			RefillInterval: durationString(configured.TokenBucket.RefillInterval),
			SelectionTotal: runtime.SelectionTotal,
			Decisions:      adminTrafficPolicyDecisionTotalsFromSnapshot(runtime.Decisions),
			LastResult:     string(runtime.LastResult),
			LastState:      runtime.LastState,
			LastDecisionAt: optionalAdminTime(runtime.LastDecisionAt),
		})
	}
	return out
}

func normalizedAdminTrafficPolicyMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", pipeline.TrafficPolicyModeLocal:
		return pipeline.TrafficPolicyModeLocal
	case pipeline.TrafficPolicyModeRedis:
		return pipeline.TrafficPolicyModeRedis
	default:
		return "unknown"
	}
}

func adminTrafficPolicyDecisionTotalsFromSnapshot(
	totals pipeline.TrafficPolicyDecisionTotals,
) adminTrafficPolicyDecisionTotals {
	return adminTrafficPolicyDecisionTotals{
		Allowed:              totals.Allowed,
		RateLimited:          totals.RateLimited,
		Overloaded:           totals.Overloaded,
		AdmissionUnavailable: totals.AdmissionUnavailable,
	}
}

func trafficPolicyKeyUtilization(keys, limit int) float64 {
	if keys <= 0 || limit <= 0 {
		return 0
	}
	return min(float64(keys)/float64(limit), 1)
}

func adminDiagnosticWarnings(
	config Config,
	registry cluster.OnlineRegistry,
	downlinkHasStore bool,
	trafficPolicy adminTrafficPolicyDiagnostics,
) []adminDiagnosticWarning {
	warnings := make([]adminDiagnosticWarning, 0)
	if auth.ProviderName(config.Verifier) == auth.ProviderStatic {
		warnings = append(warnings, adminDiagnosticWarning{
			Code:    "static_auth_provider",
			Message: "static auth provider is suitable for development; production should use HTTP or JWT/JWKS auth",
		})
	}
	if config.Cluster.Enabled {
		if registry == nil {
			warnings = append(warnings, adminDiagnosticWarning{
				Code:    "cluster_registry_not_attached",
				Message: "cluster is enabled but this process has no online route registry attached",
			})
		}
		if config.Cluster.Registry.Type != "redis" {
			warnings = append(warnings, adminDiagnosticWarning{
				Code:    "non_redis_cluster_registry",
				Message: "multi-node deployments should use the redis cluster registry",
			})
		}
	}
	if !downlinkHasStore || config.DownlinkStorage.Type == "memory" {
		warnings = append(warnings, adminDiagnosticWarning{
			Code:    "non_durable_downlink_store",
			Message: "downlink storage is not durable across gateway restarts",
		})
	}
	if adminStorageType(config.AdminAuditStorage.Type) == "memory" {
		warnings = append(warnings, adminDiagnosticWarning{
			Code:    "non_durable_admin_audit_store",
			Message: "admin audit storage is memory-only; use postgres for production audit retention",
		})
	}
	if config.AdminConsole.Session.Enabled && adminStorageType(config.AdminConsole.Session.Store.Type) == "memory" {
		warnings = append(warnings, adminDiagnosticWarning{
			Code:    "node_local_admin_session_store",
			Message: "admin console sessions are node-local; use redis when console traffic can move across gateway nodes",
		})
	}
	if adminInternalHTTPFromConfig(config).AuthMode == InternalHTTPAuthModeToken && adminBindsWildcard(config.InternalHTTPAddr) {
		warnings = append(warnings, adminDiagnosticWarning{
			Code:    "token_auth_on_wildcard_internal_http",
			Message: "internal HTTP listens on all interfaces with token auth; prefer HMAC or a private network boundary",
		})
	}
	if trafficPolicy.Enabled && trafficPolicy.Mode == pipeline.TrafficPolicyModeLocal && config.Cluster.Enabled {
		warnings = append(warnings, adminDiagnosticWarning{
			Code:    "node_local_traffic_policy_store",
			Message: "traffic policy quotas are process-local; use redis mode when limits must hold across gateway nodes",
		})
	}
	if trafficPolicy.StoreStatus == "not_configured" {
		warnings = append(warnings, adminDiagnosticWarning{
			Code:    "traffic_policy_runtime_not_attached",
			Message: "traffic policies are enabled but their runtime state is not attached to diagnostics",
		})
	}
	if trafficPolicy.StoreStatus == "unavailable" {
		warnings = append(warnings, adminDiagnosticWarning{
			Code:    "traffic_policy_store_unavailable",
			Message: "the latest traffic policy admission could not obtain a safe quota-store decision",
		})
	}
	if trafficPolicy.Local != nil && trafficPolicy.Local.MaxKeys > 0 && trafficPolicy.Local.Utilization >= 0.8 {
		warnings = append(warnings, adminDiagnosticWarning{
			Code: "traffic_policy_local_key_capacity_high",
			Message: "traffic policy local key usage is " +
				intString(trafficPolicy.Local.LiveKeys) +
				"/" +
				intString(trafficPolicy.Local.MaxKeys),
		})
	}
	return warnings
}

func adminRouteFromConfig(route UpstreamRouteConfig) adminRoute {
	resp := adminRoute{
		Name:        route.Name,
		MsgIDMin:    route.MsgIDMin,
		MsgIDMax:    route.MsgIDMax,
		MaxInFlight: route.MaxInFlight,
	}
	switch {
	case route.HTTP != nil:
		resp.TargetType = "http"
		resp.HTTP = &adminHTTPRoute{
			URL:     sanitizeAdminURL(primaryHTTPUpstreamURL(route.HTTP)),
			Timeout: durationString(route.HTTP.Timeout),
		}
	case route.NSQ != nil:
		resp.TargetType = "nsq"
		addresses := append([]string(nil), route.NSQ.Addresses...)
		if len(addresses) == 0 && route.NSQ.Address != "" {
			addresses = append(addresses, route.NSQ.Address)
		}
		resp.NSQ = &adminNSQRoute{
			Addresses:     addresses,
			Topic:         route.NSQ.Topic,
			DialTimeout:   durationString(route.NSQ.DialTimeout),
			ReadTimeout:   durationString(route.NSQ.ReadTimeout),
			WriteTimeout:  durationString(route.NSQ.WriteTimeout),
			PublishMode:   route.NSQ.PublishMode,
			RetryAttempts: route.NSQ.RetryAttempts,
		}
	default:
		resp.TargetType = "unknown"
	}
	return resp
}

func primaryHTTPUpstreamURL(config *HTTPUpstreamConfig) string {
	urls := httpUpstreamURLs(config)
	if len(urls) == 0 {
		return ""
	}
	return urls[0]
}

func httpUpstreamURLs(config *HTTPUpstreamConfig) []string {
	if config == nil {
		return nil
	}
	if config.URL != "" {
		return []string{config.URL}
	}
	if config.Discovery.Type == "static" && len(config.Discovery.Endpoints) > 0 {
		return append([]string(nil), config.Discovery.Endpoints...)
	}
	if config.Discovery.Type == "dns" {
		pathValue := config.Path
		if pathValue == "" {
			pathValue = "/"
		}
		host := net.JoinHostPort(config.Discovery.Hostname, strconv.Itoa(config.Discovery.Port))
		return []string{config.Discovery.Scheme + "://" + host + pathValue}
	}
	return nil
}

func sanitizeAdminURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return sanitizeAmbiguousAdminURL(raw)
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func sanitizeAmbiguousAdminURL(raw string) string {
	value := strings.TrimSpace(raw)
	if index := strings.IndexAny(value, "?#"); index >= 0 {
		value = value[:index]
	}
	if index := strings.LastIndex(value, "@"); index >= 0 {
		value = value[index+1:]
	}
	return value
}

func durationString(duration time.Duration) string {
	if duration <= 0 {
		return ""
	}
	return duration.String()
}

func intString(value int) string {
	return strconv.Itoa(value)
}

func adminBindsWildcard(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return strings.HasPrefix(addr, "0.0.0.0:") || strings.HasPrefix(addr, "[::]:") || strings.HasPrefix(addr, ":")
	}
	return host == "" || host == "0.0.0.0" || host == "::" || host == "[::]"
}

func writeAdminJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, err := sonic.Marshal(value)
	if err != nil {
		_, _ = w.Write([]byte(`{"code":"marshal_error"}`))
		return
	}
	_, _ = w.Write(body)
}
