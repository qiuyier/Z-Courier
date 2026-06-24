package downlink

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
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
