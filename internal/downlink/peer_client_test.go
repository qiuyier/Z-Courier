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
	"github.com/qiuyier/Z-Courier/pkg/sdk/signing"
)

var peerHMACTestSecret = []byte("cluster-peer-secret-0123456789abcdef")

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

	dispatcher, err := NewHTTPPeerDispatcher(HTTPPeerDispatcherConfig{
		Token:  "secret",
		Client: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewHTTPPeerDispatcher() error = %v", err)
	}
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

	dispatcher, err := NewHTTPPeerDispatcher(HTTPPeerDispatcherConfig{Client: server.Client()})
	if err != nil {
		t.Fatalf("NewHTTPPeerDispatcher() error = %v", err)
	}
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

func TestHTTPPeerDispatcherSignsExactRequestBody(t *testing.T) {
	verifier, err := signing.NewVerifier(signing.VerifierConfig{
		Keys: map[string][]byte{"gateway-2026-01": peerHMACTestSecret},
	})
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if got := r.Header.Get(InternalTokenHeader); got != "" {
			t.Fatalf("token header = %q, want empty", got)
		}
		if err := verifier.Verify(r, body); err != nil {
			t.Fatalf("Verify() error = %v", err)
		}
		writeJSON(w, http.StatusOK, PeerPushResponse{Code: "ok", DeliveryState: DeliveryStateSent})
	}))
	defer server.Close()

	dispatcher, err := NewHTTPPeerDispatcher(HTTPPeerDispatcherConfig{
		HMAC:   &signing.SignerConfig{KeyID: "gateway-2026-01", Secret: peerHMACTestSecret},
		Client: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewHTTPPeerDispatcher() error = %v", err)
	}
	response, err := dispatcher.Push(context.Background(), cluster.RouteEntry{
		ClientID:     "client-1",
		DeviceID:     "device-1",
		SessionID:    "session-1",
		InternalAddr: server.URL,
	}, PeerPushRequest{OriginNode: "gateway-a", MsgID: 2001, Body: []byte("hello")})
	if err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if response.Code != "ok" {
		t.Fatalf("response = %+v, want ok", response)
	}
}

func TestNewHTTPPeerDispatcherRejectsConflictingAuth(t *testing.T) {
	_, err := NewHTTPPeerDispatcher(HTTPPeerDispatcherConfig{
		Token: "peer-token",
		HMAC:  &signing.SignerConfig{KeyID: "gateway-2026-01", Secret: peerHMACTestSecret},
	})
	if err == nil {
		t.Fatal("NewHTTPPeerDispatcher() error = nil, want conflict")
	}
}
