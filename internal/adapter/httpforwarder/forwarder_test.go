package httpforwarder

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/router"
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
	if result.Endpoint != "http://backend.local/gateway/upstream" || result.Attempts != 1 || result.MaxAttempts != 1 {
		t.Fatalf("forward metadata = %+v", result)
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
	forwardErr := requireForwardError(t, err)
	if forwardErr.Class != router.FailureClassResponse || forwardErr.Retryable ||
		forwardErr.Decision != router.FailoverDecisionNotRetryable {
		t.Fatalf("forward error = %+v", forwardErr)
	}
	if strings.Contains(err.Error(), "boom") || strings.Contains(err.Error(), "backend.local") {
		t.Fatalf("error exposes backend response or endpoint: %q", err)
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
	if result.Endpoint != "http://backend-b.local/gateway/upstream" || result.Attempts != 2 || result.MaxAttempts != 2 {
		t.Fatalf("failover metadata = %+v", result)
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
	forwardErr := requireForwardError(t, err)
	if forwardErr.Class != router.FailureClassResponse || forwardErr.Retryable ||
		forwardErr.Decision != router.FailoverDecisionNotRetryable || forwardErr.Attempts != 1 {
		t.Fatalf("forward error = %+v", forwardErr)
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
	forwardErr := requireForwardError(t, err)
	if forwardErr.Class != router.FailureClassResponse || forwardErr.Retryable ||
		forwardErr.Decision != router.FailoverDecisionNotRetryable {
		t.Fatalf("forward error = %+v", forwardErr)
	}
}

func TestForwarderKeepsSingleURLTransportFailureAtOneAttempt(t *testing.T) {
	var requests int
	cause := errors.New("connection refused")
	forwarder := New(Config{
		URL: "http://backend.local/gateway/upstream?token=secret",
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return nil, cause
		})},
	})

	result, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, nil))
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	forwardErr := requireForwardError(t, err)
	if forwardErr.Class != router.FailureClassTransport || !forwardErr.Retryable ||
		forwardErr.Decision != router.FailoverDecisionDisabled {
		t.Fatalf("forward error = %+v", forwardErr)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want wrapped cause", err)
	}
	if result.Endpoint != "http://backend.local/gateway/upstream" ||
		forwardErr.Endpoint != result.Endpoint || strings.Contains(forwardErr.Endpoint, "secret") {
		t.Fatalf("sanitized metadata: result=%+v error=%+v", result, forwardErr)
	}
}

func TestDiscoveredForwarderClassifiesExhaustedTransportFailure(t *testing.T) {
	resolver, err := NewStaticResolver([]string{
		"http://backend-a.local/gateway/upstream",
		"http://backend-b.local/gateway/upstream?token=secret",
	})
	if err != nil {
		t.Fatalf("NewStaticResolver() error = %v", err)
	}

	var requests int
	lastCause := errors.New("second connection refused")
	forwarder, err := NewDiscovered(DiscoveryConfig{
		Resolver:          resolver,
		MaxAttempts:       2,
		UnhealthyCooldown: time.Minute,
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests++
			if r.URL.Host == "backend-b.local" {
				return nil, lastCause
			}
			return nil, errors.New("first connection refused")
		})},
	})
	if err != nil {
		t.Fatalf("NewDiscovered() error = %v", err)
	}

	result, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, nil))
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
	forwardErr := requireForwardError(t, err)
	if forwardErr.Class != router.FailureClassTransport || !forwardErr.Retryable ||
		forwardErr.Decision != router.FailoverDecisionExhausted ||
		forwardErr.Attempts != 2 || forwardErr.MaxAttempts != 2 {
		t.Fatalf("forward error = %+v", forwardErr)
	}
	if !errors.Is(err, lastCause) {
		t.Fatalf("error = %v, want final cause", err)
	}
	if result.Endpoint != "http://backend-b.local/gateway/upstream" ||
		forwardErr.Endpoint != result.Endpoint || strings.Contains(forwardErr.Endpoint, "secret") {
		t.Fatalf("sanitized metadata: result=%+v error=%+v", result, forwardErr)
	}
}

func TestDiscoveredForwarderClassifiesMissingAlternateEndpoint(t *testing.T) {
	resolver, err := NewStaticResolver([]string{"http://backend-a.local/gateway/upstream"})
	if err != nil {
		t.Fatalf("NewStaticResolver() error = %v", err)
	}
	forwarder, err := NewDiscovered(DiscoveryConfig{
		Resolver:    resolver,
		MaxAttempts: 2,
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("connection refused")
		})},
	})
	if err != nil {
		t.Fatalf("NewDiscovered() error = %v", err)
	}

	result, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, nil))
	forwardErr := requireForwardError(t, err)
	if forwardErr.Class != router.FailureClassTransport || !forwardErr.Retryable ||
		forwardErr.Decision != router.FailoverDecisionNoAlternate ||
		forwardErr.Attempts != 1 || result.Attempts != 1 {
		t.Fatalf("forward result=%+v error=%+v", result, forwardErr)
	}
}

func TestDiscoveredForwarderClassifiesUnavailableDiscovery(t *testing.T) {
	forwarder, err := NewDiscovered(DiscoveryConfig{
		Resolver:    stubResolver{err: ErrNoAvailableEndpoint},
		MaxAttempts: 2,
	})
	if err != nil {
		t.Fatalf("NewDiscovered() error = %v", err)
	}

	result, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, nil))
	forwardErr := requireForwardError(t, err)
	if forwardErr.Class != router.FailureClassDiscovery || forwardErr.Retryable ||
		forwardErr.Decision != router.FailoverDecisionNoAlternate ||
		forwardErr.Attempts != 0 || forwardErr.MaxAttempts != 2 ||
		result.Attempts != 0 || result.MaxAttempts != 2 {
		t.Fatalf("forward result=%+v error=%+v", result, forwardErr)
	}
}

func TestDiscoveredForwarderDoesNotRetryInvalidRequestEndpoint(t *testing.T) {
	forwarder, err := NewDiscovered(DiscoveryConfig{
		Resolver: stubResolver{snapshot: EndpointSnapshot{Endpoints: []string{
			"://invalid",
			"http://backend-b.local/gateway/upstream",
		}}},
		MaxAttempts: 2,
	})
	if err != nil {
		t.Fatalf("NewDiscovered() error = %v", err)
	}

	result, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, nil))
	forwardErr := requireForwardError(t, err)
	if forwardErr.Class != router.FailureClassRequest || forwardErr.Retryable ||
		forwardErr.Decision != router.FailoverDecisionNotRetryable ||
		forwardErr.Attempts != 1 || result.Attempts != 1 {
		t.Fatalf("forward result=%+v error=%+v", result, forwardErr)
	}
}

func TestDiscoveredForwarderFailsOverClientTimeout(t *testing.T) {
	slowStarted := make(chan struct{})
	slowStopped := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		close(slowStarted)
		<-r.Context().Done()
		close(slowStopped)
	}))
	defer slow.Close()
	defer slow.CloseClientConnections()
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer healthy.Close()

	resolver, err := NewStaticResolver([]string{
		slow.URL,
		healthy.URL,
	})
	if err != nil {
		t.Fatalf("NewStaticResolver() error = %v", err)
	}

	forwarder, err := NewDiscovered(DiscoveryConfig{
		Resolver:    resolver,
		MaxAttempts: 2,
		Client:      &http.Client{Timeout: 50 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("NewDiscovered() error = %v", err)
	}

	result, err := forwarder.Forward(context.Background(), protocol.NewPacket(1001, nil))
	if err != nil {
		t.Fatalf("Forward() error = %v", err)
	}
	if result.Attempts != 2 || result.Endpoint != safeEndpoint(healthy.URL) {
		t.Fatalf("result=%+v", result)
	}
	select {
	case <-slowStarted:
	default:
		t.Fatal("slow endpoint was not attempted")
	}
	select {
	case <-slowStopped:
	case <-time.After(time.Second):
		t.Fatal("timed-out HTTP attempt did not release its request context")
	}
}

func TestDiscoveredForwarderStopsOnCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer backend.Close()
	defer backend.CloseClientConnections()
	resolver, err := NewStaticResolver([]string{backend.URL})
	if err != nil {
		t.Fatalf("NewStaticResolver() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	forwarder, err := NewDiscovered(DiscoveryConfig{
		Resolver:    resolver,
		MaxAttempts: 2,
	})
	if err != nil {
		t.Fatalf("NewDiscovered() error = %v", err)
	}

	type outcome struct {
		result *router.ForwardResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := forwarder.Forward(ctx, protocol.NewPacket(1001, nil))
		done <- outcome{result: result, err: err}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("HTTP attempt did not start")
	}
	cancel()

	var got outcome
	select {
	case got = <-done:
	case <-time.After(time.Second):
		t.Fatal("Forward() did not stop after caller cancellation")
	}
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", got.err)
	}
	forwardErr := requireForwardError(t, got.err)
	if forwardErr.Class != router.FailureClassCanceled || forwardErr.Retryable ||
		forwardErr.Decision != router.FailoverDecisionNotRetryable ||
		forwardErr.Attempts != 1 || got.result.Attempts != 1 {
		t.Fatalf("forward result=%+v error=%+v", got.result, forwardErr)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("canceled HTTP attempt did not release its request context")
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

type stubResolver struct {
	snapshot EndpointSnapshot
	err      error
}

func (r stubResolver) Resolve(ctx context.Context) (EndpointSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return EndpointSnapshot{}, err
	}
	return r.snapshot, r.err
}

func (stubResolver) Close() error {
	return nil
}

func response(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Status:     http.StatusText(statusCode),
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
}

func requireForwardError(t *testing.T, err error) *router.ForwardError {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want *router.ForwardError")
	}
	var forwardErr *router.ForwardError
	if !errors.As(err, &forwardErr) {
		t.Fatalf("error type = %T, want *router.ForwardError", err)
	}
	return forwardErr
}
