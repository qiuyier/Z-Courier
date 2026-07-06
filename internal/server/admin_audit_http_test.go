package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/adminaudit"
)

func TestAdminAuditHandlerPaginatesWithCursor(t *testing.T) {
	store := adminaudit.NewStore(adminaudit.StoreConfig{Capacity: 10})
	for _, action := range []string{"first", "second", "third"} {
		store.RecordAdminAudit(adminaudit.Entry{Action: action, Result: "success"})
	}
	handler := newAdminAuditHandler(Config{GatewayNode: "gateway-a"}, store)

	firstReq := httptest.NewRequest(http.MethodGet, adminAuditPath+"?limit=2", nil)
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d, body = %s", firstRec.Code, http.StatusOK, firstRec.Body.String())
	}
	var first adminAuditResponse
	if err := sonic.Unmarshal(firstRec.Body.Bytes(), &first); err != nil {
		t.Fatalf("Unmarshal(first) error = %v", err)
	}
	if first.Total != 3 || first.Limit != 2 || !first.HasMore || first.NextCursor == "" || len(first.Events) != 2 {
		t.Fatalf("first response = %+v, want first page with next cursor", first)
	}
	if first.Events[0].Action != "third" || first.Events[1].Action != "second" {
		t.Fatalf("first events = %+v, want third/second", first.Events)
	}

	secondReq := httptest.NewRequest(http.MethodGet, adminAuditPath+"?limit=2&cursor="+first.NextCursor, nil)
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d, body = %s", secondRec.Code, http.StatusOK, secondRec.Body.String())
	}
	var second adminAuditResponse
	if err := sonic.Unmarshal(secondRec.Body.Bytes(), &second); err != nil {
		t.Fatalf("Unmarshal(second) error = %v", err)
	}
	if second.Total != 3 || second.Cursor != first.NextCursor || second.HasMore || second.NextCursor != "" || len(second.Events) != 1 {
		t.Fatalf("second response = %+v, want final page", second)
	}
	if second.Events[0].Action != "first" {
		t.Fatalf("second event = %+v, want first", second.Events[0])
	}
}
