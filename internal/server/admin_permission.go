package server

import (
	"net/http"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/adminaudit"
	"github.com/qiuyier/Z-Courier/internal/httpauth"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"go.uber.org/zap"
)

const (
	adminSessionRoleReadonly = "readonly"
	adminSessionRoleOperator = "operator"
	adminSessionRoleAdmin    = "admin"

	adminPermissionRead              = "admin:read"
	adminPermissionMessageRepair     = "message:repair"
	adminPermissionRetryScan         = "message:retry_scan"
	adminPermissionSessionDisconnect = "session:disconnect"
	adminPermissionDownlinkTestPush  = "downlink:test_push"
)

type adminPermissionDeniedResponse struct {
	Code       string `json:"code"`
	Reason     string `json:"reason,omitempty"`
	Role       string `json:"role,omitempty"`
	Permission string `json:"permission,omitempty"`
}

func withAdminPermission(next http.Handler, permission string, logger *zap.Logger, audit adminaudit.Recorder, gatewayNode string) http.Handler {
	permission = strings.TrimSpace(permission)
	if permission == "" {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := httpauth.IdentityFromContext(r.Context())
		if !ok || identity.Mode != httpauth.ModeAdminSession {
			next.ServeHTTP(w, r)
			return
		}

		role := normalizeAdminRole(identity.Role)
		if adminRoleAllows(role, permission) {
			next.ServeHTTP(w, r)
			return
		}

		metrics.RecordAdminPermissionRejected(role, permission)
		adminaudit.Record(audit, adminaudit.Entry{
			Action:         "admin_permission_denied",
			Result:         "permission_denied",
			HTTPStatus:     http.StatusForbidden,
			GatewayNode:    gatewayNode,
			AuthMode:       identity.Mode,
			Principal:      identity.Principal,
			Role:           role,
			AdminSessionID: identity.SessionID,
			AuthKeyID:      identity.KeyID,
			Method:         r.Method,
			Path:           r.URL.Path,
			RemoteAddr:     r.RemoteAddr,
			Permission:     permission,
			Reason:         "admin session role does not allow this operation",
		})
		if logger != nil {
			logger.Warn(
				"admin permission denied",
				zap.String("audit_event", "admin_permission_denied"),
				zap.String("role", role),
				zap.String("permission", permission),
				zap.String("principal", identity.Principal),
				zap.String("session_id", identity.SessionID),
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("remote_addr", r.RemoteAddr),
			)
		}

		writeAdminPermissionDenied(w, role, permission)
	})
}

func writeAdminPermissionDenied(w http.ResponseWriter, role string, permission string) {
	data, err := sonic.Marshal(adminPermissionDeniedResponse{
		Code:       "permission_denied",
		Reason:     "admin session role does not allow this operation",
		Role:       role,
		Permission: permission,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write(data)
}

func normalizeAdminRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case adminSessionRoleReadonly:
		return adminSessionRoleReadonly
	case adminSessionRoleOperator:
		return adminSessionRoleOperator
	case adminSessionRoleAdmin:
		return adminSessionRoleAdmin
	default:
		return adminSessionRoleAdmin
	}
}

func adminRoleAllows(role string, permission string) bool {
	role = normalizeAdminRole(role)
	permission = strings.TrimSpace(permission)

	switch role {
	case adminSessionRoleAdmin:
		return true
	case adminSessionRoleOperator:
		return permission == adminPermissionRead ||
			permission == adminPermissionMessageRepair ||
			permission == adminPermissionRetryScan ||
			permission == adminPermissionSessionDisconnect ||
			permission == adminPermissionDownlinkTestPush
	case adminSessionRoleReadonly:
		return permission == adminPermissionRead
	default:
		return false
	}
}

func adminPermissionsForRole(role string) []string {
	switch normalizeAdminRole(role) {
	case adminSessionRoleAdmin:
		return []string{adminPermissionRead, adminPermissionMessageRepair, adminPermissionRetryScan, adminPermissionSessionDisconnect, adminPermissionDownlinkTestPush}
	case adminSessionRoleOperator:
		return []string{adminPermissionRead, adminPermissionMessageRepair, adminPermissionRetryScan, adminPermissionSessionDisconnect, adminPermissionDownlinkTestPush}
	default:
		return []string{adminPermissionRead}
	}
}
