package downlink

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
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

func TestBatchHandlerPushOK(t *testing.T) {
	conn := &fakeConnection{}
	handler := NewBatchHandler(HandlerConfig{
		Service: NewService(
			fakeSessions{session: &session.Session{SessionID: "s1", ConnID: 1, ClientID: "c1", DeviceID: "d1"}},
			fakeConnections{conn: conn},
		),
		InternalToken: "secret",
		Logger:        zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/push/batch", strings.NewReader(`{
		"messages": [
			{"client_id":"c1","device_id":"d1","msg_id":2001,"message_id":"m1","body":"aGVsbG8="},
			{"client_id":"c1","device_id":"d1","msg_id":2001,"message_id":"m2","body":"d29ybGQ="}
		]
	}`))
	req.Header.Set(InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp BatchPushResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.Code != "ok" || resp.Total != 2 || resp.Success != 2 || resp.Failed != 0 {
		t.Fatalf("response summary = %+v, want ok total=2 success=2 failed=0", resp)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("results length = %d, want 2", len(resp.Results))
	}
	if len(conn.data) == 0 {
		t.Fatal("connection did not receive data")
	}
}

func TestBatchHandlerPartialFailure(t *testing.T) {
	conn := &fakeConnection{}
	handler := NewBatchHandler(HandlerConfig{
		Service: NewService(
			fakeSessions{session: &session.Session{SessionID: "s1", ConnID: 1, ClientID: "c1", DeviceID: "d1"}},
			fakeConnections{conn: conn},
		),
		InternalToken: "secret",
		Logger:        zap.NewNop(),
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/push/batch", strings.NewReader(`{
		"messages": [
			{"client_id":"c1","device_id":"d1","msg_id":2001,"message_id":"m1","body":"aGVsbG8="},
			{"device_id":"d1","msg_id":2001,"message_id":"m2","body":"d29ybGQ="}
		]
	}`))
	req.Header.Set(InternalTokenHeader, "secret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusMultiStatus, rec.Body.String())
	}

	var resp BatchPushResponse
	if err := sonic.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if resp.Code != "partial_failure" || resp.Total != 2 || resp.Success != 1 || resp.Failed != 1 {
		t.Fatalf("response summary = %+v, want partial failure total=2 success=1 failed=1", resp)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("results length = %d, want 2", len(resp.Results))
	}
	if resp.Results[1].Code != "bad_request" {
		t.Fatalf("second result code = %q, want bad_request", resp.Results[1].Code)
	}
}
