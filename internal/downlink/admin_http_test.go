package downlink

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/adminaudit"
	"github.com/qiuyier/Z-Courier/internal/httpauth"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestMessageListHandlerOK(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.Save(context.Background(), Message{
		MessageID: "failed-1",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		Status:    MessageStatusFailed,
	}); err != nil {
		t.Fatalf("Save failed error = %v", err)
	}
	if _, err := store.Save(context.Background(), Message{
		MessageID: "pending-1",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		Status:    MessageStatusPending,
	}); err != nil {
		t.Fatalf("Save pending error = %v", err)
	}

	handler := NewMessageListHandler(HandlerConfig{
		Service:       NewService(fakeSessions{}, fakeConnections{}, WithStore(store)),
		InternalToken: "secret",
		Logger:        zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodGet, "/internal/messages?status=failed&limit=10", nil)
	req.Header.Set(InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp ListMessagesResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.Code != "ok" || resp.Status != MessageStatusFailed || resp.Total != 1 {
		t.Fatalf("response = %+v, want one failed message", resp)
	}
	if len(resp.Messages) != 1 || resp.Messages[0].MessageID != "failed-1" {
		t.Fatalf("messages = %+v, want failed-1", resp.Messages)
	}
}

func TestMessageListHandlerPaginatesWithCursor(t *testing.T) {
	store := NewMemoryStore()
	now := time.UnixMilli(1760000000000)
	for _, message := range []Message{
		{MessageID: "failed-1", ClientID: "client-1", DeviceID: "device-1", MsgID: 2001, Status: MessageStatusFailed, UpdatedAt: now.Add(3 * time.Second)},
		{MessageID: "failed-2", ClientID: "client-1", DeviceID: "device-1", MsgID: 2001, Status: MessageStatusFailed, UpdatedAt: now.Add(2 * time.Second)},
		{MessageID: "failed-3", ClientID: "client-1", DeviceID: "device-1", MsgID: 2001, Status: MessageStatusFailed, UpdatedAt: now.Add(time.Second)},
	} {
		if _, err := store.Save(context.Background(), message); err != nil {
			t.Fatalf("Save(%s) error = %v", message.MessageID, err)
		}
	}

	handler := NewMessageListHandler(HandlerConfig{
		Service:       NewService(fakeSessions{}, fakeConnections{}, WithStore(store)),
		InternalToken: "secret",
		Logger:        zap.NewNop(),
	})

	firstReq := httptest.NewRequest(http.MethodGet, "/internal/messages?status=failed&limit=2", nil)
	firstReq.Header.Set(InternalTokenHeader, "secret")
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d, body = %s", firstRec.Code, http.StatusOK, firstRec.Body.String())
	}
	var first ListMessagesResponse
	if err := sonic.Unmarshal(firstRec.Body.Bytes(), &first); err != nil {
		t.Fatalf("Unmarshal(first) error = %v", err)
	}
	if first.Total != 3 || !first.HasMore || first.NextCursor == "" || len(first.Messages) != 2 {
		t.Fatalf("first response = %+v, want total=3 has_more with two messages", first)
	}
	if first.Messages[0].MessageID != "failed-1" || first.Messages[1].MessageID != "failed-2" {
		t.Fatalf("first messages = %+v, want failed-1 then failed-2", first.Messages)
	}

	secondReq := httptest.NewRequest(http.MethodGet, "/internal/messages?status=failed&limit=2&cursor="+url.QueryEscape(first.NextCursor), nil)
	secondReq.Header.Set(InternalTokenHeader, "secret")
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d, body = %s", secondRec.Code, http.StatusOK, secondRec.Body.String())
	}
	var second ListMessagesResponse
	if err := sonic.Unmarshal(secondRec.Body.Bytes(), &second); err != nil {
		t.Fatalf("Unmarshal(second) error = %v", err)
	}
	if second.Total != 3 || second.Cursor != first.NextCursor || second.HasMore || second.NextCursor != "" || len(second.Messages) != 1 {
		t.Fatalf("second response = %+v, want final page", second)
	}
	if second.Messages[0].MessageID != "failed-3" {
		t.Fatalf("second messages = %+v, want failed-3", second.Messages)
	}
}

func TestMessageListHandlerRejectsInvalidStatus(t *testing.T) {
	handler := NewMessageListHandler(HandlerConfig{
		Service:       NewService(fakeSessions{}, fakeConnections{}, WithStore(NewMemoryStore())),
		InternalToken: "secret",
		Logger:        zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodGet, "/internal/messages?status=nope", nil)
	req.Header.Set(InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestRequeueHandlerOK(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.Save(context.Background(), Message{
		MessageID: "message-1",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		Status:    MessageStatusFailed,
		Attempts:  5,
	}); err != nil {
		t.Fatalf("Save error = %v", err)
	}

	handler := NewRequeueHandler(HandlerConfig{
		Service:       NewService(fakeSessions{}, fakeConnections{}, WithStore(store)),
		InternalToken: "secret",
		Logger:        zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/message/requeue", strings.NewReader(`{"message_id":"message-1"}`))
	req.Header.Set(InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp MessageStatusResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.Status != MessageStatusPending || resp.Attempts != 0 {
		t.Fatalf("response = %+v, want pending attempts reset", resp)
	}
}

func TestRequeueHandlerRejectsQueueCapacity(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()
	for _, message := range []Message{
		{
			MessageID: "pending-1", ClientID: "client-1", DeviceID: "device-1", MsgID: 2001,
			Status: MessageStatusPending, CreatedAt: now, UpdatedAt: now,
		},
		{
			MessageID: "failed-1", ClientID: "client-1", DeviceID: "device-1", MsgID: 2001,
			Status: MessageStatusFailed, CreatedAt: now, UpdatedAt: now,
		},
	} {
		if _, err := store.Save(context.Background(), message); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}
	service := NewService(fakeSessions{}, fakeConnections{},
		WithStore(store),
		WithQueueCapacity(QueueCapacity{MaxPendingPerDevice: 1}),
	)
	handler := NewRequeueHandler(HandlerConfig{Service: service})
	req := httptest.NewRequest(http.MethodPost, "/internal/message/requeue", strings.NewReader(`{"message_id":"failed-1"}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response MessageStatusResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if response.Code != "queue_capacity_exceeded" || response.CapacityScope != QueueCapacityScopeDevice ||
		response.CapacityLimit != 1 || response.CapacityPending != 1 {
		t.Fatalf("response = %+v", response)
	}
}

func TestBulkRequeueHandlerOKAndAuditsEachItem(t *testing.T) {
	store := NewMemoryStore()
	for _, messageID := range []string{"failed-1", "failed-2"} {
		if _, err := store.Save(context.Background(), Message{
			MessageID: messageID,
			ClientID:  "client-1",
			DeviceID:  "device-1",
			MsgID:     2001,
			Status:    MessageStatusFailed,
			Attempts:  5,
		}); err != nil {
			t.Fatalf("Save(%s) error = %v", messageID, err)
		}
	}

	core, logs := observer.New(zap.InfoLevel)
	audit := adminaudit.NewStore(adminaudit.StoreConfig{Capacity: 10})
	handler := NewBulkRequeueHandler(HandlerConfig{
		Service:       NewService(fakeSessions{}, fakeConnections{}, WithStore(store)),
		InternalToken: "secret",
		GatewayNode:   "gateway-a",
		Logger:        zap.New(core),
		Audit:         audit,
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/messages/requeue", strings.NewReader(`{"message_ids":["failed-1","failed-2"]}`))
	req.Header.Set(InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response BulkRequeueResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if response.Code != "ok" || response.Total != 2 || response.Success != 2 || response.Failed != 0 || len(response.Results) != 2 {
		t.Fatalf("response = %+v", response)
	}
	for index, result := range response.Results {
		if result.Code != "ok" || result.Status != MessageStatusPending || result.Attempts != 0 {
			t.Fatalf("results[%d] = %+v, want pending success", index, result)
		}
	}
	if got := logs.FilterMessage("admin message action audit").Len(); got != 2 {
		t.Fatalf("item audit logs = %d, want 2", got)
	}
	summaries := logs.FilterMessage("admin bulk requeue audit").All()
	if len(summaries) != 1 {
		t.Fatalf("summary audit logs = %d, want 1", len(summaries))
	}
	fields := summaries[0].ContextMap()
	if fields["audit_event"] != "downlink_message_bulk_requeue" || fields["result"] != "success" ||
		fields["total"] != int64(2) || fields["success"] != int64(2) || fields["failed"] != int64(0) {
		t.Fatalf("summary fields = %#v", fields)
	}
	auditResult := audit.List(adminaudit.Query{Limit: 10})
	if auditResult.Total != 3 || len(auditResult.Entries) != 3 {
		t.Fatalf("audit entries = %+v, want summary plus two items", auditResult)
	}
	if summary := auditResult.Entries[0]; summary.Action != "downlink_message_bulk_requeue" || summary.Result != "success" ||
		summary.Details["total"] != "2" || summary.Details["success"] != "2" || summary.Details["failed"] != "0" {
		t.Fatalf("summary audit = %+v", summary)
	}
	for _, entry := range auditResult.Entries[1:] {
		if entry.Action != "downlink_message_action" || entry.Details["batch"] != "true" {
			t.Fatalf("item audit = %+v", entry)
		}
	}
}

func TestBulkRequeueHandlerReportsCapacityPartialFailure(t *testing.T) {
	store := NewMemoryStore()
	for _, messageID := range []string{"failed-1", "failed-2"} {
		if _, err := store.Save(context.Background(), Message{
			MessageID: messageID,
			ClientID:  "client-1",
			DeviceID:  "device-1",
			MsgID:     2001,
			Status:    MessageStatusFailed,
		}); err != nil {
			t.Fatalf("Save(%s) error = %v", messageID, err)
		}
	}
	handler := NewBulkRequeueHandler(HandlerConfig{Service: NewService(
		fakeSessions{},
		fakeConnections{},
		WithStore(store),
		WithQueueCapacity(QueueCapacity{MaxPendingPerDevice: 1}),
	)})
	req := httptest.NewRequest(http.MethodPost, "/internal/messages/requeue", strings.NewReader(`{"message_ids":["failed-1","failed-2"]}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusMultiStatus, rec.Body.String())
	}
	var response BulkRequeueResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if response.Code != "partial_failure" || response.Success != 1 || response.Failed != 1 || len(response.Results) != 2 {
		t.Fatalf("response = %+v", response)
	}
	if response.Results[0].Code != "ok" || response.Results[0].Status != MessageStatusPending {
		t.Fatalf("results[0] = %+v, want success", response.Results[0])
	}
	failed := response.Results[1]
	if failed.Code != "queue_capacity_exceeded" || failed.Status != MessageStatusFailed ||
		failed.CapacityScope != QueueCapacityScopeDevice || failed.CapacityLimit != 1 || failed.CapacityPending != 1 {
		t.Fatalf("results[1] = %+v, want capacity failure", failed)
	}
}

func TestBulkRequeueHandlerOnlyAcceptsFailedMessages(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.Save(context.Background(), Message{
		MessageID: "pending-1",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		Status:    MessageStatusPending,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	handler := NewBulkRequeueHandler(HandlerConfig{Service: NewService(fakeSessions{}, fakeConnections{}, WithStore(store))})
	req := httptest.NewRequest(http.MethodPost, "/internal/messages/requeue", strings.NewReader(`{"message_ids":["pending-1"]}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusMultiStatus, rec.Body.String())
	}
	var response BulkRequeueResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if response.Code != "failed" || response.Success != 0 || response.Failed != 1 ||
		len(response.Results) != 1 || response.Results[0].Code != "invalid_transition" || response.Results[0].Status != MessageStatusPending {
		t.Fatalf("response = %+v", response)
	}
}

func TestBulkRequeueHandlerValidatesSelection(t *testing.T) {
	handler := NewBulkRequeueHandler(HandlerConfig{Service: NewService(fakeSessions{}, fakeConnections{}, WithStore(NewMemoryStore()))})
	tooMany := make([]string, MaxBulkRequeueMessages+1)
	for index := range tooMany {
		tooMany[index] = fmt.Sprintf("message-%d", index)
	}
	tooManyBody, err := sonic.Marshal(BulkRequeueRequest{MessageIDs: tooMany})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	tests := []struct {
		name string
		body string
	}{
		{name: "empty", body: `{"message_ids":[]}`},
		{name: "blank", body: `{"message_ids":[" "]}`},
		{name: "duplicate", body: `{"message_ids":["message-1"," message-1 "]}`},
		{name: "too many", body: string(tooManyBody)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/internal/messages/requeue", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
		})
	}
}

func TestDiscardHandlerOK(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.Save(context.Background(), Message{
		MessageID: "message-1",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		Status:    MessageStatusFailed,
	}); err != nil {
		t.Fatalf("Save error = %v", err)
	}

	handler := NewDiscardHandler(HandlerConfig{
		Service:       NewService(fakeSessions{}, fakeConnections{}, WithStore(store)),
		InternalToken: "secret",
		Logger:        zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/message/discard", strings.NewReader(`{"message_id":"message-1","reason":"manual"}`))
	req.Header.Set(InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp MessageStatusResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.Status != MessageStatusDiscarded || resp.LastError != "manual" {
		t.Fatalf("response = %+v, want discarded with reason", resp)
	}
}

func TestRetryScanHandlerOK(t *testing.T) {
	now := time.Now().Add(-time.Minute)
	store := NewMemoryStore()
	if _, err := store.Save(context.Background(), Message{
		MessageID:   "message-1",
		ClientID:    "client-1",
		DeviceID:    "device-1",
		MsgID:       2001,
		Status:      MessageStatusPending,
		NextRetryAt: now,
	}); err != nil {
		t.Fatalf("Save error = %v", err)
	}

	core, logs := observer.New(zap.InfoLevel)
	handler := NewRetryScanHandler(HandlerConfig{
		Service:        NewService(fakeSessions{}, fakeConnections{}, WithStore(store)),
		InternalToken:  "secret",
		RetryScanLimit: 25,
		GatewayNode:    "gateway-a",
		Logger:         zap.New(core),
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/messages/retry/scan", strings.NewReader(`{}`))
	req.Header.Set(InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp RetryScanResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.Code != "ok" || resp.Limit != 25 || resp.Scanned != 1 || resp.Sent != 0 || resp.Queued != 1 || resp.Failed != 0 ||
		resp.SelectionMode != RetrySelectionModeFIFO || resp.SelectedDevices != 1 || resp.MaxPerDevice != 1 {
		t.Fatalf("response = %+v, want one queued retry", resp)
	}

	entry := onlyRetryScanAuditLog(t, logs)
	if entry.Level != zap.InfoLevel {
		t.Fatalf("level = %s, want info", entry.Level)
	}
	fields := entry.ContextMap()
	if fields["audit_event"] != "admin_retry_scan" ||
		fields["result"] != "success" ||
		fields["http_status"] != int64(http.StatusOK) ||
		fields["auth_mode"] != httpauth.ModeToken ||
		fields["gateway_node"] != "gateway-a" ||
		fields["limit"] != int64(25) ||
		fields["scanned"] != int64(1) ||
		fields["queued"] != int64(1) ||
		fields["selection_mode"] != RetrySelectionModeFIFO ||
		fields["selected_devices"] != int64(1) ||
		fields["max_per_device"] != int64(1) {
		t.Fatalf("audit fields = %#v", fields)
	}
}

func TestRetryScanHandlerRejectsInvalidLimit(t *testing.T) {
	handler := NewRetryScanHandler(HandlerConfig{
		Service:       NewService(fakeSessions{}, fakeConnections{}, WithStore(NewMemoryStore())),
		InternalToken: "secret",
		Logger:        zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/messages/retry/scan", strings.NewReader(`{"limit":-1}`))
	req.Header.Set(InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestDiscardHandlerAuditsSuccessWithTokenIdentity(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.Save(context.Background(), Message{
		MessageID: "message-1",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		Status:    MessageStatusFailed,
	}); err != nil {
		t.Fatalf("Save error = %v", err)
	}

	core, logs := observer.New(zap.InfoLevel)
	handler := NewDiscardHandler(HandlerConfig{
		Service:       NewService(fakeSessions{}, fakeConnections{}, WithStore(store)),
		InternalToken: "secret",
		GatewayNode:   "gateway-a",
		Logger:        zap.New(core),
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/message/discard", strings.NewReader(`{"message_id":"message-1","reason":"manual"}`))
	req.Header.Set(InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	entry := onlyAuditLog(t, logs)
	if entry.Level != zap.InfoLevel {
		t.Fatalf("level = %s, want info", entry.Level)
	}
	fields := entry.ContextMap()
	if fields["audit_event"] != "downlink_message_action" ||
		fields["action"] != "discard" ||
		fields["result"] != "success" ||
		fields["http_status"] != int64(http.StatusOK) ||
		fields["auth_mode"] != httpauth.ModeToken ||
		fields["gateway_node"] != "gateway-a" ||
		fields["message_id"] != "message-1" ||
		fields["reason"] != "manual" ||
		fields["message_status"] != string(MessageStatusDiscarded) {
		t.Fatalf("audit fields = %#v", fields)
	}
}

func TestRequeueHandlerAuditsFailureWithHMACIdentity(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.Save(context.Background(), Message{
		MessageID: "message-1",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		Status:    MessageStatusDelivered,
	}); err != nil {
		t.Fatalf("Save error = %v", err)
	}

	core, logs := observer.New(zap.InfoLevel)
	handler := NewRequeueHandler(HandlerConfig{
		Service:     NewService(fakeSessions{}, fakeConnections{}, WithStore(store)),
		GatewayNode: "gateway-b",
		Logger:      zap.New(core),
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/message/requeue", strings.NewReader(`{"message_id":"message-1"}`))
	req = req.WithContext(httpauth.WithIdentity(req.Context(), httpauth.Identity{Mode: httpauth.ModeHMAC, KeyID: "backend-1"}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	entry := onlyAuditLog(t, logs)
	if entry.Level != zap.WarnLevel {
		t.Fatalf("level = %s, want warn", entry.Level)
	}
	fields := entry.ContextMap()
	if fields["audit_event"] != "downlink_message_action" ||
		fields["action"] != "requeue" ||
		fields["result"] != "invalid_transition" ||
		fields["http_status"] != int64(http.StatusConflict) ||
		fields["auth_mode"] != httpauth.ModeHMAC ||
		fields["auth_key_id"] != "backend-1" ||
		fields["gateway_node"] != "gateway-b" ||
		fields["message_id"] != "message-1" {
		t.Fatalf("audit fields = %#v", fields)
	}
	if _, ok := fields["error"]; !ok {
		t.Fatalf("audit fields = %#v, want error", fields)
	}
}

func TestRequeueHandlerRejectsDelivered(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.Save(context.Background(), Message{
		MessageID: "message-1",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		Status:    MessageStatusDelivered,
	}); err != nil {
		t.Fatalf("Save error = %v", err)
	}

	handler := NewRequeueHandler(HandlerConfig{
		Service:       NewService(fakeSessions{}, fakeConnections{}, WithStore(store)),
		InternalToken: "secret",
		Logger:        zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/message/requeue", strings.NewReader(`{"message_id":"message-1"}`))
	req.Header.Set(InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"invalid_transition"`) {
		t.Fatalf("body = %s, want invalid_transition", rec.Body.String())
	}
}

func onlyAuditLog(t *testing.T, logs *observer.ObservedLogs) observer.LoggedEntry {
	t.Helper()

	entries := logs.FilterMessage("admin message action audit").All()
	if len(entries) != 1 {
		t.Fatalf("audit log entries = %d, want 1: %+v", len(entries), entries)
	}
	return entries[0]
}

func onlyRetryScanAuditLog(t *testing.T, logs *observer.ObservedLogs) observer.LoggedEntry {
	t.Helper()

	entries := logs.FilterMessage("admin retry scan audit").All()
	if len(entries) != 1 {
		t.Fatalf("audit log entries = %d, want 1: %+v", len(entries), entries)
	}
	return entries[0]
}
