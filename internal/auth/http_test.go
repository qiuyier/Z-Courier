package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPVerifierSuccess(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(time.Hour)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer client-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get(InternalTokenHeader); got != "internal-token" {
			t.Errorf("internal token = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"client_id":"client-1","token_id":"token-1","subject":"user-1","scopes":["gateway:connect"],"expires_at":"` + expiresAt.Format(time.RFC3339) + `"}`))
	}))
	defer server.Close()

	verifier, err := NewHTTPVerifier(HTTPVerifierConfig{
		URL:           server.URL,
		InternalToken: "internal-token",
		Client:        server.Client(),
		Clock:         func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewHTTPVerifier() error = %v", err)
	}

	principal, err := verifier.Verify(context.Background(), "client-token")
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if principal.ClientID != "client-1" || principal.TokenID != "token-1" || principal.Subject != "user-1" {
		t.Fatalf("principal = %+v", principal)
	}
	if len(principal.Scopes) != 1 || principal.Scopes[0] != "gateway:connect" {
		t.Fatalf("scopes = %v", principal.Scopes)
	}
	if !principal.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("ExpiresAt = %v, want %v", principal.ExpiresAt, expiresAt)
	}
}

func TestHTTPVerifierStatusMapping(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{status: http.StatusUnauthorized, want: ErrInvalidToken},
		{status: http.StatusForbidden, want: ErrForbidden},
		{status: http.StatusTooManyRequests, want: ErrProviderUnavailable},
		{status: http.StatusInternalServerError, want: ErrProviderUnavailable},
		{status: http.StatusBadRequest, want: ErrProviderUnavailable},
	}

	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer server.Close()

			verifier, err := NewHTTPVerifier(HTTPVerifierConfig{URL: server.URL, Client: server.Client()})
			if err != nil {
				t.Fatalf("NewHTTPVerifier() error = %v", err)
			}
			_, err = verifier.Verify(context.Background(), "token")
			if !errors.Is(err, test.want) {
				t.Fatalf("Verify() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestHTTPVerifierTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	verifier, err := NewHTTPVerifier(HTTPVerifierConfig{
		URL:     server.URL,
		Timeout: 20 * time.Millisecond,
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("NewHTTPVerifier() error = %v", err)
	}
	_, err = verifier.Verify(context.Background(), "token")
	if !errors.Is(err, ErrProviderTimeout) {
		t.Fatalf("Verify() error = %v, want ErrProviderTimeout", err)
	}
}

func TestHTTPVerifierMaxInFlight(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"client_id":"client-1"}`))
	}))
	defer server.Close()

	verifier, err := NewHTTPVerifier(HTTPVerifierConfig{
		URL:         server.URL,
		Timeout:     time.Second,
		MaxInFlight: 1,
		Client:      server.Client(),
	})
	if err != nil {
		t.Fatalf("NewHTTPVerifier() error = %v", err)
	}

	firstDone := make(chan error, 1)
	go func() {
		_, verifyErr := verifier.Verify(context.Background(), "token-1")
		firstDone <- verifyErr
	}()
	<-entered

	_, err = verifier.Verify(context.Background(), "token-2")
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("second Verify() error = %v, want ErrProviderUnavailable", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Verify() error = %v", err)
	}
}

func TestHTTPVerifierRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed", body: `{`},
		{name: "missing client id", body: `{"token_id":"token-1"}`},
		{name: "expired", body: `{"client_id":"client-1","expires_at":"2020-01-01T00:00:00Z"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			verifier, err := NewHTTPVerifier(HTTPVerifierConfig{URL: server.URL, Client: server.Client()})
			if err != nil {
				t.Fatalf("NewHTTPVerifier() error = %v", err)
			}
			_, err = verifier.Verify(context.Background(), "token")
			if test.name == "expired" {
				if !errors.Is(err, ErrExpiredToken) {
					t.Fatalf("Verify() error = %v, want ErrExpiredToken", err)
				}
				return
			}
			if !errors.Is(err, ErrProviderUnavailable) {
				t.Fatalf("Verify() error = %v, want ErrProviderUnavailable", err)
			}
		})
	}
}

func TestHTTPVerifierRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 32)))
	}))
	defer server.Close()
	verifier, err := NewHTTPVerifier(HTTPVerifierConfig{
		URL:                 server.URL,
		Client:              server.Client(),
		MaxResponseBodySize: 16,
	})
	if err != nil {
		t.Fatalf("NewHTTPVerifier() error = %v", err)
	}
	_, err = verifier.Verify(context.Background(), "token")
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("Verify() error = %v, want ErrProviderUnavailable", err)
	}
}

func TestHTTPVerifierDoesNotFollowRedirects(t *testing.T) {
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Store(true)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	verifier, err := NewHTTPVerifier(HTTPVerifierConfig{URL: source.URL, Client: source.Client()})
	if err != nil {
		t.Fatalf("NewHTTPVerifier() error = %v", err)
	}
	_, err = verifier.Verify(context.Background(), "token")
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("Verify() error = %v, want ErrProviderUnavailable", err)
	}
	if redirected.Load() {
		t.Fatal("verification request followed a redirect")
	}
}

func TestNewHTTPVerifierRejectsInvalidConfig(t *testing.T) {
	for _, config := range []HTTPVerifierConfig{
		{},
		{URL: "ftp://backend.local/verify"},
		{URL: "http://backend.local/verify", Timeout: -time.Second},
		{URL: "http://backend.local/verify", MaxInFlight: -1},
		{URL: "http://backend.local/verify", MaxResponseBodySize: -1},
		{URL: "http://backend.local/verify", InternalToken: "invalid\nsecret"},
	} {
		if _, err := NewHTTPVerifier(config); !errors.Is(err, ErrMisconfigured) {
			t.Fatalf("NewHTTPVerifier(%+v) error = %v, want ErrMisconfigured", config, err)
		}
	}
}

func TestHTTPVerifierRejectsInvalidTokenHeader(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()
	verifier, err := NewHTTPVerifier(HTTPVerifierConfig{URL: server.URL, Client: server.Client()})
	if err != nil {
		t.Fatalf("NewHTTPVerifier() error = %v", err)
	}
	_, err = verifier.Verify(context.Background(), "invalid\ntoken")
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Verify() error = %v, want ErrInvalidToken", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("HTTP requests = %d, want 0", requests.Load())
	}
}
