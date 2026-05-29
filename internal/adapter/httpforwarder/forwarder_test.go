package httpforwarder

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/protocol"
)

func TestForwarderPostsPacketEnvelope(t *testing.T) {
	var got UpstreamRequest
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.String() != "http://backend.local/gateway/upstream" {
			t.Fatalf("url = %s, want http://backend.local/gateway/upstream", r.URL.String())
		}
		if r.Header.Get(InternalTokenHeader) != "secret" {
			t.Fatalf("internal token = %q, want secret", r.Header.Get(InternalTokenHeader))
		}
		if r.Header.Get(TraceIDHeader) != "trace-1" {
			t.Fatalf("trace header = %q, want trace-1", r.Header.Get(TraceIDHeader))
		}

		if err := sonic.ConfigDefault.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request error = %v", err)
		}
		return response(http.StatusAccepted, `{"code":"ok"}`), nil
	})

	forwarder := New(Config{
		URL:   "http://backend.local/gateway/upstream",
		Token: "secret",
		Client: &http.Client{
			Transport: transport,
		},
	})
	packet := protocol.NewPacket(1001, []byte("hello"))
	packet.ClientID = "client-1"
	packet.DeviceID = "device-1"
	packet.SessionID = "session-1"
	packet.MessageID = "message-1"
	packet.TraceID = "trace-1"

	result, err := forwarder.Forward(context.Background(), packet)
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}

	if result.TargetType != TargetType {
		t.Fatalf("TargetType = %q, want %q", result.TargetType, TargetType)
	}
	if result.StatusCode != http.StatusAccepted {
		t.Fatalf("StatusCode = %d, want %d", result.StatusCode, http.StatusAccepted)
	}
	if got.MsgID != packet.MsgID || got.ClientID != packet.ClientID || got.DeviceID != packet.DeviceID {
		t.Fatalf("request identity = msgID:%d client:%q device:%q", got.MsgID, got.ClientID, got.DeviceID)
	}
	if string(got.Body) != "hello" {
		t.Fatalf("Body = %q, want hello", got.Body)
	}
}

func TestForwarderReturnsErrorOnNon2xx(t *testing.T) {
	transport := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusBadGateway, "boom"), nil
	})

	forwarder := New(Config{
		URL: "http://backend.local/gateway/upstream",
		Client: &http.Client{
			Transport: transport,
		},
	})

	result, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, nil))
	if err == nil {
		t.Fatal("Forward() error = nil, want error")
	}
	if result == nil || result.StatusCode != http.StatusBadGateway {
		t.Fatalf("result = %+v, want status %d", result, http.StatusBadGateway)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func response(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Status:     http.StatusText(statusCode),
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
}
