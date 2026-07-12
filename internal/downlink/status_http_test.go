package downlink

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"
)

func TestStatusHandlerGetOK(t *testing.T) {
	now := time.Unix(1780000000, 0).UTC()
	store := NewMemoryStore()
	if _, err := store.Save(context.Background(), Message{
		MessageID:   "message-1",
		ClientID:    "client-1",
		DeviceID:    "device-1",
		MsgID:       2001,
		TraceID:     "trace-1",
		SessionID:   "session-1",
		Status:      MessageStatusSent,
		Attempts:    1,
		SentAt:      now,
		CreatedAt:   now,
		UpdatedAt:   now,
		AckRequired: true,
		Body:        []byte("hello"),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	handler := NewStatusHandler(HandlerConfig{
		Service:       NewService(fakeSessions{}, fakeConnections{}, WithStore(store)),
		InternalToken: "secret",
		Logger:        zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodGet, "/internal/message/status?message_id=message-1", nil)
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
	if resp.Code != "ok" || resp.MessageID != "message-1" || resp.Status != MessageStatusSent {
		t.Fatalf("response = %+v, want message-1 sent", resp)
	}
	if resp.PolicyName != DefaultDeliveryPolicyName {
		t.Fatalf("PolicyName = %q, want %q", resp.PolicyName, DefaultDeliveryPolicyName)
	}
	if resp.BodySizeBytes != len("hello") {
		t.Fatalf("BodySizeBytes = %d, want %d", resp.BodySizeBytes, len("hello"))
	}
	if resp.SentAt == nil {
		t.Fatal("SentAt = nil, want timestamp")
	}
}

func TestResponseFromMessageIncludesTerminalPublicationState(t *testing.T) {
	now := time.Unix(1780000000, 0).UTC()
	response := responseFromMessage(Message{
		MessageID:               "message-1",
		Status:                  MessageStatusFailed,
		TerminalReason:          TerminalReasonMaxAttempts,
		TerminalAt:              now,
		TerminalPublishStatus:   TerminalPublicationFailed,
		TerminalPublishAttempts: 2,
		TerminalNextPublishAt:   now.Add(time.Minute),
		TerminalPublishError:    "nsq unavailable",
	})
	if response.TerminalReason != TerminalReasonMaxAttempts ||
		response.TerminalPublishStatus != string(TerminalPublicationFailed) ||
		response.TerminalPublishAttempts != 2 || response.TerminalAt == nil ||
		response.TerminalNextPublishAt == nil || response.TerminalPublishError != "nsq unavailable" {
		t.Fatalf("terminal response = %+v", response)
	}
}

func TestStatusHandlerPostOK(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.Save(context.Background(), Message{
		MessageID: "message-1",
		ClientID:  "client-1",
		DeviceID:  "device-1",
		MsgID:     2001,
		Status:    MessageStatusPending,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	handler := NewStatusHandler(HandlerConfig{
		Service:       NewService(fakeSessions{}, fakeConnections{}, WithStore(store)),
		InternalToken: "secret",
		Logger:        zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/message/status", strings.NewReader(`{"message_id":"message-1"}`))
	req.Header.Set(InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestStatusHandlerRejectsMissingMessageID(t *testing.T) {
	handler := NewStatusHandler(HandlerConfig{
		Service:       NewService(fakeSessions{}, fakeConnections{}, WithStore(NewMemoryStore())),
		InternalToken: "secret",
		Logger:        zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodGet, "/internal/message/status", nil)
	req.Header.Set(InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestStatusHandlerMessageNotFound(t *testing.T) {
	handler := NewStatusHandler(HandlerConfig{
		Service:       NewService(fakeSessions{}, fakeConnections{}, WithStore(NewMemoryStore())),
		InternalToken: "secret",
		Logger:        zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodGet, "/internal/message/status?message_id=missing", nil)
	req.Header.Set(InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"message_not_found"`) {
		t.Fatalf("body = %s, want message_not_found", rec.Body.String())
	}
}
