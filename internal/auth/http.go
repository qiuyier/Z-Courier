package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/capacity"
)

const (
	InternalTokenHeader        = "X-ZCourier-Internal-Token"
	defaultHTTPTimeout         = 2 * time.Second
	defaultHTTPMaxInFlight     = 500
	defaultHTTPMaxResponseSize = int64(64 << 10)
)

type HTTPVerifierConfig struct {
	URL                 string
	InternalToken       string
	Timeout             time.Duration
	MaxInFlight         int
	MaxResponseBodySize int64
	Client              *http.Client
	Clock               func() time.Time
}

type HTTPVerifier struct {
	url                 string
	internalToken       string
	timeout             time.Duration
	maxResponseBodySize int64
	client              *http.Client
	limiter             *capacity.Limiter
	clock               func() time.Time
}

type httpVerifyResponse struct {
	ClientID  string     `json:"client_id"`
	TokenID   string     `json:"token_id"`
	Subject   string     `json:"subject"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func NewHTTPVerifier(config HTTPVerifierConfig) (*HTTPVerifier, error) {
	endpoint := strings.TrimSpace(config.URL)
	parsedURL, err := url.Parse(endpoint)
	if err != nil || parsedURL.Host == "" || parsedURL.User != nil ||
		parsedURL.Fragment != "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return nil, fmt.Errorf("%w: http provider requires an absolute http or https URL", ErrMisconfigured)
	}
	if !validHeaderValue(config.InternalToken) {
		return nil, fmt.Errorf("%w: http provider internal_token contains invalid characters", ErrMisconfigured)
	}

	timeout := config.Timeout
	if timeout == 0 {
		timeout = defaultHTTPTimeout
	}
	if timeout < 0 {
		return nil, fmt.Errorf("%w: http provider timeout must be greater than 0", ErrMisconfigured)
	}

	maxInFlight := config.MaxInFlight
	if maxInFlight == 0 {
		maxInFlight = defaultHTTPMaxInFlight
	}
	if maxInFlight < 0 {
		return nil, fmt.Errorf("%w: http provider max_in_flight must be greater than 0", ErrMisconfigured)
	}

	maxResponseBodySize := config.MaxResponseBodySize
	if maxResponseBodySize == 0 {
		maxResponseBodySize = defaultHTTPMaxResponseSize
	}
	if maxResponseBodySize < 0 {
		return nil, fmt.Errorf("%w: http provider max response body size must be greater than 0", ErrMisconfigured)
	}

	client := config.Client
	if client == nil {
		client = &http.Client{}
	} else {
		cloned := *client
		client = &cloned
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	clock := config.Clock
	if clock == nil {
		clock = time.Now
	}

	return &HTTPVerifier{
		url:                 endpoint,
		internalToken:       config.InternalToken,
		timeout:             timeout,
		maxResponseBodySize: maxResponseBodySize,
		client:              client,
		limiter:             capacity.NewLimiter(maxInFlight),
		clock:               clock,
	}, nil
}

func (*HTTPVerifier) Provider() string {
	return ProviderHTTP
}

func (v *HTTPVerifier) Ping(ctx context.Context) error {
	if v == nil || v.client == nil {
		return ErrMisconfigured
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodHead, v.url, nil)
	if err != nil {
		return fmt.Errorf("%w: create verification health request", ErrMisconfigured)
	}
	if v.internalToken != "" {
		req.Header.Set(InternalTokenHeader, v.internalToken)
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return classifyHTTPProviderError(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode >= http.StatusInternalServerError {
		return mapHTTPProviderStatus(resp.StatusCode)
	}
	return nil
}

func (v *HTTPVerifier) Verify(ctx context.Context, token string) (*Principal, error) {
	if v == nil || v.client == nil || v.limiter == nil {
		return nil, ErrMisconfigured
	}
	if token == "" {
		return nil, ErrInvalidToken
	}
	if !validHeaderValue(token) {
		return nil, fmt.Errorf("%w: token contains invalid characters", ErrInvalidToken)
	}
	if !v.limiter.TryAcquire() {
		return nil, fmt.Errorf("%w: verification capacity exceeded", ErrProviderUnavailable)
	}
	defer v.limiter.Release()

	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, v.url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: create verification request", ErrMisconfigured)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if v.internalToken != "" {
		req.Header.Set(InternalTokenHeader, v.internalToken)
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, classifyHTTPProviderError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, v.maxResponseBodySize))
		return nil, mapHTTPProviderStatus(resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, v.maxResponseBodySize+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read verification response", ErrProviderUnavailable)
	}
	if int64(len(body)) > v.maxResponseBodySize {
		return nil, fmt.Errorf("%w: verification response is too large", ErrProviderUnavailable)
	}

	var decoded httpVerifyResponse
	if err := sonic.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("%w: invalid verification response", ErrProviderUnavailable)
	}
	if strings.TrimSpace(decoded.ClientID) == "" {
		return nil, fmt.Errorf("%w: verification response is missing client_id", ErrProviderUnavailable)
	}

	principal := &Principal{
		ClientID: decoded.ClientID,
		TokenID:  decoded.TokenID,
		Subject:  decoded.Subject,
		Scopes:   append([]string(nil), decoded.Scopes...),
	}
	if decoded.ExpiresAt != nil {
		principal.ExpiresAt = *decoded.ExpiresAt
		if !principal.ExpiresAt.After(v.clock()) {
			return nil, ErrExpiredToken
		}
	}

	return principal, nil
}

func classifyHTTPProviderError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: verification request deadline exceeded", ErrProviderTimeout)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("%w: verification request timed out", ErrProviderTimeout)
	}
	return fmt.Errorf("%w: verification request failed", ErrProviderUnavailable)
}

func mapHTTPProviderStatus(statusCode int) error {
	switch statusCode {
	case http.StatusUnauthorized:
		return ErrInvalidToken
	case http.StatusForbidden:
		return ErrForbidden
	case http.StatusTooManyRequests:
		return ErrProviderUnavailable
	default:
		return fmt.Errorf("%w: verification endpoint returned status %d", ErrProviderUnavailable, statusCode)
	}
}

func validHeaderValue(value string) bool {
	for index := range len(value) {
		if value[index] < 0x20 || value[index] == 0x7f {
			return false
		}
	}
	return true
}
