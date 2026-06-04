package downlink

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qiuyier/Z-Courier/internal/session"
	"go.uber.org/zap"
)

func TestHandlerRejectsMissingInternalToken(t *testing.T) {
	handler := NewHandler(HandlerConfig{
		Service:       NewService(fakeSessions{}, fakeConnections{}),
		InternalToken: "secret",
		Logger:        zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/push", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandlerPushOK(t *testing.T) {
	conn := &fakeConnection{}
	handler := NewHandler(HandlerConfig{
		Service: NewService(
			fakeSessions{session: &session.Session{SessionID: "s1", ConnID: 1, ClientID: "c1", DeviceID: "d1"}},
			fakeConnections{conn: conn},
		),
		InternalToken: "secret",
		Logger:        zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/push", strings.NewReader(`{
		"client_id":"c1",
		"device_id":"d1",
		"msg_id":2001,
		"message_id":"m1",
		"body":"aGVsbG8="
	}`))
	req.Header.Set(InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(conn.data) == 0 {
		t.Fatal("connection did not receive data")
	}
}

func TestHandlerReliablePushQueued(t *testing.T) {
	handler := NewHandler(HandlerConfig{
		Service:       NewService(fakeSessions{}, fakeConnections{}, WithStore(NewMemoryStore())),
		InternalToken: "secret",
		Logger:        zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/push", strings.NewReader(`{
		"client_id":"c1",
		"device_id":"d1",
		"msg_id":2001,
		"message_id":"m1",
		"body":"aGVsbG8="
	}`))
	req.Header.Set(InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"delivery_state":"queued"`) {
		t.Fatalf("body = %s, want queued delivery_state", rec.Body.String())
	}
}
