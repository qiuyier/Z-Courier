package server

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/cluster"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/session"
)

const (
	defaultDebugSessionLimit = 100
	maxDebugSessionLimit     = 1000
)

type debugHandlerConfig struct {
	gatewayNode    string
	internalToken  string
	sessions       *session.Manager
	registry       cluster.OnlineRegistry
	clusterEnabled bool
}

type debugRouteResponse struct {
	Code              string             `json:"code"`
	Reason            string             `json:"reason,omitempty"`
	GatewayNode       string             `json:"gateway_node"`
	ClientID          string             `json:"client_id,omitempty"`
	DeviceID          string             `json:"device_id,omitempty"`
	LocalSessionFound bool               `json:"local_session_found"`
	LocalSession      *debugSession      `json:"local_session,omitempty"`
	ClusterEnabled    bool               `json:"cluster_enabled"`
	ClusterRouteFound bool               `json:"cluster_route_found"`
	ClusterRoute      *debugClusterRoute `json:"cluster_route,omitempty"`
}

type debugSessionsResponse struct {
	Code          string         `json:"code"`
	Reason        string         `json:"reason,omitempty"`
	GatewayNode   string         `json:"gateway_node"`
	ClientID      string         `json:"client_id,omitempty"`
	Limit         int            `json:"limit"`
	Total         int            `json:"total"`
	UniqueClients int            `json:"unique_clients"`
	Sessions      []debugSession `json:"sessions"`
}

type debugSession struct {
	SessionID   string     `json:"session_id"`
	ConnID      uint64     `json:"conn_id"`
	ClientID    string     `json:"client_id"`
	DeviceID    string     `json:"device_id"`
	TokenID     string     `json:"token_id,omitempty"`
	GatewayNode string     `json:"gateway_node,omitempty"`
	ConnectedAt *time.Time `json:"connected_at,omitempty"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
}

type debugClusterRoute struct {
	ClientID        string     `json:"client_id"`
	DeviceID        string     `json:"device_id"`
	SessionID       string     `json:"session_id"`
	GatewayNode     string     `json:"gateway_node"`
	InternalAddr    string     `json:"internal_addr,omitempty"`
	TokenID         string     `json:"token_id,omitempty"`
	UpdatedAt       *time.Time `json:"updated_at,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	ExpiresInMillis int64      `json:"expires_in_ms,omitempty"`
}

func newDebugRouteHandler(config Config, registry cluster.OnlineRegistry) http.Handler {
	return &debugRouteHandler{config: debugConfig(config, registry)}
}

func newDebugSessionsHandler(config Config) http.Handler {
	return &debugSessionsHandler{config: debugConfig(config, nil)}
}

func debugConfig(config Config, registry cluster.OnlineRegistry) debugHandlerConfig {
	return debugHandlerConfig{
		gatewayNode:    config.GatewayNode,
		internalToken:  config.InternalToken,
		sessions:       config.Sessions,
		registry:       registry,
		clusterEnabled: config.Cluster.Enabled && registry != nil,
	}
}

type debugRouteHandler struct {
	config debugHandlerConfig
}

func (h *debugRouteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDebugJSON(w, http.StatusMethodNotAllowed, debugRouteResponse{Code: "method_not_allowed", GatewayNode: h.config.gatewayNode})
		return
	}
	if !h.authorized(r) {
		writeDebugJSON(w, http.StatusUnauthorized, debugRouteResponse{Code: "unauthorized", GatewayNode: h.config.gatewayNode})
		return
	}

	clientID := strings.TrimSpace(r.URL.Query().Get("client_id"))
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	if clientID == "" || deviceID == "" {
		writeDebugJSON(w, http.StatusBadRequest, debugRouteResponse{
			Code:        "bad_request",
			Reason:      "client_id and device_id are required",
			GatewayNode: h.config.gatewayNode,
			ClientID:    clientID,
			DeviceID:    deviceID,
		})
		return
	}

	resp := debugRouteResponse{
		Code:           "ok",
		GatewayNode:    h.config.gatewayNode,
		ClientID:       clientID,
		DeviceID:       deviceID,
		ClusterEnabled: h.config.clusterEnabled,
	}
	if h.config.sessions != nil {
		if found, ok := h.config.sessions.GetByClientDevice(clientID, deviceID); ok {
			resp.LocalSessionFound = true
			resp.LocalSession = debugSessionFromSession(found)
		}
	}
	if h.config.registry != nil {
		entry, ok, err := h.config.registry.Lookup(r.Context(), cluster.RouteKey{ClientID: clientID, DeviceID: deviceID})
		if err != nil {
			status := http.StatusBadGateway
			code := "registry_lookup_failed"
			if errors.Is(err, cluster.ErrInvalidRouteKey) {
				status = http.StatusBadRequest
				code = "bad_request"
			}
			resp.Code = code
			resp.Reason = err.Error()
			writeDebugJSON(w, status, resp)
			return
		}
		if ok {
			resp.ClusterRouteFound = true
			resp.ClusterRoute = debugClusterRouteFromEntry(entry, time.Now())
		}
	}

	writeDebugJSON(w, http.StatusOK, resp)
}

func (h *debugRouteHandler) authorized(r *http.Request) bool {
	return h.config.internalToken == "" || r.Header.Get(downlink.InternalTokenHeader) == h.config.internalToken
}

type debugSessionsHandler struct {
	config debugHandlerConfig
}

func (h *debugSessionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDebugJSON(w, http.StatusMethodNotAllowed, debugSessionsResponse{Code: "method_not_allowed", GatewayNode: h.config.gatewayNode})
		return
	}
	if !h.authorized(r) {
		writeDebugJSON(w, http.StatusUnauthorized, debugSessionsResponse{Code: "unauthorized", GatewayNode: h.config.gatewayNode})
		return
	}

	limit, err := parseDebugSessionLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeDebugJSON(w, http.StatusBadRequest, debugSessionsResponse{Code: "bad_request", Reason: err.Error(), GatewayNode: h.config.gatewayNode})
		return
	}

	clientID := strings.TrimSpace(r.URL.Query().Get("client_id"))
	var found []*session.Session
	if h.config.sessions != nil {
		if clientID != "" {
			found = h.config.sessions.ListByClientID(clientID)
		} else {
			found = h.config.sessions.Snapshot()
		}
	}
	sortSessions(found)

	total := len(found)
	if len(found) > limit {
		found = found[:limit]
	}

	resp := debugSessionsResponse{
		Code:          "ok",
		GatewayNode:   h.config.gatewayNode,
		ClientID:      clientID,
		Limit:         limit,
		Total:         total,
		UniqueClients: uniqueClientCount(found),
		Sessions:      make([]debugSession, 0, len(found)),
	}
	if h.config.sessions != nil && clientID == "" {
		resp.UniqueClients = h.config.sessions.UniqueClientLen()
	}
	for _, item := range found {
		if converted := debugSessionFromSession(item); converted != nil {
			resp.Sessions = append(resp.Sessions, *converted)
		}
	}

	writeDebugJSON(w, http.StatusOK, resp)
}

func (h *debugSessionsHandler) authorized(r *http.Request) bool {
	return h.config.internalToken == "" || r.Header.Get(downlink.InternalTokenHeader) == h.config.internalToken
}

func parseDebugSessionLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultDebugSessionLimit, nil
	}

	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return 0, errors.New("limit must be greater than 0")
	}
	if limit > maxDebugSessionLimit {
		return maxDebugSessionLimit, nil
	}
	return limit, nil
}

func debugSessionFromSession(found *session.Session) *debugSession {
	if found == nil {
		return nil
	}

	return &debugSession{
		SessionID:   found.SessionID,
		ConnID:      found.ConnID,
		ClientID:    found.ClientID,
		DeviceID:    found.DeviceID,
		TokenID:     found.TokenID,
		GatewayNode: found.GatewayNode,
		ConnectedAt: optionalDebugTime(found.ConnectedAt),
		LastSeenAt:  optionalDebugTime(found.LastSeenAt),
	}
}

func debugClusterRouteFromEntry(entry cluster.RouteEntry, now time.Time) *debugClusterRoute {
	return &debugClusterRoute{
		ClientID:        entry.ClientID,
		DeviceID:        entry.DeviceID,
		SessionID:       entry.SessionID,
		GatewayNode:     entry.GatewayNode,
		InternalAddr:    entry.InternalAddr,
		TokenID:         entry.TokenID,
		UpdatedAt:       optionalDebugTime(entry.UpdatedAt),
		ExpiresAt:       optionalDebugTime(entry.ExpiresAt),
		ExpiresInMillis: expiresInMillis(entry.ExpiresAt, now),
	}
}

func expiresInMillis(expiresAt time.Time, now time.Time) int64 {
	if expiresAt.IsZero() {
		return 0
	}
	if now.IsZero() {
		now = time.Now()
	}
	remaining := expiresAt.Sub(now)
	if remaining <= 0 {
		return 0
	}
	return remaining.Milliseconds()
}

func optionalDebugTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func sortSessions(items []*session.Session) {
	sort.Slice(items, func(i, j int) bool {
		left := items[i]
		right := items[j]
		if left == nil {
			return false
		}
		if right == nil {
			return true
		}
		if left.ClientID != right.ClientID {
			return left.ClientID < right.ClientID
		}
		if left.DeviceID != right.DeviceID {
			return left.DeviceID < right.DeviceID
		}
		return left.ConnID < right.ConnID
	})
}

func uniqueClientCount(items []*session.Session) int {
	seen := make(map[string]struct{})
	for _, item := range items {
		if item == nil || item.ClientID == "" {
			continue
		}
		seen[item.ClientID] = struct{}{}
	}
	return len(seen)
}

func writeDebugJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, err := sonic.Marshal(value)
	if err != nil {
		_, _ = w.Write([]byte(`{"code":"marshal_error"}`))
		return
	}
	_, _ = w.Write(body)
}
