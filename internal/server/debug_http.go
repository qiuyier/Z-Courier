package server

import (
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/adminaudit"
	"github.com/qiuyier/Z-Courier/internal/cluster"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/httpauth"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"github.com/qiuyier/Z-Courier/internal/session"
	"go.uber.org/zap"
)

const (
	defaultDebugSessionLimit = 100
	maxDebugSessionLimit     = 1000
)

type debugHandlerConfig struct {
	gatewayNode        string
	internalToken      string
	maxRequestBodySize int64
	sessions           *session.Manager
	connections        downlink.ConnectionFinder
	registry           cluster.OnlineRegistry
	clusterEnabled     bool
	logger             *zap.Logger
	audit              adminaudit.Recorder
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
	SessionID     string         `json:"session_id,omitempty"`
	ClientID      string         `json:"client_id,omitempty"`
	DeviceID      string         `json:"device_id,omitempty"`
	Limit         int            `json:"limit"`
	Total         int            `json:"total"`
	UniqueClients int            `json:"unique_clients"`
	Sessions      []debugSession `json:"sessions"`
}

type debugSessionDisconnectRequest struct {
	SessionID string `json:"session_id"`
	ClientID  string `json:"client_id,omitempty"`
	DeviceID  string `json:"device_id,omitempty"`
}

type debugSessionDisconnectResponse struct {
	Code              string        `json:"code"`
	Reason            string        `json:"reason,omitempty"`
	GatewayNode       string        `json:"gateway_node"`
	SessionID         string        `json:"session_id,omitempty"`
	ConnID            uint64        `json:"conn_id,omitempty"`
	ClientID          string        `json:"client_id,omitempty"`
	DeviceID          string        `json:"device_id,omitempty"`
	LocalSessionFound bool          `json:"local_session_found"`
	Disconnected      bool          `json:"disconnected"`
	LocalSession      *debugSession `json:"local_session,omitempty"`
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

func newDebugSessionDisconnectHandler(config Config, connections downlink.ConnectionFinder, logger *zap.Logger, audit adminaudit.Recorder) http.Handler {
	debugConfig := debugConfig(config, nil)
	debugConfig.connections = connections
	debugConfig.logger = logger
	debugConfig.audit = audit
	return &debugSessionDisconnectHandler{config: debugConfig}
}

func debugConfig(config Config, registry cluster.OnlineRegistry) debugHandlerConfig {
	return debugHandlerConfig{
		gatewayNode:        config.GatewayNode,
		internalToken:      config.InternalToken,
		maxRequestBodySize: config.InternalMaxRequestBodySize,
		sessions:           config.Sessions,
		registry:           registry,
		clusterEnabled:     config.Cluster.Enabled && registry != nil,
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

	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	clientID := strings.TrimSpace(r.URL.Query().Get("client_id"))
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	var found []*session.Session
	if h.config.sessions != nil {
		switch {
		case sessionID != "":
			if item, ok := h.config.sessions.GetBySessionID(sessionID); ok && sessionMatchesFilters(item, clientID, deviceID) {
				found = []*session.Session{item}
			}
		case clientID != "" && deviceID != "":
			if item, ok := h.config.sessions.GetByClientDevice(clientID, deviceID); ok {
				found = []*session.Session{item}
			}
		case clientID != "":
			found = h.config.sessions.ListByClientID(clientID)
			if deviceID != "" {
				found = filterSessionsByDeviceID(found, deviceID)
			}
		default:
			found = h.config.sessions.Snapshot()
			if deviceID != "" {
				found = filterSessionsByDeviceID(found, deviceID)
			}
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
		SessionID:     sessionID,
		ClientID:      clientID,
		DeviceID:      deviceID,
		Limit:         limit,
		Total:         total,
		UniqueClients: uniqueClientCount(found),
		Sessions:      make([]debugSession, 0, len(found)),
	}
	if h.config.sessions != nil && sessionID == "" && clientID == "" && deviceID == "" {
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

func sessionMatchesFilters(found *session.Session, clientID string, deviceID string) bool {
	if found == nil {
		return false
	}
	if clientID != "" && found.ClientID != clientID {
		return false
	}
	if deviceID != "" && found.DeviceID != deviceID {
		return false
	}
	return true
}

func filterSessionsByDeviceID(items []*session.Session, deviceID string) []*session.Session {
	if deviceID == "" {
		return items
	}
	filtered := make([]*session.Session, 0, len(items))
	for _, item := range items {
		if item != nil && item.DeviceID == deviceID {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

type debugSessionDisconnectHandler struct {
	config debugHandlerConfig
}

type connectionStopper interface {
	Stop()
}

func (h *debugSessionDisconnectHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeDebugJSON(w, http.StatusMethodNotAllowed, debugSessionDisconnectResponse{Code: "method_not_allowed", GatewayNode: h.config.gatewayNode})
		return
	}
	if !h.authorized(r) {
		writeDebugJSON(w, http.StatusUnauthorized, debugSessionDisconnectResponse{Code: "unauthorized", GatewayNode: h.config.gatewayNode})
		return
	}

	req, ok := h.readRequest(w, r)
	if !ok {
		return
	}
	resp := debugSessionDisconnectResponse{
		GatewayNode: h.config.gatewayNode,
		SessionID:   req.SessionID,
		ClientID:    req.ClientID,
		DeviceID:    req.DeviceID,
	}
	if req.SessionID == "" {
		resp.Code = "bad_request"
		resp.Reason = "session_id is required"
		h.recordAndAudit(r, "bad_request", http.StatusBadRequest, resp, nil)
		writeDebugJSON(w, http.StatusBadRequest, resp)
		return
	}
	if h.config.sessions == nil {
		resp.Code = "sessions_unavailable"
		resp.Reason = "session manager is not configured"
		h.recordAndAudit(r, "sessions_unavailable", http.StatusServiceUnavailable, resp, nil)
		writeDebugJSON(w, http.StatusServiceUnavailable, resp)
		return
	}
	if h.config.connections == nil {
		resp.Code = "connections_unavailable"
		resp.Reason = "connection finder is not configured"
		h.recordAndAudit(r, "connections_unavailable", http.StatusServiceUnavailable, resp, nil)
		writeDebugJSON(w, http.StatusServiceUnavailable, resp)
		return
	}

	found, foundOK := h.config.sessions.GetBySessionID(req.SessionID)
	if !foundOK || found == nil {
		resp.Code = "session_not_found"
		resp.Reason = "local session was not found on this gateway"
		h.recordAndAudit(r, "session_not_found", http.StatusNotFound, resp, nil)
		writeDebugJSON(w, http.StatusNotFound, resp)
		return
	}

	resp.LocalSessionFound = true
	resp.LocalSession = debugSessionFromSession(found)
	resp.ConnID = found.ConnID
	resp.ClientID = found.ClientID
	resp.DeviceID = found.DeviceID
	if req.ClientID != "" && req.ClientID != found.ClientID {
		resp.Code = "session_mismatch"
		resp.Reason = "client_id does not match local session"
		h.recordAndAudit(r, "session_mismatch", http.StatusConflict, resp, nil)
		writeDebugJSON(w, http.StatusConflict, resp)
		return
	}
	if req.DeviceID != "" && req.DeviceID != found.DeviceID {
		resp.Code = "session_mismatch"
		resp.Reason = "device_id does not match local session"
		h.recordAndAudit(r, "session_mismatch", http.StatusConflict, resp, nil)
		writeDebugJSON(w, http.StatusConflict, resp)
		return
	}

	conn, err := h.config.connections.Get(found.ConnID)
	if err != nil || conn == nil {
		h.config.sessions.UnbindByConnID(found.ConnID)
		resp.Code = "connection_not_found"
		resp.Reason = "local connection was not found; stale session binding was removed"
		h.recordAndAudit(r, "connection_not_found", http.StatusConflict, resp, err)
		writeDebugJSON(w, http.StatusConflict, resp)
		return
	}
	stopper, ok := conn.(connectionStopper)
	if !ok {
		resp.Code = "connection_not_stoppable"
		resp.Reason = "local connection does not support stop"
		h.recordAndAudit(r, "connection_not_stoppable", http.StatusConflict, resp, nil)
		writeDebugJSON(w, http.StatusConflict, resp)
		return
	}

	stopper.Stop()
	h.config.sessions.UnbindByConnID(found.ConnID)
	resp.Code = "ok"
	resp.Disconnected = true
	h.recordAndAudit(r, "ok", http.StatusOK, resp, nil)
	writeDebugJSON(w, http.StatusOK, resp)
}

func (h *debugSessionDisconnectHandler) readRequest(w http.ResponseWriter, r *http.Request) (debugSessionDisconnectRequest, bool) {
	if r.Body == nil {
		return debugSessionDisconnectRequest{}, true
	}
	defer r.Body.Close()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.config.maxRequestBodySize))
	if err != nil {
		writeDebugJSON(w, http.StatusRequestEntityTooLarge, debugSessionDisconnectResponse{
			Code:        "request_too_large",
			Reason:      err.Error(),
			GatewayNode: h.config.gatewayNode,
		})
		return debugSessionDisconnectRequest{}, false
	}
	var req debugSessionDisconnectRequest
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := sonic.Unmarshal(body, &req); err != nil {
			writeDebugJSON(w, http.StatusBadRequest, debugSessionDisconnectResponse{
				Code:        "bad_request",
				Reason:      err.Error(),
				GatewayNode: h.config.gatewayNode,
			})
			return debugSessionDisconnectRequest{}, false
		}
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.ClientID = strings.TrimSpace(req.ClientID)
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	return req, true
}

func (h *debugSessionDisconnectHandler) authorized(r *http.Request) bool {
	return h.config.internalToken == "" || r.Header.Get(downlink.InternalTokenHeader) == h.config.internalToken
}

func (h *debugSessionDisconnectHandler) recordAndAudit(r *http.Request, result string, statusCode int, resp debugSessionDisconnectResponse, err error) {
	metrics.RecordAdminSessionDisconnect(result)
	identity := debugAuthIdentity(r, h.config.internalToken)
	role := strings.TrimSpace(identity.Role)
	if role == "" {
		role = "unknown"
	} else {
		role = normalizeAdminRole(role)
	}
	adminaudit.Record(h.config.audit, adminaudit.Entry{
		Action:          "admin_session_disconnect",
		Result:          result,
		HTTPStatus:      statusCode,
		GatewayNode:     h.config.gatewayNode,
		AuthMode:        identity.Mode,
		Principal:       identity.Principal,
		Role:            role,
		AdminSessionID:  identity.SessionID,
		AuthKeyID:       identity.KeyID,
		Method:          r.Method,
		Path:            r.URL.Path,
		RemoteAddr:      r.RemoteAddr,
		TargetClientID:  resp.ClientID,
		TargetDeviceID:  resp.DeviceID,
		TargetSessionID: resp.SessionID,
		TargetConnID:    resp.ConnID,
		Reason:          nonEmptyString(resp.Reason, errorString(err)),
		Details: map[string]string{
			"disconnected": strconv.FormatBool(resp.Disconnected),
		},
	})
	if h.config.logger == nil {
		return
	}

	fields := []zap.Field{
		zap.String("audit_event", "admin_session_disconnect"),
		zap.String("result", result),
		zap.Int("http_status", statusCode),
		zap.String("auth_mode", identity.Mode),
		zap.String("principal", identity.Principal),
		zap.String("role", role),
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("remote_addr", r.RemoteAddr),
		zap.String("gateway_node", h.config.gatewayNode),
		zap.String("target_session_id", resp.SessionID),
		zap.Uint64("target_conn_id", resp.ConnID),
		zap.String("target_client_id", resp.ClientID),
		zap.String("target_device_id", resp.DeviceID),
		zap.Bool("disconnected", resp.Disconnected),
	}
	if identity.KeyID != "" {
		fields = append(fields, zap.String("auth_key_id", identity.KeyID))
	}
	if identity.SessionID != "" {
		fields = append(fields, zap.String("admin_session_id", identity.SessionID))
	}
	if resp.Reason != "" {
		fields = append(fields, zap.String("reason", resp.Reason))
	}
	if err != nil {
		fields = append(fields, zap.Error(err))
	}
	if statusCode >= http.StatusBadRequest {
		h.config.logger.Warn("admin session disconnect audit", fields...)
		return
	}
	h.config.logger.Info("admin session disconnect audit", fields...)
}

func debugAuthIdentity(r *http.Request, internalToken string) httpauth.Identity {
	if identity, ok := httpauth.IdentityFromContext(r.Context()); ok {
		if identity.Mode == "" {
			identity.Mode = httpauth.ModeNone
		}
		return identity
	}
	if internalToken != "" {
		return httpauth.Identity{Mode: httpauth.ModeToken}
	}
	return httpauth.Identity{Mode: httpauth.ModeNone}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func nonEmptyString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
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
