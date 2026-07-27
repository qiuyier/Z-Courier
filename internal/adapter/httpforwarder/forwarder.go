package httpforwarder

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
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
	RequestHost       string
	ServerName        string
	Client            *http.Client
	Observer          Observer
}

type Forwarder struct {
	url         string
	token       string
	requestHost string
	client      *http.Client
	ownsClient  bool
	selector    *endpointSelector
	maxAttempts int
	observer    Observer
}

type UpstreamRequest = upstream.Message

type attemptFailure struct {
	class     router.FailureClass
	retryable bool
	cause     error
}

func New(config Config) *Forwarder {
	client, ownsClient := httpClient(config.Client, config.Timeout, "")
	return &Forwarder{
		url:         config.URL,
		token:       config.Token,
		client:      client,
		ownsClient:  ownsClient,
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
	if strings.ContainsAny(config.RequestHost, "\r\n") || strings.ContainsAny(config.ServerName, "\r\n") {
		return nil, fmt.Errorf("http forwarder: DNS host metadata is invalid")
	}

	client, ownsClient := httpClient(config.Client, config.Timeout, config.ServerName)
	return &Forwarder{
		token:       config.Token,
		requestHost: config.RequestHost,
		client:      client,
		ownsClient:  ownsClient,
		selector:    newEndpointSelector(config.Resolver, config.UnhealthyCooldown, nil, config.Observer),
		maxAttempts: config.MaxAttempts,
		observer:    config.Observer,
	}, nil
}

func httpClient(client *http.Client, timeout time.Duration, serverName string) (*http.Client, bool) {
	if client != nil {
		return client, false
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if serverName == "" {
		return &http.Client{Timeout: timeout}, false
	}

	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		baseTransport = &http.Transport{Proxy: http.ProxyFromEnvironment}
	}
	transport := baseTransport.Clone()
	tlsConfig := transport.TLSClientConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	} else {
		tlsConfig = tlsConfig.Clone()
	}
	tlsConfig.ServerName = serverName
	transport.TLSClientConfig = tlsConfig
	return &http.Client{Timeout: timeout, Transport: transport}, true
}

func (f *Forwarder) Forward(ctx context.Context, packet *protocol.Packet) (result *router.ForwardResult, err error) {
	if f.observer != nil {
		defer func() {
			f.observeForward(result, err)
		}()
	}

	if f.selector == nil && f.url == "" {
		cause := fmt.Errorf("http forwarder: empty url")
		result := annotateForwardResult(nil, "", 0, 1)
		return result, newForwardError(
			&attemptFailure{class: router.FailureClassRequest, cause: cause},
			result,
			router.FailoverDecisionDisabled,
		)
	}

	body, err := sonic.Marshal(newUpstreamRequest(packet))
	if err != nil {
		maxAttempts := f.maxAttempts
		if maxAttempts < 1 {
			maxAttempts = 1
		}
		result := annotateForwardResult(nil, "", 0, maxAttempts)
		return result, newForwardError(
			&attemptFailure{class: router.FailureClassEncoding, cause: err},
			result,
			router.FailoverDecisionNotRetryable,
		)
	}

	if f.selector == nil {
		return f.forwardSingle(ctx, packet, body)
	}

	return f.forwardDiscovered(ctx, packet, body)
}

func (f *Forwarder) forwardSingle(ctx context.Context, packet *protocol.Packet, body []byte) (*router.ForwardResult, error) {
	result, failure := f.forwardTo(ctx, f.url, packet, body)
	result = annotateForwardResult(result, f.url, 1, 1)
	if failure == nil {
		return result, nil
	}

	decision := router.FailoverDecisionNotRetryable
	if failure.retryable {
		decision = router.FailoverDecisionDisabled
	}
	return result, newForwardError(failure, result, decision)
}

func (f *Forwarder) forwardDiscovered(ctx context.Context, packet *protocol.Packet, body []byte) (*router.ForwardResult, error) {
	attempted := make(map[string]struct{}, f.maxAttempts)
	var lastResult *router.ForwardResult
	var lastFailure *attemptFailure

	for attempt := 1; attempt <= f.maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			result := annotateForwardResult(lastResult, resultEndpoint(lastResult), attempt-1, f.maxAttempts)
			failure := contextAttemptFailure(err)
			return result, newForwardError(failure, result, router.FailoverDecisionNotRetryable)
		}

		endpoint, err := f.selector.Select(ctx, attempted)
		if err != nil {
			if lastFailure != nil {
				return lastResult, newForwardError(lastFailure, lastResult, router.FailoverDecisionNoAlternate)
			}
			result := annotateForwardResult(nil, "", 0, f.maxAttempts)
			failure := preAttemptFailure(err)
			decision := router.FailoverDecisionNoAlternate
			if failure.class == router.FailureClassCanceled || failure.class == router.FailureClassTimeout {
				decision = router.FailoverDecisionNotRetryable
			}
			return result, newForwardError(failure, result, decision)
		}
		attempted[endpoint] = struct{}{}

		result, failure := f.forwardTo(ctx, endpoint, packet, body)
		result = annotateForwardResult(result, endpoint, attempt, f.maxAttempts)
		if failure == nil {
			f.selector.MarkSuccess(endpoint)
			return result, nil
		}
		if f.observer != nil {
			f.observer.RecordEndpointFailure(failure.class)
		}
		if !failure.retryable {
			if failure.class == router.FailureClassResponse {
				f.selector.MarkSuccess(endpoint)
			}
			return result, newForwardError(failure, result, router.FailoverDecisionNotRetryable)
		}

		f.selector.MarkFailure(endpoint)
		lastResult = result
		lastFailure = failure
		if attempt == f.maxAttempts {
			decision := router.FailoverDecisionExhausted
			if f.maxAttempts == 1 {
				decision = router.FailoverDecisionDisabled
			}
			return result, newForwardError(failure, result, decision)
		}
	}

	return lastResult, newForwardError(lastFailure, lastResult, router.FailoverDecisionExhausted)
}

func (f *Forwarder) observeForward(result *router.ForwardResult, err error) {
	attempts := 0
	if result != nil {
		attempts = result.Attempts
	}

	observationResult := ForwardObservationSuccess
	var decision router.FailoverDecision
	if err != nil {
		observationResult = ForwardObservationFailure
		var forwardErr *router.ForwardError
		if errors.As(err, &forwardErr) && forwardErr != nil {
			decision = forwardErr.Decision
		}
	} else if attempts > 1 {
		decision = router.FailoverDecisionSucceeded
	}
	f.observer.ObserveForward(attempts, observationResult, decision)
}

func (f *Forwarder) forwardTo(ctx context.Context, endpoint string, packet *protocol.Packet, body []byte) (*router.ForwardResult, *attemptFailure) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, &attemptFailure{class: router.FailureClassRequest, cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	if f.requestHost != "" {
		req.Host = f.requestHost
	}
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
				}, &attemptFailure{
					class: router.FailureClassResponse,
					cause: err,
				}
		}
		return nil, classifyTransportFailure(ctx, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return &router.ForwardResult{
				TargetType: TargetType,
				Status:     resp.Status,
				StatusCode: resp.StatusCode,
			}, &attemptFailure{
				class: router.FailureClassResponse,
				cause: fmt.Errorf("http forwarder: status %s", resp.Status),
			}
	}

	return &router.ForwardResult{
		TargetType: TargetType,
		Status:     resp.Status,
		StatusCode: resp.StatusCode,
	}, nil
}

func classifyTransportFailure(ctx context.Context, err error) *attemptFailure {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return contextAttemptFailure(ctxErr)
	}
	if errors.Is(err, context.Canceled) {
		return contextAttemptFailure(context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &attemptFailure{class: router.FailureClassTimeout, retryable: true, cause: err}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &attemptFailure{class: router.FailureClassTimeout, retryable: true, cause: err}
	}
	return &attemptFailure{class: router.FailureClassTransport, retryable: true, cause: err}
}

func contextAttemptFailure(err error) *attemptFailure {
	failureClass := router.FailureClassCanceled
	if errors.Is(err, context.DeadlineExceeded) {
		failureClass = router.FailureClassTimeout
	}
	return &attemptFailure{class: failureClass, cause: err}
}

func preAttemptFailure(err error) *attemptFailure {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return contextAttemptFailure(err)
	}
	return &attemptFailure{class: router.FailureClassDiscovery, cause: err}
}

func annotateForwardResult(result *router.ForwardResult, endpoint string, attempts, maxAttempts int) *router.ForwardResult {
	if result == nil {
		result = &router.ForwardResult{}
	}
	result.TargetType = TargetType
	result.Endpoint = safeEndpoint(endpoint)
	result.Attempts = attempts
	result.MaxAttempts = maxAttempts
	return result
}

func resultEndpoint(result *router.ForwardResult) string {
	if result == nil {
		return ""
	}
	return result.Endpoint
}

func newForwardError(failure *attemptFailure, result *router.ForwardResult, decision router.FailoverDecision) error {
	if failure == nil {
		return nil
	}
	forwardErr := &router.ForwardError{
		Class:     failure.class,
		Retryable: failure.retryable,
		Decision:  decision,
		Cause:     failure.cause,
	}
	if result != nil {
		forwardErr.Endpoint = result.Endpoint
		forwardErr.Attempts = result.Attempts
		forwardErr.MaxAttempts = result.MaxAttempts
	}
	return forwardErr
}

func safeEndpoint(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func (f *Forwarder) Close() error {
	if f == nil {
		return nil
	}
	var err error
	if f.selector != nil {
		err = f.selector.Close()
	}
	if f.ownsClient && f.client != nil {
		f.client.CloseIdleConnections()
	}
	return err
}

func newUpstreamRequest(packet *protocol.Packet) UpstreamRequest {
	return upstream.NewMessage(packet)
}
