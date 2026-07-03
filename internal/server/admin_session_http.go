package server

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/httpauth"
)

const (
	adminSessionLoginPath  = "/internal/admin/session/login"
	adminSessionMePath     = "/internal/admin/session/me"
	adminSessionLogoutPath = "/internal/admin/session/logout"
)

type adminSessionHTTPConfig struct {
	gatewayNode        string
	internalToken      string
	internalAuthMode   string
	maxRequestBodySize int64
	sessionConfig      AdminConsoleSessionConfig
	sessions           *adminSessionManager
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
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	ExpiresInMS int64     `json:"expires_in_ms"`
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

func newAdminSessionHTTPConfig(config Config, sessions *adminSessionManager) adminSessionHTTPConfig {
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
		writeAdminSessionJSON(w, http.StatusMethodNotAllowed, adminSessionResponse{Code: "method_not_allowed", GatewayNode: h.config.gatewayNode})
		return
	}
	if h.config.sessions == nil {
		writeAdminSessionJSON(w, http.StatusNotFound, adminSessionResponse{Code: "not_found", GatewayNode: h.config.gatewayNode})
		return
	}

	principal, ok, handled := h.authorizedPrincipal(w, r)
	if handled {
		return
	}
	if !ok {
		writeAdminSessionJSON(w, http.StatusUnauthorized, adminSessionResponse{Code: "unauthorized", GatewayNode: h.config.gatewayNode})
		return
	}

	token, session, err := h.config.sessions.Create(principal)
	if err != nil {
		writeAdminSessionJSON(w, http.StatusInternalServerError, adminSessionResponse{Code: "session_create_failed", Reason: err.Error(), GatewayNode: h.config.gatewayNode})
		return
	}
	setAdminSessionCookie(w, h.config.sessionConfig, token, session.ExpiresAt)
	writeAdminSessionJSON(w, http.StatusOK, adminSessionResponse{
		Code:        "ok",
		GatewayNode: h.config.gatewayNode,
		Session:     adminSessionInfoFromSession(session, h.config.sessions.now().UTC()),
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
		writeAdminSessionJSON(w, http.StatusRequestEntityTooLarge, adminSessionResponse{Code: "request_too_large", Reason: err.Error(), GatewayNode: h.config.gatewayNode})
		return adminSessionLoginRequest{}, false
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return adminSessionLoginRequest{}, true
	}
	var request adminSessionLoginRequest
	if err := sonic.Unmarshal(body, &request); err != nil {
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
	session, ok := adminSessionFromCookie(r, h.config.sessionConfig, h.config.sessions)
	if !ok {
		writeAdminSessionJSON(w, http.StatusUnauthorized, adminSessionResponse{Code: "unauthorized", GatewayNode: h.config.gatewayNode})
		return
	}
	writeAdminSessionJSON(w, http.StatusOK, adminSessionResponse{
		Code:        "ok",
		GatewayNode: h.config.gatewayNode,
		Session:     adminSessionInfoFromSession(session, h.config.sessions.now().UTC()),
	})
}

func (h *adminSessionLogoutHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAdminSessionJSON(w, http.StatusMethodNotAllowed, adminSessionResponse{Code: "method_not_allowed", GatewayNode: h.config.gatewayNode})
		return
	}
	cookie, err := r.Cookie(h.config.sessionConfig.CookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" || h.config.sessions == nil {
		clearAdminSessionCookie(w, h.config.sessionConfig)
		writeAdminSessionJSON(w, http.StatusUnauthorized, adminSessionResponse{Code: "unauthorized", GatewayNode: h.config.gatewayNode})
		return
	}
	if !h.config.sessions.Delete(cookie.Value) {
		clearAdminSessionCookie(w, h.config.sessionConfig)
		writeAdminSessionJSON(w, http.StatusUnauthorized, adminSessionResponse{Code: "unauthorized", GatewayNode: h.config.gatewayNode})
		return
	}
	clearAdminSessionCookie(w, h.config.sessionConfig)
	writeAdminSessionJSON(w, http.StatusOK, adminSessionResponse{Code: "ok", GatewayNode: h.config.gatewayNode})
}

func withAdminSessionAuth(next http.Handler, sessions *adminSessionManager, config AdminConsoleSessionConfig, internalToken string) http.Handler {
	if sessions == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := adminSessionFromCookie(r, config, sessions)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, requestWithAdminSession(r, session, internalToken))
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
		if session, ok := adminSessionFromCookie(r, h.config, h.sessions); ok {
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
		"/internal/admin/diagnostics",
		"/internal/admin/check",
		"/internal/admin/diagnose",
		"/internal/debug/route",
		"/internal/debug/sessions",
		"/internal/message/status",
		"/internal/messages",
		"/internal/message/requeue",
		"/internal/message/discard":
		return true
	default:
		return false
	}
}

func adminSessionFromCookie(r *http.Request, config AdminConsoleSessionConfig, sessions *adminSessionManager) (adminSession, bool) {
	if sessions == nil {
		return adminSession{}, false
	}
	cookie, err := r.Cookie(config.CookieName)
	if err != nil {
		return adminSession{}, false
	}
	return sessions.Lookup(cookie.Value)
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

func adminSessionInfoFromSession(session adminSession, now time.Time) *adminSessionInfo {
	expiresIn := session.ExpiresAt.Sub(now)
	if expiresIn < 0 {
		expiresIn = 0
	}
	return &adminSessionInfo{
		SessionID:   session.SessionID,
		Principal:   session.Principal,
		Role:        normalizeAdminRole(session.Role),
		Permissions: adminPermissionsForRole(session.Role),
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
