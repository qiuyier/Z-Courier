package server

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/cluster"
	"github.com/qiuyier/Z-Courier/internal/downlink"
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
	Downlink     adminDownlinkSummary     `json:"downlink"`
	Upstream     adminUpstreamSummary     `json:"upstream"`
	Dependencies []adminDependency        `json:"dependencies"`
}

type adminReadiness struct {
	Ready  bool   `json:"ready"`
	Status string `json:"status"`
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

type adminDownlinkSummary struct {
	StorageType     string `json:"storage_type"`
	StoreConfigured bool   `json:"store_configured"`
	RetryInterval   string `json:"retry_interval,omitempty"`
	RetryDelay      string `json:"retry_delay,omitempty"`
	AckTimeout      string `json:"ack_timeout,omitempty"`
	RetryLease      string `json:"retry_lease,omitempty"`
	MaxAttempts     int    `json:"max_attempts,omitempty"`
	ScanLimit       int    `json:"scan_limit,omitempty"`
	BindFlushLimit  int    `json:"bind_flush_limit,omitempty"`
}

type adminUpstreamSummary struct {
	Routes int `json:"routes"`
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
	return &adminRoutesHandler{config: adminConfig(config, nil, nil)}
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
	readiness := adminReadiness{Ready: h.config.health.Ready(), Status: "ready"}
	if !readiness.Ready {
		readiness.Status = "draining"
	}

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
		Downlink:     adminDownlinkFromConfig(config),
		Upstream:     adminUpstreamSummary{Routes: len(config.UpstreamRoutes)},
		Dependencies: adminDependencies(config, h.config.registry, h.config.clusterEnabled),
	}
	writeAdminJSON(w, http.StatusOK, resp)
}

type adminRoutesHandler struct {
	config adminHandlerConfig
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

	routes := make([]adminRoute, 0, len(h.config.config.UpstreamRoutes))
	for _, route := range h.config.config.UpstreamRoutes {
		routes = append(routes, adminRouteFromConfig(route))
	}
	writeAdminJSON(w, http.StatusOK, adminRoutesResponse{
		Code:        "ok",
		GatewayNode: h.config.gatewayNode,
		Total:       len(routes),
		Routes:      routes,
	})
}

func (c adminHandlerConfig) authorized(r *http.Request) bool {
	return c.internalToken == "" || r.Header.Get(downlink.InternalTokenHeader) == c.internalToken
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

func adminDownlinkFromConfig(config Config) adminDownlinkSummary {
	return adminDownlinkSummary{
		StorageType:     config.DownlinkStorage.Type,
		StoreConfigured: config.DownlinkStore != nil,
		RetryInterval:   durationString(config.DownlinkDelivery.RetryInterval),
		RetryDelay:      durationString(config.DownlinkDelivery.RetryDelay),
		AckTimeout:      durationString(config.DownlinkDelivery.AckTimeout),
		RetryLease:      durationString(config.DownlinkDelivery.RetryLease),
		MaxAttempts:     config.DownlinkDelivery.MaxAttempts,
		ScanLimit:       config.DownlinkDelivery.ScanLimit,
		BindFlushLimit:  config.DownlinkDelivery.BindFlushLimit,
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
			URL:     sanitizeAdminURL(route.HTTP.URL),
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
