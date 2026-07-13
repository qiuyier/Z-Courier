package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/adminaudit"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/httpauth"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"go.uber.org/zap"
)

const (
	adminSessionLoginPath  = "/internal/admin/session/login"
	adminSessionMePath     = "/internal/admin/session/me"
	adminSessionLogoutPath = "/internal/admin/session/logout"
	adminCSRFHeader        = "X-ZCourier-CSRF-Token"
)

type adminSessionHTTPConfig struct {
	gatewayNode        string
	internalToken      string
	internalAuthMode   string
	maxRequestBodySize int64
	sessionConfig      AdminConsoleSessionConfig
	sessions           *adminSessionManager
	audit              adminaudit.Recorder
}

type adminSessionLoginRequest struct {
	Token string `json:"token"`
}

type adminSessionResponse struct {
	Code        string            `json:"code"`
	Reason      string            `json:"reason,omitempty"`
	GatewayNode string            `json:"gateway_node"`
	Session     *adminSessionInfo `json:"session,omitempty"`
}

type adminSessionInfo struct {
	SessionID   string    `json:"session_id"`
	Principal   string    `json:"principal"`
	Role        string    `json:"role"`
	Permissions []string  `json:"permissions,omitempty"`
	CSRFToken   string    `json:"csrf_token,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	ExpiresInMS int64     `json:"expires_in_ms"`
}

type adminSessionCSRFResponse struct {
	Code        string `json:"code"`
	Reason      string `json:"reason,omitempty"`
	GatewayNode string `json:"gateway_node"`
}

type adminSessionLoginHandler struct {
	config adminSessionHTTPConfig
}

type adminSessionMeHandler struct {
	config adminSessionHTTPConfig
}

type adminSessionLogoutHandler struct {
	config adminSessionHTTPConfig
}

func newAdminSessionHTTPConfig(config Config, sessions *adminSessionManager, audit adminaudit.Recorder) adminSessionHTTPConfig {
	sessionConfig := config.AdminConsole.Session
	if sessions != nil {
		sessionConfig = sessions.config
	}
	return adminSessionHTTPConfig{
		gatewayNode:        config.GatewayNode,
		internalToken:      config.InternalToken,
		internalAuthMode:   config.InternalHTTPAuth.Mode,
		maxRequestBodySize: config.InternalMaxRequestBodySize,
		sessionConfig:      sessionConfig,
		sessions:           sessions,
		audit:              audit,
	}
}

func newAdminSessionLoginHandler(config adminSessionHTTPConfig) http.Handler {
	return &adminSessionLoginHandler{config: config}
}

func newAdminSessionMeHandler(config adminSessionHTTPConfig) http.Handler {
	return &adminSessionMeHandler{config: config}
}

func newAdminSessionLogoutHandler(config adminSessionHTTPConfig) http.Handler {
	return &adminSessionLogoutHandler{config: config}
}

func (h *adminSessionLoginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.recordLogin(r, "method_not_allowed", http.StatusMethodNotAllowed, "", adminSession{}, "method not allowed")
		writeAdminSessionJSON(w, http.StatusMethodNotAllowed, adminSessionResponse{Code: "method_not_allowed", GatewayNode: h.config.gatewayNode})
		return
	}
	if h.config.sessions == nil {
		h.recordLogin(r, "not_found", http.StatusNotFound, "", adminSession{}, "admin session manager is not configured")
		writeAdminSessionJSON(w, http.StatusNotFound, adminSessionResponse{Code: "not_found", GatewayNode: h.config.gatewayNode})
		return
	}

	principal, ok, handled := h.authorizedPrincipal(w, r)
	if handled {
		return
	}
	if !ok {
		h.recordLogin(r, "unauthorized", http.StatusUnauthorized, "", adminSession{}, "invalid internal credential")
		writeAdminSessionJSON(w, http.StatusUnauthorized, adminSessionResponse{Code: "unauthorized", GatewayNode: h.config.gatewayNode})
		return
	}

	token, session, err := h.config.sessions.Create(principal)
	if err != nil {
		h.recordLogin(r, "session_create_failed", http.StatusInternalServerError, principal, adminSession{}, err.Error())
		writeAdminSessionJSON(w, http.StatusInternalServerError, adminSessionResponse{Code: "session_create_failed", Reason: err.Error(), GatewayNode: h.config.gatewayNode})
		return
	}
	setAdminSessionCookie(w, h.config.sessionConfig, token, session.ExpiresAt)
	h.recordLogin(r, "success", http.StatusOK, principal, session, "")
	writeAdminSessionJSON(w, http.StatusOK, adminSessionResponse{
		Code:        "ok",
		GatewayNode: h.config.gatewayNode,
		Session:     adminSessionInfoFromSession(session, h.config.sessions.now().UTC(), adminSessionCSRFToken(token)),
	})
}

func (h *adminSessionLoginHandler) authorizedPrincipal(w http.ResponseWriter, r *http.Request) (string, bool, bool) {
	if h.config.internalAuthMode == InternalHTTPAuthModeHMAC {
		identity, ok := httpauth.IdentityFromContext(r.Context())
		if !ok || identity.Mode != httpauth.ModeHMAC {
			return "", false, false
		}
		if identity.KeyID != "" {
			return "hmac:" + identity.KeyID, true, false
		}
		return "hmac", true, false
	}

	token := strings.TrimSpace(r.Header.Get(downlink.InternalTokenHeader))
	if token == "" {
		request, ok := h.loginRequest(w, r)
		if !ok {
			return "", false, true
		}
		token = strings.TrimSpace(request.Token)
	}
	if h.config.internalToken != "" && token != h.config.internalToken {
		return "", false, false
	}
	return "internal-token", true, false
}

func (h *adminSessionLoginHandler) loginRequest(w http.ResponseWriter, r *http.Request) (adminSessionLoginRequest, bool) {
	if r.Body == nil {
		return adminSessionLoginRequest{}, true
	}
	defer r.Body.Close()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.config.maxRequestBodySize))
	if err != nil {
		h.recordLogin(r, "request_too_large", http.StatusRequestEntityTooLarge, "", adminSession{}, err.Error())
		writeAdminSessionJSON(w, http.StatusRequestEntityTooLarge, adminSessionResponse{Code: "request_too_large", Reason: err.Error(), GatewayNode: h.config.gatewayNode})
		return adminSessionLoginRequest{}, false
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return adminSessionLoginRequest{}, true
	}
	var request adminSessionLoginRequest
	if err := sonic.Unmarshal(body, &request); err != nil {
		h.recordLogin(r, "bad_request", http.StatusBadRequest, "", adminSession{}, err.Error())
		writeAdminSessionJSON(w, http.StatusBadRequest, adminSessionResponse{Code: "bad_request", Reason: err.Error(), GatewayNode: h.config.gatewayNode})
		return adminSessionLoginRequest{}, false
	}
	return request, true
}

func (h *adminSessionMeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminSessionJSON(w, http.StatusMethodNotAllowed, adminSessionResponse{Code: "method_not_allowed", GatewayNode: h.config.gatewayNode})
		return
	}
	token, hasToken := adminSessionCookieToken(r, h.config.sessionConfig)
	session, ok, err := adminSessionFromCookie(r, h.config.sessionConfig, h.config.sessions)
	if err != nil {
		writeAdminSessionJSON(w, http.StatusServiceUnavailable, adminSessionResponse{Code: "session_store_error", Reason: err.Error(), GatewayNode: h.config.gatewayNode})
		return
	}
	if !ok {
		writeAdminSessionJSON(w, http.StatusUnauthorized, adminSessionResponse{Code: "unauthorized", GatewayNode: h.config.gatewayNode})
		return
	}
	csrfToken := ""
	if hasToken {
		csrfToken = adminSessionCSRFToken(token)
	}
	writeAdminSessionJSON(w, http.StatusOK, adminSessionResponse{
		Code:        "ok",
		GatewayNode: h.config.gatewayNode,
		Session:     adminSessionInfoFromSession(session, h.config.sessions.now().UTC(), csrfToken),
	})
}

func (h *adminSessionLogoutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.recordLogout(r, "method_not_allowed", http.StatusMethodNotAllowed, adminSession{}, "method not allowed")
		writeAdminSessionJSON(w, http.StatusMethodNotAllowed, adminSessionResponse{Code: "method_not_allowed", GatewayNode: h.config.gatewayNode})
		return
	}
	cookie, err := r.Cookie(h.config.sessionConfig.CookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" || h.config.sessions == nil {
		clearAdminSessionCookie(w, h.config.sessionConfig)
		h.recordLogout(r, "unauthorized", http.StatusUnauthorized, adminSession{}, "admin session cookie is missing")
		writeAdminSessionJSON(w, http.StatusUnauthorized, adminSessionResponse{Code: "unauthorized", GatewayNode: h.config.gatewayNode})
		return
	}
	session, ok, err := h.config.sessions.Lookup(cookie.Value)
	if err != nil {
		h.recordLogout(r, "session_store_error", http.StatusServiceUnavailable, adminSession{}, err.Error())
		writeAdminSessionJSON(w, http.StatusServiceUnavailable, adminSessionResponse{Code: "session_store_error", Reason: err.Error(), GatewayNode: h.config.gatewayNode})
		return
	}
	deleted := false
	if ok {
		deleted, err = h.config.sessions.Delete(cookie.Value)
		if err != nil {
			h.recordLogout(r, "session_store_error", http.StatusServiceUnavailable, session, err.Error())
			writeAdminSessionJSON(w, http.StatusServiceUnavailable, adminSessionResponse{Code: "session_store_error", Reason: err.Error(), GatewayNode: h.config.gatewayNode})
			return
		}
	}
	if !ok || !deleted {
		clearAdminSessionCookie(w, h.config.sessionConfig)
		h.recordLogout(r, "unauthorized", http.StatusUnauthorized, adminSession{}, "admin session is missing or expired")
		writeAdminSessionJSON(w, http.StatusUnauthorized, adminSessionResponse{Code: "unauthorized", GatewayNode: h.config.gatewayNode})
		return
	}
	clearAdminSessionCookie(w, h.config.sessionConfig)
	h.recordLogout(r, "success", http.StatusOK, session, "")
	writeAdminSessionJSON(w, http.StatusOK, adminSessionResponse{Code: "ok", GatewayNode: h.config.gatewayNode})
}

func (h *adminSessionLoginHandler) recordLogin(r *http.Request, result string, statusCode int, principal string, session adminSession, reason string) {
	authMode := h.config.internalAuthMode
	authKeyID := ""
	if identity, ok := httpauth.IdentityFromContext(r.Context()); ok {
		authMode = identity.Mode
		authKeyID = identity.KeyID
		if principal == "" {
			principal = identity.Principal
		}
	}
	if authMode == "" {
		authMode = httpauth.ModeToken
	}
	h.recordSessionAudit(r, "admin_session_login", result, statusCode, authMode, authKeyID, principal, session, reason)
}

func (h *adminSessionLoginHandler) recordSessionAudit(
	r *http.Request,
	action string,
	result string,
	statusCode int,
	authMode string,
	authKeyID string,
	principal string,
	session adminSession,
	reason string,
) {
	adminaudit.Record(h.config.audit, adminaudit.Entry{
		Action:         action,
		Result:         result,
		HTTPStatus:     statusCode,
		GatewayNode:    h.config.gatewayNode,
		AuthMode:       authMode,
		Principal:      principal,
		Role:           session.Role,
		AdminSessionID: session.SessionID,
		AuthKeyID:      authKeyID,
		Method:         r.Method,
		Path:           r.URL.Path,
		RemoteAddr:     r.RemoteAddr,
		Reason:         reason,
	})
}

func (h *adminSessionLogoutHandler) recordLogout(r *http.Request, result string, statusCode int, session adminSession, reason string) {
	adminaudit.Record(h.config.audit, adminaudit.Entry{
		Action:         "admin_session_logout",
		Result:         result,
		HTTPStatus:     statusCode,
		GatewayNode:    h.config.gatewayNode,
		AuthMode:       httpauth.ModeAdminSession,
		Principal:      session.Principal,
		Role:           session.Role,
		AdminSessionID: session.SessionID,
		Method:         r.Method,
		Path:           r.URL.Path,
		RemoteAddr:     r.RemoteAddr,
		Reason:         reason,
	})
}

func withAdminSessionAuth(next http.Handler, sessions *adminSessionManager, config AdminConsoleSessionConfig, internalToken string) http.Handler {
	if sessions == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok, err := adminSessionFromCookie(r, config, sessions)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, requestWithAdminSession(r, session, internalToken))
	})
}

func withAdminSessionMutationGuard(next http.Handler, config AdminConsoleSessionConfig, logger *zap.Logger, audit adminaudit.Recorder, gatewayNode string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := httpauth.IdentityFromContext(r.Context())
		if !ok || identity.Mode != httpauth.ModeAdminSession || !adminSessionMutationMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		if !adminSessionJSONContentType(r) {
			writeAdminSessionMutationRejected(w, r, identity, logger, audit, gatewayNode, http.StatusUnsupportedMediaType, "unsupported_media_type", "admin session mutation requests must use application/json", false)
			return
		}

		token, ok := adminSessionCookieToken(r, config)
		if !ok || !adminSessionCSRFEqual(adminSessionCSRFToken(token), r.Header.Get(adminCSRFHeader)) {
			writeAdminSessionMutationRejected(w, r, identity, logger, audit, gatewayNode, http.StatusForbidden, "csrf_failed", "admin session mutation requires a valid CSRF token", true)
			return
		}

		if ok, source := adminSessionSameOrigin(r); !ok {
			reason := "admin session mutation origin is not allowed"
			if source != "" {
				reason = reason + ": " + source
			}
			writeAdminSessionMutationRejected(w, r, identity, logger, audit, gatewayNode, http.StatusForbidden, "same_origin_failed", reason, true)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func adminSessionMutationMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func adminSessionJSONContentType(r *http.Request) bool {
	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	if contentType == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && strings.EqualFold(mediaType, "application/json")
}

func adminSessionCSRFToken(token string) string {
	sum := sha256.Sum256([]byte("zcourier-admin-csrf-v1:" + strings.TrimSpace(token)))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func adminSessionCSRFEqual(expected string, actual string) bool {
	expected = strings.TrimSpace(expected)
	actual = strings.TrimSpace(actual)
	if expected == "" || actual == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}

func adminSessionSameOrigin(r *http.Request) (bool, string) {
	source := strings.TrimSpace(r.Header.Get("Origin"))
	if source == "" {
		source = strings.TrimSpace(r.Header.Get("Referer"))
	}
	if source == "" {
		return true, ""
	}

	origin, err := url.Parse(source)
	if err != nil || origin.Host == "" {
		return false, source
	}
	if !strings.EqualFold(origin.Host, r.Host) {
		return false, source
	}
	if expectedScheme := adminSessionRequestScheme(r); expectedScheme != "" && origin.Scheme != "" && !strings.EqualFold(origin.Scheme, expectedScheme) {
		return false, source
	}
	return true, ""
}

func adminSessionRequestScheme(r *http.Request) string {
	forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if forwarded != "" {
		if beforeComma, _, ok := strings.Cut(forwarded, ","); ok {
			forwarded = beforeComma
		}
		return strings.ToLower(strings.TrimSpace(forwarded))
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func writeAdminSessionMutationRejected(
	w http.ResponseWriter,
	r *http.Request,
	identity httpauth.Identity,
	logger *zap.Logger,
	audit adminaudit.Recorder,
	gatewayNode string,
	status int,
	code string,
	reason string,
	countCSRF bool,
) {
	if countCSRF {
		metrics.RecordAdminCSRFRejected(code)
	}
	adminaudit.Record(audit, adminaudit.Entry{
		Action:         "admin_session_mutation_rejected",
		Result:         code,
		HTTPStatus:     status,
		GatewayNode:    gatewayNode,
		AuthMode:       identity.Mode,
		Principal:      identity.Principal,
		Role:           normalizeAdminRole(identity.Role),
		AdminSessionID: identity.SessionID,
		AuthKeyID:      identity.KeyID,
		Method:         r.Method,
		Path:           r.URL.Path,
		RemoteAddr:     r.RemoteAddr,
		Reason:         reason,
	})
	if logger != nil {
		logger.Warn(
			"admin session mutation rejected",
			zap.String("audit_event", "admin_session_mutation_rejected"),
			zap.String("code", code),
			zap.String("reason", reason),
			zap.String("principal", identity.Principal),
			zap.String("session_id", identity.SessionID),
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.String("remote_addr", r.RemoteAddr),
		)
	}
	writeAdminSessionCSRFJSON(w, status, adminSessionCSRFResponse{
		Code:        code,
		Reason:      reason,
		GatewayNode: gatewayNode,
	})
}

type adminSessionHMACBypassHandler struct {
	direct   http.Handler
	fallback http.Handler
	sessions *adminSessionManager
	config   AdminConsoleSessionConfig
}

func newAdminSessionHMACBypassHandler(direct http.Handler, fallback http.Handler, sessions *adminSessionManager, config AdminConsoleSessionConfig) http.Handler {
	if sessions == nil {
		return fallback
	}
	return &adminSessionHMACBypassHandler{
		direct:   direct,
		fallback: fallback,
		sessions: sessions,
		config:   config,
	}
}

func (h *adminSessionHMACBypassHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if adminSessionProtectedPath(r.URL.Path) {
		if session, ok, err := adminSessionFromCookie(r, h.config, h.sessions); err == nil && ok {
			h.direct.ServeHTTP(w, requestWithAdminSession(r, session, ""))
			return
		}
	}
	h.fallback.ServeHTTP(w, r)
}

func adminSessionProtectedPath(path string) bool {
	switch path {
	case adminSessionMePath,
		adminSessionLogoutPath,
		"/internal/admin/overview",
		"/internal/admin/routes",
		adminAuditPath,
		"/internal/admin/diagnostics",
		"/internal/admin/check",
		"/internal/admin/diagnose",
		"/internal/debug/route",
		"/internal/debug/sessions",
		"/internal/debug/cluster/routes",
		"/internal/debug/session/disconnect",
		"/internal/debug/push",
		"/internal/message/status",
		"/internal/messages",
		"/internal/message/requeue",
		"/internal/messages/requeue",
		"/internal/message/discard":
		return true
	case "/internal/messages/retry/scan":
		return true
	default:
		return false
	}
}

func adminSessionFromCookie(r *http.Request, config AdminConsoleSessionConfig, sessions *adminSessionManager) (adminSession, bool, error) {
	if sessions == nil {
		return adminSession{}, false, nil
	}
	token, ok := adminSessionCookieToken(r, config)
	if !ok {
		return adminSession{}, false, nil
	}
	return sessions.Lookup(token)
}

func adminSessionCookieToken(r *http.Request, config AdminConsoleSessionConfig) (string, bool) {
	cookie, err := r.Cookie(config.CookieName)
	if err != nil {
		return "", false
	}
	token := strings.TrimSpace(cookie.Value)
	if token == "" {
		return "", false
	}
	return token, true
}

func requestWithAdminSession(r *http.Request, session adminSession, internalToken string) *http.Request {
	identity := httpauth.Identity{
		Mode:      httpauth.ModeAdminSession,
		SessionID: session.SessionID,
		Principal: session.Principal,
		Role:      session.Role,
	}
	clone := r.Clone(httpauth.WithIdentity(r.Context(), identity))
	clone.Header = r.Header.Clone()
	if internalToken != "" {
		clone.Header.Set(downlink.InternalTokenHeader, internalToken)
	}
	return clone
}

func setAdminSessionCookie(w http.ResponseWriter, config AdminConsoleSessionConfig, token string, expiresAt time.Time) {
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     config.CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   config.CookieSecure,
		SameSite: adminSessionSameSite(config.CookieSameSite),
	})
}

func clearAdminSessionCookie(w http.ResponseWriter, config AdminConsoleSessionConfig) {
	http.SetCookie(w, &http.Cookie{
		Name:     config.CookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   config.CookieSecure,
		SameSite: adminSessionSameSite(config.CookieSameSite),
	})
}

func adminSessionSameSite(value string) http.SameSite {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteLaxMode
	}
}

func adminSessionInfoFromSession(session adminSession, now time.Time, csrfToken string) *adminSessionInfo {
	expiresIn := session.ExpiresAt.Sub(now)
	if expiresIn < 0 {
		expiresIn = 0
	}
	return &adminSessionInfo{
		SessionID:   session.SessionID,
		Principal:   session.Principal,
		Role:        normalizeAdminRole(session.Role),
		Permissions: adminPermissionsForRole(session.Role),
		CSRFToken:   csrfToken,
		CreatedAt:   session.CreatedAt.UTC(),
		ExpiresAt:   session.ExpiresAt.UTC(),
		LastSeenAt:  session.LastSeenAt.UTC(),
		ExpiresInMS: expiresIn.Milliseconds(),
	}
}

func writeAdminSessionJSON(w http.ResponseWriter, status int, resp adminSessionResponse) {
	data, err := sonic.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func writeAdminSessionCSRFJSON(w http.ResponseWriter, status int, resp adminSessionCSRFResponse) {
	data, err := sonic.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
