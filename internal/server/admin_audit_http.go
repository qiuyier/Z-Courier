package server

import (
	"net/http"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/adminaudit"
	"github.com/qiuyier/Z-Courier/internal/downlink"
)

const adminAuditPath = "/internal/admin/audit"

type adminAuditResponse struct {
	Code        string             `json:"code"`
	Reason      string             `json:"reason,omitempty"`
	GatewayNode string             `json:"gateway_node"`
	Limit       int                `json:"limit"`
	Total       int                `json:"total"`
	Events      []adminaudit.Entry `json:"events"`
}

type adminAuditHandler struct {
	gatewayNode   string
	internalToken string
	store         adminaudit.Lister
}

func newAdminAuditHandler(config Config, store adminaudit.Lister) http.Handler {
	return &adminAuditHandler{
		gatewayNode:   config.GatewayNode,
		internalToken: config.InternalToken,
		store:         store,
	}
}

func (h *adminAuditHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAdminAuditJSON(w, http.StatusMethodNotAllowed, adminAuditResponse{
			Code:        "method_not_allowed",
			GatewayNode: h.gatewayNode,
			Limit:       adminaudit.DefaultLimit,
		})
		return
	}
	if h.internalToken != "" && r.Header.Get(downlink.InternalTokenHeader) != h.internalToken {
		writeAdminAuditJSON(w, http.StatusUnauthorized, adminAuditResponse{
			Code:        "unauthorized",
			GatewayNode: h.gatewayNode,
			Limit:       adminaudit.DefaultLimit,
		})
		return
	}
	if h.store == nil {
		writeAdminAuditJSON(w, http.StatusServiceUnavailable, adminAuditResponse{
			Code:        "audit_unavailable",
			Reason:      "admin audit store is not configured",
			GatewayNode: h.gatewayNode,
			Limit:       adminaudit.DefaultLimit,
		})
		return
	}

	result := h.store.List(adminaudit.QueryFromValues(r.URL.Query()))
	writeAdminAuditJSON(w, http.StatusOK, adminAuditResponse{
		Code:        "ok",
		GatewayNode: h.gatewayNode,
		Limit:       result.Limit,
		Total:       result.Total,
		Events:      result.Entries,
	})
}

func writeAdminAuditJSON(w http.ResponseWriter, status int, resp adminAuditResponse) {
	if resp.Events == nil {
		resp.Events = []adminaudit.Entry{}
	}
	data, err := sonic.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
