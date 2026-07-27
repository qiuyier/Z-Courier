package httpforwarder

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

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

func TestDiscoveredForwarderUsesRoundRobinSelection(t *testing.T) {
	resolver, err := NewStaticResolver([]string{
		"http://backend-a.local/gateway/upstream",
		"http://backend-b.local/gateway/upstream",
	})
	if err != nil {
		t.Fatalf("NewStaticResolver() error = %v", err)
	}

	var mu sync.Mutex
	var hosts []string
	forwarder, err := NewDiscovered(DiscoveryConfig{
		Resolver:    resolver,
		MaxAttempts: 1,
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			mu.Lock()
			hosts = append(hosts, r.URL.Host)
			mu.Unlock()
			return response(http.StatusNoContent, ""), nil
		})},
	})
	if err != nil {
		t.Fatalf("NewDiscovered() error = %v", err)
	}

	for range 4 {
		if _, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, nil)); err != nil {
			t.Fatalf("Forward() error = %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"backend-a.local", "backend-b.local", "backend-a.local", "backend-b.local"}
	if len(hosts) != len(want) {
		t.Fatalf("hosts = %v, want %v", hosts, want)
	}
	for index := range want {
		if hosts[index] != want[index] {
			t.Fatalf("hosts = %v, want %v", hosts, want)
		}
	}
}

func TestDiscoveredForwarderFailsOverTransportError(t *testing.T) {
	resolver, err := NewStaticResolver([]string{
		"http://backend-a.local/gateway/upstream",
		"http://backend-b.local/gateway/upstream",
	})
	if err != nil {
		t.Fatalf("NewStaticResolver() error = %v", err)
	}

	var hosts []string
	var bodies [][]byte
	forwarder, err := NewDiscovered(DiscoveryConfig{
		Resolver:          resolver,
		MaxAttempts:       2,
		UnhealthyCooldown: time.Minute,
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			hosts = append(hosts, r.URL.Host)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			bodies = append(bodies, body)
			if r.URL.Host == "backend-a.local" {
				return nil, errors.New("connection refused")
			}
			return response(http.StatusAccepted, ""), nil
		})},
	})
	if err != nil {
		t.Fatalf("NewDiscovered() error = %v", err)
	}

	packet := protocol.NewPacket(1001, []byte("hello"))
	packet.MessageID = "message-1"
	result, err := forwarder.Forward(context.Background(), packet)
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	if result == nil || result.StatusCode != http.StatusAccepted {
		t.Fatalf("result = %+v", result)
	}
	want := []string{"backend-a.local", "backend-b.local"}
	if len(hosts) != len(want) || hosts[0] != want[0] || hosts[1] != want[1] {
		t.Fatalf("hosts = %v, want %v", hosts, want)
	}
	if len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) || !bytes.Contains(bodies[0], []byte(`"message_id":"message-1"`)) {
		t.Fatalf("failover bodies do not preserve the same message identity: %q", bodies)
	}

	hosts = nil
	bodies = nil
	if _, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, nil)); err != nil {
		t.Fatalf("Forward() during cooldown error = %v", err)
	}
	if len(hosts) != 1 || hosts[0] != "backend-b.local" {
		t.Fatalf("hosts during cooldown = %v, want backend-b only", hosts)
	}
}

func TestDiscoveredForwarderDoesNotReplayHTTPResponse(t *testing.T) {
	resolver, err := NewStaticResolver([]string{
		"http://backend-a.local/gateway/upstream",
		"http://backend-b.local/gateway/upstream",
	})
	if err != nil {
		t.Fatalf("NewStaticResolver() error = %v", err)
	}

	var requests int
	forwarder, err := NewDiscovered(DiscoveryConfig{
		Resolver:          resolver,
		MaxAttempts:       2,
		UnhealthyCooldown: time.Minute,
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return response(http.StatusInternalServerError, "boom"), nil
		})},
	})
	if err != nil {
		t.Fatalf("NewDiscovered() error = %v", err)
	}

	result, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, nil))
	if err == nil {
		t.Fatal("Forward() error = nil, want HTTP status error")
	}
	if result == nil || result.StatusCode != http.StatusInternalServerError {
		t.Fatalf("result = %+v", result)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestDiscoveredForwarderDoesNotReplayRedirectPolicyError(t *testing.T) {
	resolver, err := NewStaticResolver([]string{
		"http://backend-a.local/gateway/upstream",
		"http://backend-b.local/gateway/upstream",
	})
	if err != nil {
		t.Fatalf("NewStaticResolver() error = %v", err)
	}

	var requests int
	forwarder, err := NewDiscovered(DiscoveryConfig{
		Resolver:          resolver,
		MaxAttempts:       2,
		UnhealthyCooldown: time.Minute,
		Client: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				requests++
				resp := response(http.StatusTemporaryRedirect, "")
				resp.Header.Set("Location", "http://redirected.local/gateway/upstream")
				return resp, nil
			}),
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("redirects disabled")
			},
		},
	})
	if err != nil {
		t.Fatalf("NewDiscovered() error = %v", err)
	}

	result, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, nil))
	if err == nil {
		t.Fatal("Forward() error = nil, want redirect policy error")
	}
	if result == nil || result.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("result = %+v", result)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestDiscoveredForwarderPreservesDNSRequestHost(t *testing.T) {
	resolver, err := NewStaticResolver([]string{"http://127.0.0.1:8080/gateway/upstream"})
	if err != nil {
		t.Fatalf("NewStaticResolver() error = %v", err)
	}

	var urlHost string
	var requestHost string
	forwarder, err := NewDiscovered(DiscoveryConfig{
		Resolver:    resolver,
		MaxAttempts: 1,
		RequestHost: "backend.internal:8080",
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			urlHost = r.URL.Host
			requestHost = r.Host
			return response(http.StatusNoContent, ""), nil
		})},
	})
	if err != nil {
		t.Fatalf("NewDiscovered() error = %v", err)
	}

	if _, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, nil)); err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	if urlHost != "127.0.0.1:8080" || requestHost != "backend.internal:8080" {
		t.Fatalf("URL host = %q, request host = %q", urlHost, requestHost)
	}
}

func TestDiscoveredForwarderConfiguresDNSTLSServerName(t *testing.T) {
	resolver, err := NewStaticResolver([]string{"https://127.0.0.1:8443/gateway/upstream"})
	if err != nil {
		t.Fatalf("NewStaticResolver() error = %v", err)
	}
	forwarder, err := NewDiscovered(DiscoveryConfig{
		Resolver:    resolver,
		MaxAttempts: 1,
		RequestHost: "backend.internal:8443",
		ServerName:  "backend.internal",
	})
	if err != nil {
		t.Fatalf("NewDiscovered() error = %v", err)
	}
	defer forwarder.Close()

	transport, ok := forwarder.client.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil || transport.TLSClientConfig.ServerName != "backend.internal" {
		t.Fatalf("transport TLS config = %+v", transport)
	}
}

func TestDiscoveredForwarderFollowsDNSResolverSnapshotChange(t *testing.T) {
	lookup := &mutableHostLookup{addresses: []string{"127.0.0.1"}}
	resolver := newTestDNSResolver(t, lookup, time.Hour)
	defer resolver.Close()

	var hosts []string
	forwarder, err := NewDiscovered(DiscoveryConfig{
		Resolver:    resolver,
		MaxAttempts: 1,
		RequestHost: "backend.internal:8080",
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			hosts = append(hosts, r.URL.Host)
			return response(http.StatusNoContent, ""), nil
		})},
	})
	if err != nil {
		t.Fatalf("NewDiscovered() error = %v", err)
	}
	if _, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, nil)); err != nil {
		t.Fatalf("Forward() initial error = %v", err)
	}

	lookup.Set([]string{"127.0.0.2"}, nil)
	if err := resolver.refresh(context.Background()); err != nil {
		t.Fatalf("refresh() error = %v", err)
	}
	if _, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, nil)); err != nil {
		t.Fatalf("Forward() refreshed error = %v", err)
	}

	want := []string{"127.0.0.1:8080", "127.0.0.2:8080"}
	if len(hosts) != len(want) || hosts[0] != want[0] || hosts[1] != want[1] {
		t.Fatalf("hosts = %v, want %v", hosts, want)
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
