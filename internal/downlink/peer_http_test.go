package downlink

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qiuyier/Z-Courier/internal/session"
	"go.uber.org/zap"
)

func TestPeerHandlerRejectsMissingPeerToken(t *testing.T) {
	handler := NewPeerHandler(PeerHandlerConfig{
		Service:     NewService(fakeSessions{}, fakeConnections{}),
		GatewayNode: "gateway-a",
		PeerToken:   "secret",
		Logger:      zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, PeerPushPath, strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestPeerHandlerPushOK(t *testing.T) {
	conn := &fakeConnection{}
	handler := NewPeerHandler(PeerHandlerConfig{
		Service: NewService(
			fakeSessions{session: &session.Session{SessionID: "s1", ConnID: 1, ClientID: "c1", DeviceID: "d1"}},
			fakeConnections{conn: conn},
		),
		GatewayNode: "gateway-a",
		PeerToken:   "secret",
		Logger:      zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, PeerPushPath, strings.NewReader(`{
		"origin_node":"gateway-b",
		"client_id":"c1",
		"device_id":"d1",
		"session_id":"s1",
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
	if !strings.Contains(rec.Body.String(), `"gateway_node":"gateway-a"`) {
		t.Fatalf("body = %s, want gateway node", rec.Body.String())
	}
}

func TestPeerHandlerRejectsSessionMismatch(t *testing.T) {
	handler := NewPeerHandler(PeerHandlerConfig{
		Service: NewService(
			fakeSessions{session: &session.Session{SessionID: "new-session", ConnID: 1, ClientID: "c1", DeviceID: "d1"}},
			fakeConnections{conn: &fakeConnection{}},
		),
		GatewayNode: "gateway-a",
		Logger:      zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, PeerPushPath, strings.NewReader(`{
		"client_id":"c1",
		"device_id":"d1",
		"session_id":"old-session",
		"msg_id":2001
	}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"session_mismatch"`) {
		t.Fatalf("body = %s, want session_mismatch", rec.Body.String())
	}
}
