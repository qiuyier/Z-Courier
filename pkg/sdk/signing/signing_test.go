package signing

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef")

func TestCanonicalStringGolden(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://gateway.example/internal/push?b=two&a=1&a=0", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}

	canonical, err := CanonicalString(request, []byte("hello"), "1780000000", "MDEyMzQ1Njc4OWFiY2RlZg")
	if err != nil {
		t.Fatalf("CanonicalString() error = %v", err)
	}
	want := "ZCOURIER-HMAC-SHA256\n" +
		"1780000000\n" +
		"MDEyMzQ1Njc4OWFiY2RlZg\n" +
		"POST\n" +
		"/internal/push\n" +
		"a=0&a=1&b=two\n" +
		"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if canonical != want {
		t.Fatalf("canonical string:\n%s\nwant:\n%s", canonical, want)
	}
}

func TestCanonicalStringUsesCrossLanguageQueryEncoding(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://gateway.example/internal/messages?tag=b+value&tag=a%20value&symbol=%2F", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	canonical, err := CanonicalString(request, nil, "1780000000", "MDEyMzQ1Njc4OWFiY2RlZg")
	if err != nil {
		t.Fatalf("CanonicalString() error = %v", err)
	}
	if !strings.Contains(canonical, "\nsymbol=%2F&tag=a%20value&tag=b%20value\n") {
		t.Fatalf("canonical query not normalized:\n%s", canonical)
	}
}

func TestSignerAndVerifier(t *testing.T) {
	now := time.Unix(1780000000, 0).UTC()
	signer := newTestSigner(t, now, "MDEyMzQ1Njc4OWFiY2RlZg")
	verifier := newTestVerifier(t, now, nil)
	request := newTestRequest(t, "https://gateway.example/internal/push?b=two&a=1", []byte("hello"))

	if err := signer.Sign(request, []byte("hello")); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	if request.Header.Get(HeaderKeyID) != "backend-1" || request.Header.Get(HeaderTimestamp) != "1780000000" {
		t.Fatalf("signature headers = %v", request.Header)
	}
	if err := verifier.Verify(request, []byte("hello")); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if err := verifier.Verify(request, []byte("hello")); !errors.Is(err, ErrReplay) {
		t.Fatalf("second Verify() error = %v, want ErrReplay", err)
	}
}

func TestVerifierRejectsTampering(t *testing.T) {
	now := time.Unix(1780000000, 0).UTC()
	tests := []struct {
		name   string
		mutate func(*http.Request, *[]byte)
	}{
		{name: "method", mutate: func(request *http.Request, _ *[]byte) { request.Method = http.MethodPut }},
		{name: "path", mutate: func(request *http.Request, _ *[]byte) { request.URL.Path = "/internal/messages" }},
		{name: "query", mutate: func(request *http.Request, _ *[]byte) { request.URL.RawQuery = "a=2" }},
		{name: "body", mutate: func(_ *http.Request, body *[]byte) { *body = []byte("changed") }},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			nonce := []string{
				"MDEyMzQ1Njc4OWFiY2RlZg",
				"MDEyMzQ1Njc4OWFiY2RlZw",
				"MDEyMzQ1Njc4OWFiY2RlaA",
				"MDEyMzQ1Njc4OWFiY2RlaQ",
			}[index]
			signer := newTestSigner(t, now, nonce)
			verifier := newTestVerifier(t, now, nil)
			body := []byte("hello")
			request := newTestRequest(t, "https://gateway.example/internal/push?a=1", body)
			if err := signer.Sign(request, body); err != nil {
				t.Fatalf("Sign() error = %v", err)
			}
			test.mutate(request, &body)
			if err := verifier.Verify(request, body); !errors.Is(err, ErrInvalidSignature) {
				t.Fatalf("Verify() error = %v, want ErrInvalidSignature", err)
			}
		})
	}
}

func TestVerifierRejectsInvalidMetadata(t *testing.T) {
	now := time.Unix(1780000000, 0).UTC()
	tests := []struct {
		name   string
		mutate func(*http.Request)
		want   error
	}{
		{name: "missing", mutate: func(request *http.Request) { request.Header.Del(HeaderSignature) }, want: ErrMissingSignature},
		{name: "unknown key", mutate: func(request *http.Request) { request.Header.Set(HeaderKeyID, "unknown") }, want: ErrUnknownKey},
		{name: "timestamp", mutate: func(request *http.Request) { request.Header.Set(HeaderTimestamp, "invalid") }, want: ErrInvalidTimestamp},
		{name: "expired", mutate: func(request *http.Request) { request.Header.Set(HeaderTimestamp, "1779999900") }, want: ErrExpired},
		{name: "nonce", mutate: func(request *http.Request) { request.Header.Set(HeaderNonce, "short") }, want: ErrInvalidNonce},
		{name: "signature", mutate: func(request *http.Request) { request.Header.Set(HeaderSignature, "invalid") }, want: ErrInvalidSignature},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			signer := newTestSigner(t, now, "MDEyMzQ1Njc4OWFiY2RlZg")
			verifier := newTestVerifier(t, now, nil)
			request := newTestRequest(t, "https://gateway.example/internal/push", nil)
			if err := signer.Sign(request, nil); err != nil {
				t.Fatalf("Sign() error = %v", err)
			}
			test.mutate(request)
			if err := verifier.Verify(request, nil); !errors.Is(err, test.want) {
				t.Fatalf("Verify() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestVerifierAllowsOnlyOneConcurrentNonceConsumer(t *testing.T) {
	now := time.Unix(1780000000, 0).UTC()
	signer := newTestSigner(t, now, "MDEyMzQ1Njc4OWFiY2RlZg")
	verifier := newTestVerifier(t, now, nil)
	request := newTestRequest(t, "https://gateway.example/internal/push", []byte("hello"))
	if err := signer.Sign(request, []byte("hello")); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}

	var accepted atomic.Int32
	var replayed atomic.Int32
	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			cloned := request.Clone(request.Context())
			err := verifier.Verify(cloned, []byte("hello"))
			switch {
			case err == nil:
				accepted.Add(1)
			case errors.Is(err, ErrReplay):
				replayed.Add(1)
			default:
				t.Errorf("Verify() error = %v", err)
			}
		}()
	}
	wait.Wait()
	if accepted.Load() != 1 || replayed.Load() != 31 {
		t.Fatalf("accepted=%d replayed=%d, want 1 and 31", accepted.Load(), replayed.Load())
	}
}

func TestMemoryNonceStoreIsBoundedAndCleansExpiredEntries(t *testing.T) {
	store, err := NewMemoryNonceStore(1)
	if err != nil {
		t.Fatalf("NewMemoryNonceStore() error = %v", err)
	}
	now := time.Unix(1780000000, 0).UTC()
	if err := store.Consume("key", "nonce-1", now, now.Add(time.Minute)); err != nil {
		t.Fatalf("Consume(first) error = %v", err)
	}
	if err := store.Consume("key", "nonce-2", now, now.Add(time.Minute)); !errors.Is(err, ErrNonceStoreFull) {
		t.Fatalf("Consume(full) error = %v, want ErrNonceStoreFull", err)
	}
	if err := store.Consume("key", "nonce-2", now.Add(time.Minute), now.Add(2*time.Minute)); err != nil {
		t.Fatalf("Consume(after expiry) error = %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", store.Len())
	}
}

func TestMemoryNonceStoreCleansByExpiryOrder(t *testing.T) {
	store, err := NewMemoryNonceStore(2)
	if err != nil {
		t.Fatalf("NewMemoryNonceStore() error = %v", err)
	}
	now := time.Unix(1780000000, 0).UTC()
	if err := store.Consume("key", "late", now, now.Add(time.Minute)); err != nil {
		t.Fatalf("Consume(late) error = %v", err)
	}
	if err := store.Consume("key", "early", now, now.Add(time.Second)); err != nil {
		t.Fatalf("Consume(early) error = %v", err)
	}
	if err := store.Consume("key", "replacement", now.Add(time.Second), now.Add(time.Minute)); err != nil {
		t.Fatalf("Consume(replacement) error = %v", err)
	}
	if err := store.Consume("key", "late", now.Add(time.Second), now.Add(time.Minute)); !errors.Is(err, ErrReplay) {
		t.Fatalf("Consume(late replay) error = %v, want ErrReplay", err)
	}
}

func TestConfigurationValidation(t *testing.T) {
	if _, err := NewSigner(SignerConfig{KeyID: "key", Secret: []byte("short")}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewSigner() error = %v, want ErrInvalidConfig", err)
	}
	if _, err := NewVerifier(VerifierConfig{Keys: map[string][]byte{"key": testSecret}, MaxClockSkew: time.Minute, NonceTTL: time.Minute}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewVerifier() error = %v, want ErrInvalidConfig", err)
	}
	if _, err := NewMemoryNonceStore(0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewMemoryNonceStore() error = %v, want ErrInvalidConfig", err)
	}
}

func newTestSigner(t *testing.T, now time.Time, nonce string) *Signer {
	t.Helper()
	signer, err := NewSigner(SignerConfig{KeyID: "backend-1", Secret: testSecret})
	if err != nil {
		t.Fatalf("NewSigner() error = %v", err)
	}
	signer.now = func() time.Time { return now }
	signer.nonce = func() (string, error) { return nonce, nil }
	return signer
}

func newTestVerifier(t *testing.T, now time.Time, store NonceStore) *Verifier {
	t.Helper()
	verifier, err := NewVerifier(VerifierConfig{
		Keys:         map[string][]byte{"backend-1": testSecret},
		MaxClockSkew: 30 * time.Second,
		NonceTTL:     time.Minute,
		NonceStore:   store,
	})
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	verifier.now = func() time.Time { return now }
	return verifier
}

func newTestRequest(t *testing.T, rawURL string, body []byte) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	return request
}
