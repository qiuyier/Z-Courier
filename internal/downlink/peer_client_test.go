package downlink

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/cluster"
)

func TestHTTPPeerDispatcherPushOK(t *testing.T) {
	var gotReq PeerPushRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PeerPushPath {
			t.Fatalf("path = %q, want %q", r.URL.Path, PeerPushPath)
		}
		if r.Header.Get(InternalTokenHeader) != "secret" {
			t.Fatalf("token header = %q, want secret", r.Header.Get(InternalTokenHeader))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if err := sonic.Unmarshal(body, &gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		writeJSON(w, http.StatusOK, PeerPushResponse{
			Code:          "ok",
			DeliveryState: DeliveryStateSent,
			GatewayNode:   "gateway-a",
			SessionID:     gotReq.SessionID,
			MessageID:     gotReq.MessageID,
		})
	}))
	defer server.Close()

	dispatcher := NewHTTPPeerDispatcher(HTTPPeerDispatcherConfig{
		Token:  "secret",
		Client: server.Client(),
	})
	resp, err := dispatcher.Push(context.Background(), cluster.RouteEntry{
		ClientID:     "client-1",
		DeviceID:     "device-1",
		SessionID:    "session-1",
		InternalAddr: server.URL,
	}, PeerPushRequest{
		OriginNode: "gateway-b",
		MsgID:      2001,
		MessageID:  "message-1",
		Body:       []byte("hello"),
	})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if resp.Code != "ok" || resp.SessionID != "session-1" {
		t.Fatalf("response = %+v", resp)
	}
	if gotReq.ClientID != "client-1" || gotReq.DeviceID != "device-1" || gotReq.SessionID != "session-1" {
		t.Fatalf("request identity = %+v", gotReq)
	}
}

func TestHTTPPeerDispatcherReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, PeerPushResponse{
			Code:   "session_mismatch",
			Reason: "downlink: session mismatch",
		})
	}))
	defer server.Close()

	dispatcher := NewHTTPPeerDispatcher(HTTPPeerDispatcherConfig{Client: server.Client()})
	resp, err := dispatcher.Push(context.Background(), cluster.RouteEntry{
		ClientID:     "client-1",
		DeviceID:     "device-1",
		SessionID:    "session-1",
		InternalAddr: server.URL,
	}, PeerPushRequest{MsgID: 2001})
	if err == nil {
		t.Fatal("Push() error = nil, want error")
	}
	if resp == nil || resp.Code != "session_mismatch" {
		t.Fatalf("response = %+v, want session_mismatch", resp)
	}

	var httpErr *PeerPushHTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("Push() error = %T, want PeerPushHTTPError", err)
	}
	if httpErr.StatusCode != http.StatusNotFound || httpErr.Code != "session_mismatch" {
		t.Fatalf("http error = %+v", httpErr)
	}
}
