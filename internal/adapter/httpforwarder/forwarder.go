package httpforwarder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/adapter/upstream"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"github.com/qiuyier/Z-Courier/internal/router"
)

const (
	TargetType          = "http"
	InternalTokenHeader = "X-ZCourier-Internal-Token"
	TraceIDHeader       = "X-ZCourier-Trace-ID"
)

type Config struct {
	URL     string
	Token   string
	Timeout time.Duration
	Client  *http.Client
}

type DiscoveryConfig struct {
	Resolver          Resolver
	Token             string
	Timeout           time.Duration
	MaxAttempts       int
	UnhealthyCooldown time.Duration
	Client            *http.Client
}

type Forwarder struct {
	url         string
	token       string
	client      *http.Client
	selector    *endpointSelector
	maxAttempts int
}

type UpstreamRequest = upstream.Message

func New(config Config) *Forwarder {
	return &Forwarder{
		url:         config.URL,
		token:       config.Token,
		client:      httpClient(config.Client, config.Timeout),
		maxAttempts: 1,
	}
}

func NewDiscovered(config DiscoveryConfig) (*Forwarder, error) {
	if config.Resolver == nil {
		return nil, fmt.Errorf("http forwarder: resolver is required")
	}
	if config.MaxAttempts < 1 || config.MaxAttempts > 4 {
		return nil, fmt.Errorf("http forwarder: max attempts must be between 1 and 4")
	}
	if config.UnhealthyCooldown < 0 {
		return nil, fmt.Errorf("http forwarder: unhealthy cooldown must be greater than or equal to 0")
	}

	return &Forwarder{
		token:       config.Token,
		client:      httpClient(config.Client, config.Timeout),
		selector:    newEndpointSelector(config.Resolver, config.UnhealthyCooldown, nil),
		maxAttempts: config.MaxAttempts,
	}, nil
}

func httpClient(client *http.Client, timeout time.Duration) *http.Client {
	if client != nil {
		return client
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &http.Client{Timeout: timeout}
}

func (f *Forwarder) Forward(ctx context.Context, packet *protocol.Packet) (*router.ForwardResult, error) {
	if f.selector == nil && f.url == "" {
		return nil, fmt.Errorf("http forwarder: empty url")
	}

	body, err := sonic.Marshal(newUpstreamRequest(packet))
	if err != nil {
		return nil, err
	}

	if f.selector == nil {
		return f.forwardTo(ctx, f.url, packet, body)
	}

	return f.forwardDiscovered(ctx, packet, body)
}

func (f *Forwarder) forwardDiscovered(ctx context.Context, packet *protocol.Packet, body []byte) (*router.ForwardResult, error) {
	attempted := make(map[string]struct{}, f.maxAttempts)
	var lastResult *router.ForwardResult
	var lastErr error

	for range f.maxAttempts {
		if err := ctx.Err(); err != nil {
			return lastResult, err
		}

		endpoint, err := f.selector.Select(ctx, attempted)
		if err != nil {
			if lastErr != nil {
				return lastResult, lastErr
			}
			return nil, err
		}
		attempted[endpoint] = struct{}{}

		result, err := f.forwardTo(ctx, endpoint, packet, body)
		if result != nil {
			f.selector.MarkSuccess(endpoint)
			return result, err
		}
		if err == nil {
			f.selector.MarkSuccess(endpoint)
			return nil, nil
		}
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return nil, err
		}

		f.selector.MarkFailure(endpoint)
		lastResult = result
		lastErr = err
	}

	return lastResult, lastErr
}

func (f *Forwarder) forwardTo(ctx context.Context, endpoint string, packet *protocol.Packet, body []byte) (*router.ForwardResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if packet.TraceID != "" {
		req.Header.Set(TraceIDHeader, packet.TraceID)
	}
	if f.token != "" {
		req.Header.Set(InternalTokenHeader, f.token)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		if resp != nil {
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
			return &router.ForwardResult{
				TargetType: TargetType,
				Status:     resp.Status,
				StatusCode: resp.StatusCode,
			}, err
		}
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &router.ForwardResult{
			TargetType: TargetType,
			Status:     resp.Status,
			StatusCode: resp.StatusCode,
		}, fmt.Errorf("http forwarder: status %s: %s", resp.Status, string(data))
	}

	return &router.ForwardResult{
		TargetType: TargetType,
		Status:     resp.Status,
		StatusCode: resp.StatusCode,
	}, nil
}

func (f *Forwarder) Close() error {
	if f == nil || f.selector == nil {
		return nil
	}
	return f.selector.Close()
}

func newUpstreamRequest(packet *protocol.Packet) UpstreamRequest {
	return upstream.NewMessage(packet)
}
