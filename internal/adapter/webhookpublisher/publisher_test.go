package webhookpublisher

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qiuyier/Z-Courier/pkg/sdk/signing"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func TestPublisherPostsSignedBody(t *testing.T) {
	verifier, err := signing.NewVerifier(signing.VerifierConfig{
		Keys: map[string][]byte{"gateway-terminal": []byte(testSecret)},
	})
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	body := []byte(`{"event_id":"message-1:failed"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if r.Header.Get("Content-Type") != contentTypeJSON {
			t.Fatalf("content type = %q", r.Header.Get("Content-Type"))
		}
		got, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		if string(got) != string(body) {
			t.Fatalf("body = %q, want %q", got, body)
		}
		if err := verifier.Verify(r, got); err != nil {
			t.Fatalf("Verify() error = %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	publisher := newTestPublisher(t, server.URL)
	if err := publisher.Publish(context.Background(), body); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
}

func TestPublisherRejectsNon2xxAndRedirects(t *testing.T) {
	t.Run("non-2xx", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		err := newTestPublisher(t, server.URL).Publish(context.Background(), []byte(`{}`))
		if err == nil || !strings.Contains(err.Error(), "503") {
			t.Fatalf("Publish() error = %v, want status 503", err)
		}
	})

	t.Run("redirect", func(t *testing.T) {
		redirected := false
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/target" {
				redirected = true
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.Redirect(w, r, "/target", http.StatusTemporaryRedirect)
		}))
		defer server.Close()

		err := newTestPublisher(t, server.URL).Publish(context.Background(), []byte(`{}`))
		if err == nil || !strings.Contains(err.Error(), "307") {
			t.Fatalf("Publish() error = %v, want status 307", err)
		}
		if redirected {
			t.Fatal("redirect target was called")
		}
	})
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	signer := newTestSigner(t)
	for _, test := range []struct {
		name   string
		config Config
		want   string
	}{
		{name: "empty URL", config: Config{Signer: signer}, want: "absolute http or https URL"},
		{name: "invalid scheme", config: Config{URL: "ftp://receiver.local", Signer: signer}, want: "absolute http or https URL"},
		{name: "missing signer", config: Config{URL: "https://receiver.local"}, want: "signer is required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(test.config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("New() error = %v, want %q", err, test.want)
			}
		})
	}
}

func newTestPublisher(t *testing.T, rawURL string) *Publisher {
	t.Helper()
	publisher, err := New(Config{URL: rawURL, Signer: newTestSigner(t)})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return publisher
}

func newTestSigner(t *testing.T) *signing.Signer {
	t.Helper()
	signer, err := signing.NewSigner(signing.SignerConfig{KeyID: "gateway-terminal", Secret: []byte(testSecret)})
	if err != nil {
		t.Fatalf("NewSigner() error = %v", err)
	}
	return signer
}
