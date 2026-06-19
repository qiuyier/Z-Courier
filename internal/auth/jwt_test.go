package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

const (
	testJWTIssuer   = "https://identity.example.test"
	testJWTAudience = "z-courier"
)

func TestJWTVerifierSuccess(t *testing.T) {
	privateKey := newRSAJWTTestKey(t)
	server := newJWKSTestServer(t, rsaJWKS(t, &privateKey.PublicKey, "key-1", "RS256"))
	defer server.Close()
	verifier := newJWTTestVerifier(t, server, JWTVerifierConfig{})
	defer verifier.Close()

	token := signJWTTestToken(t, privateKey, "key-1", jwtlib.SigningMethodRS256, jwtlib.MapClaims{
		"iss":       testJWTIssuer,
		"aud":       testJWTAudience,
		"exp":       time.Now().Add(time.Hour).Unix(),
		"sub":       "user-1",
		"jti":       "token-1",
		"client_id": "client-1",
		"scope":     "messages:send messages:read",
	})
	principal, err := verifier.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if principal.ClientID != "client-1" || principal.TokenID != "token-1" || principal.Subject != "user-1" {
		t.Fatalf("principal = %+v", principal)
	}
	if len(principal.Scopes) != 2 || principal.Scopes[1] != "messages:read" || principal.ExpiresAt.IsZero() {
		t.Fatalf("principal = %+v", principal)
	}
}

func TestJWTVerifierCustomClaims(t *testing.T) {
	privateKey := newRSAJWTTestKey(t)
	server := newJWKSTestServer(t, rsaJWKS(t, &privateKey.PublicKey, "key-1", "RS256"))
	defer server.Close()
	verifier := newJWTTestVerifier(t, server, JWTVerifierConfig{
		ClientIDClaim: "cid",
		TokenIDClaim:  "tid",
		ScopesClaim:   "permissions",
	})
	defer verifier.Close()
	token := signJWTTestToken(t, privateKey, "key-1", jwtlib.SigningMethodRS256, jwtlib.MapClaims{
		"iss":         testJWTIssuer,
		"aud":         testJWTAudience,
		"exp":         time.Now().Add(time.Hour).Unix(),
		"cid":         "client-2",
		"tid":         "token-2",
		"permissions": []string{"push", "status"},
	})

	principal, err := verifier.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if principal.ClientID != "client-2" || principal.TokenID != "token-2" || len(principal.Scopes) != 2 {
		t.Fatalf("principal = %+v", principal)
	}
}

func TestJWTVerifierAsymmetricAlgorithms(t *testing.T) {
	t.Run("ES256", func(t *testing.T) {
		privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("generate ECDSA key: %v", err)
		}
		body := marshalJWKS(t, map[string]any{
			"kty": "EC", "kid": "ec-key", "use": "sig", "alg": "ES256", "crv": "P-256",
			"x": base64.RawURLEncoding.EncodeToString(privateKey.X.Bytes()),
			"y": base64.RawURLEncoding.EncodeToString(privateKey.Y.Bytes()),
		})
		verifyJWTTestAlgorithm(t, privateKey, jwtlib.SigningMethodES256, "ES256", "ec-key", body)
	})

	t.Run("EdDSA", func(t *testing.T) {
		publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generate Ed25519 key: %v", err)
		}
		body := marshalJWKS(t, map[string]any{
			"kty": "OKP", "kid": "ed-key", "use": "sig", "alg": "EdDSA", "crv": "Ed25519",
			"x": base64.RawURLEncoding.EncodeToString(publicKey),
		})
		verifyJWTTestAlgorithm(t, privateKey, jwtlib.SigningMethodEdDSA, "EdDSA", "ed-key", body)
	})
}

func TestJWTVerifierRejectsInvalidTokens(t *testing.T) {
	privateKey := newRSAJWTTestKey(t)
	server := newJWKSTestServer(t, rsaJWKS(t, &privateKey.PublicKey, "key-1", "RS256"))
	defer server.Close()
	verifier := newJWTTestVerifier(t, server, JWTVerifierConfig{})
	defer verifier.Close()

	validClaims := func() jwtlib.MapClaims {
		return jwtlib.MapClaims{
			"iss": testJWTIssuer, "aud": testJWTAudience,
			"exp": time.Now().Add(time.Hour).Unix(), "client_id": "client-1",
		}
	}
	tests := []struct {
		name  string
		token func() string
		want  error
	}{
		{name: "malformed", token: func() string { return "not-a-jwt" }, want: ErrInvalidToken},
		{name: "expired", token: func() string {
			claims := validClaims()
			claims["exp"] = time.Now().Add(-time.Minute).Unix()
			return signJWTTestToken(t, privateKey, "key-1", jwtlib.SigningMethodRS256, claims)
		}, want: ErrExpiredToken},
		{name: "not before", token: func() string {
			claims := validClaims()
			claims["nbf"] = time.Now().Add(time.Hour).Unix()
			return signJWTTestToken(t, privateKey, "key-1", jwtlib.SigningMethodRS256, claims)
		}, want: ErrInvalidToken},
		{name: "wrong issuer", token: func() string {
			claims := validClaims()
			claims["iss"] = "https://wrong.example.test"
			return signJWTTestToken(t, privateKey, "key-1", jwtlib.SigningMethodRS256, claims)
		}, want: ErrInvalidToken},
		{name: "wrong audience", token: func() string {
			claims := validClaims()
			claims["aud"] = "other-service"
			return signJWTTestToken(t, privateKey, "key-1", jwtlib.SigningMethodRS256, claims)
		}, want: ErrInvalidToken},
		{name: "missing expiration", token: func() string {
			claims := validClaims()
			delete(claims, "exp")
			return signJWTTestToken(t, privateKey, "key-1", jwtlib.SigningMethodRS256, claims)
		}, want: ErrInvalidToken},
		{name: "missing client", token: func() string {
			claims := validClaims()
			delete(claims, "client_id")
			return signJWTTestToken(t, privateKey, "key-1", jwtlib.SigningMethodRS256, claims)
		}, want: ErrInvalidToken},
		{name: "unsupported algorithm", token: func() string {
			return signJWTTestToken(t, privateKey, "key-1", jwtlib.SigningMethodPS256, validClaims())
		}, want: ErrInvalidToken},
		{name: "invalid scopes", token: func() string {
			claims := validClaims()
			claims["scope"] = 123
			return signJWTTestToken(t, privateKey, "key-1", jwtlib.SigningMethodRS256, claims)
		}, want: ErrInvalidToken},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := verifier.Verify(context.Background(), test.token())
			if !errors.Is(err, test.want) {
				t.Fatalf("Verify() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestJWTVerifierRefreshesUnknownKey(t *testing.T) {
	privateKey1 := newRSAJWTTestKey(t)
	privateKey2 := newRSAJWTTestKey(t)
	server := newJWKSTestServer(t, rsaJWKS(t, &privateKey1.PublicKey, "key-1", "RS256"))
	defer server.Close()
	verifier := newJWTTestVerifier(t, server, JWTVerifierConfig{})
	defer verifier.Close()

	server.SetBody(rsaJWKS(t, &privateKey2.PublicKey, "key-2", "RS256"))
	token := signJWTTestToken(t, privateKey2, "key-2", jwtlib.SigningMethodRS256, validJWTTestClaims())
	if _, err := verifier.Verify(context.Background(), token); err != nil {
		t.Fatalf("Verify() after key rotation error = %v", err)
	}
	if got := server.Requests(); got < 2 {
		t.Fatalf("JWKS requests = %d, want at least 2", got)
	}
}

func TestJWTVerifierKeepsStaleKeysAfterRefreshFailure(t *testing.T) {
	privateKey := newRSAJWTTestKey(t)
	server := newJWKSTestServer(t, rsaJWKS(t, &privateKey.PublicKey, "key-1", "RS256"))
	defer server.Close()
	verifier := newJWTTestVerifier(t, server, JWTVerifierConfig{RefreshInterval: 20 * time.Millisecond})
	defer verifier.Close()

	server.SetStatus(http.StatusServiceUnavailable)
	deadline := time.Now().Add(time.Second)
	for server.Requests() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if server.Requests() < 2 {
		t.Fatal("background JWKS refresh did not run")
	}
	token := signJWTTestToken(t, privateKey, "key-1", jwtlib.SigningMethodRS256, validJWTTestClaims())
	if _, err := verifier.Verify(context.Background(), token); err != nil {
		t.Fatalf("Verify() with stale key error = %v", err)
	}
}

func TestJWTVerifierUnknownKeyReportsProviderFailure(t *testing.T) {
	privateKey1 := newRSAJWTTestKey(t)
	privateKey2 := newRSAJWTTestKey(t)
	server := newJWKSTestServer(t, rsaJWKS(t, &privateKey1.PublicKey, "key-1", "RS256"))
	defer server.Close()
	verifier := newJWTTestVerifier(t, server, JWTVerifierConfig{})
	defer verifier.Close()

	server.SetStatus(http.StatusServiceUnavailable)
	token := signJWTTestToken(t, privateKey2, "key-2", jwtlib.SigningMethodRS256, validJWTTestClaims())
	if _, err := verifier.Verify(context.Background(), token); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("Verify() error = %v, want ErrProviderUnavailable", err)
	}
}

func TestJWTVerifierLimitsUnknownKeyRefreshes(t *testing.T) {
	privateKey1 := newRSAJWTTestKey(t)
	privateKey2 := newRSAJWTTestKey(t)
	server := newJWKSTestServer(t, rsaJWKS(t, &privateKey1.PublicKey, "key-1", "RS256"))
	defer server.Close()
	verifier := newJWTTestVerifier(t, server, JWTVerifierConfig{})
	defer verifier.Close()

	for index := range 2 {
		token := signJWTTestToken(t, privateKey2, fmt.Sprintf("missing-%d", index), jwtlib.SigningMethodRS256, validJWTTestClaims())
		if _, err := verifier.Verify(context.Background(), token); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("Verify() error = %v, want ErrInvalidToken", err)
		}
	}
	if got := server.Requests(); got != 2 {
		t.Fatalf("JWKS requests = %d, want initial load plus one forced refresh", got)
	}
}

func TestNewJWTVerifierProviderFailures(t *testing.T) {
	t.Run("unavailable", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}))
		defer server.Close()
		_, err := NewJWTVerifier(jwtTestConfig(server.URL, server.Client()))
		if !errors.Is(err, ErrProviderUnavailable) {
			t.Fatalf("NewJWTVerifier() error = %v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(100 * time.Millisecond)
			_, _ = w.Write([]byte(`{"keys":[]}`))
		}))
		defer server.Close()
		config := jwtTestConfig(server.URL, server.Client())
		config.FetchTimeout = 10 * time.Millisecond
		_, err := NewJWTVerifier(config)
		if !errors.Is(err, ErrProviderTimeout) {
			t.Fatalf("NewJWTVerifier() error = %v", err)
		}
	})
	t.Run("redirect", func(t *testing.T) {
		var destinationRequests atomic.Int32
		destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			destinationRequests.Add(1)
			_, _ = w.Write([]byte(`{"keys":[]}`))
		}))
		defer destination.Close()
		source := httptest.NewServer(http.RedirectHandler(destination.URL, http.StatusFound))
		defer source.Close()

		_, err := NewJWTVerifier(jwtTestConfig(source.URL, source.Client()))
		if !errors.Is(err, ErrProviderUnavailable) {
			t.Fatalf("NewJWTVerifier() error = %v, want ErrProviderUnavailable", err)
		}
		if got := destinationRequests.Load(); got != 0 {
			t.Fatalf("redirect destination requests = %d, want 0", got)
		}
	})
	t.Run("response too large", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"keys":[{"padding":"too-large"}]}`))
		}))
		defer server.Close()
		config := jwtTestConfig(server.URL, server.Client())
		config.MaxResponseBodySize = 8
		_, err := NewJWTVerifier(config)
		if !errors.Is(err, ErrProviderUnavailable) {
			t.Fatalf("NewJWTVerifier() error = %v, want ErrProviderUnavailable", err)
		}
	})
}

func TestDecodeJWKSRejectsInvalidDocuments(t *testing.T) {
	privateKey := newRSAJWTTestKey(t)
	validKey := map[string]any{
		"kty": "RSA", "kid": "key-1", "use": "sig", "alg": "RS256",
		"n": base64.RawURLEncoding.EncodeToString(privateKey.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.E)).Bytes()),
	}
	withoutKeyID := cloneJWTTestMap(validKey)
	delete(withoutKeyID, "kid")
	weakRSA := cloneJWTTestMap(validKey)
	weakRSA["n"] = base64.RawURLEncoding.EncodeToString(big.NewInt(17).Bytes())
	incompatibleAlgorithm := cloneJWTTestMap(validKey)
	incompatibleAlgorithm["alg"] = "ES256"

	tests := []struct {
		name       string
		body       []byte
		algorithms map[string]struct{}
	}{
		{name: "malformed", body: []byte(`{"keys":`)},
		{name: "empty", body: []byte(`{"keys":[]}`)},
		{name: "missing kid", body: marshalJWKS(t, withoutKeyID)},
		{name: "weak RSA", body: marshalJWKS(t, weakRSA)},
		{name: "incompatible declared algorithm", body: marshalJWKS(t, incompatibleAlgorithm), algorithms: map[string]struct{}{"ES256": {}}},
	}
	duplicateBody, err := json.Marshal(map[string]any{"keys": []any{validKey, validKey}})
	if err != nil {
		t.Fatalf("marshal duplicate JWKS: %v", err)
	}
	tests = append(tests, struct {
		name       string
		body       []byte
		algorithms map[string]struct{}
	}{name: "duplicate kid", body: duplicateBody})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			algorithms := test.algorithms
			if algorithms == nil {
				algorithms = map[string]struct{}{"RS256": {}}
			}
			if _, err := decodeJWKS(test.body, algorithms); err == nil {
				t.Fatal("decodeJWKS() error = nil, want error")
			}
		})
	}
}

func TestNewJWTVerifierRejectsInvalidConfig(t *testing.T) {
	tests := []JWTVerifierConfig{
		{},
		{Issuer: testJWTIssuer, Audience: testJWTAudience, JWKSURL: "ftp://example.test/jwks", Algorithms: []string{"RS256"}},
		{Issuer: testJWTIssuer, Audience: testJWTAudience, JWKSURL: "https://example.test/jwks", Algorithms: []string{"none"}},
		{Issuer: testJWTIssuer, Audience: testJWTAudience, JWKSURL: "https://example.test/jwks", Algorithms: []string{"HS256"}},
		{Issuer: testJWTIssuer, Audience: testJWTAudience, JWKSURL: "https://example.test/jwks", Algorithms: []string{"invalid"}},
		{Issuer: testJWTIssuer, Audience: testJWTAudience, JWKSURL: "https://example.test/jwks", Algorithms: []string{"RS256"}, ClockSkew: -time.Second},
		{Issuer: testJWTIssuer, Audience: testJWTAudience, JWKSURL: "https://example.test/jwks", Algorithms: []string{"RS256"}, RefreshInterval: -time.Second},
		{Issuer: testJWTIssuer, Audience: testJWTAudience, JWKSURL: "https://example.test/jwks", Algorithms: []string{"RS256"}, FetchTimeout: -time.Second},
		{Issuer: testJWTIssuer, Audience: testJWTAudience, JWKSURL: "https://example.test/jwks", Algorithms: []string{"RS256"}, MaxResponseBodySize: -1},
	}
	for _, config := range tests {
		if _, err := NewJWTVerifier(config); !errors.Is(err, ErrMisconfigured) {
			t.Fatalf("NewJWTVerifier(%+v) error = %v, want ErrMisconfigured", config, err)
		}
	}
}

type jwksTestServer struct {
	*httptest.Server
	mu       sync.RWMutex
	body     []byte
	status   int
	requests atomic.Int32
}

func newJWKSTestServer(t *testing.T, body []byte) *jwksTestServer {
	t.Helper()
	server := &jwksTestServer{body: body, status: http.StatusOK}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		server.requests.Add(1)
		server.mu.RLock()
		defer server.mu.RUnlock()
		if server.status != http.StatusOK {
			http.Error(w, "unavailable", server.status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(server.body)
	}))
	return server
}

func (s *jwksTestServer) SetBody(body []byte) {
	s.mu.Lock()
	s.body = body
	s.status = http.StatusOK
	s.mu.Unlock()
}

func (s *jwksTestServer) SetStatus(status int) {
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
}

func (s *jwksTestServer) Requests() int32 { return s.requests.Load() }

func newJWTTestVerifier(t *testing.T, server *jwksTestServer, override JWTVerifierConfig) *JWTVerifier {
	t.Helper()
	override.Issuer = testJWTIssuer
	override.Audience = testJWTAudience
	override.JWKSURL = server.URL
	override.Algorithms = []string{"RS256"}
	override.Client = server.Client()
	if override.RefreshInterval == 0 {
		override.RefreshInterval = time.Hour
	}
	verifier, err := NewJWTVerifier(override)
	if err != nil {
		t.Fatalf("NewJWTVerifier() error = %v", err)
	}
	return verifier
}

func jwtTestConfig(endpoint string, client *http.Client) JWTVerifierConfig {
	return JWTVerifierConfig{
		Issuer: testJWTIssuer, Audience: testJWTAudience, JWKSURL: endpoint,
		Algorithms: []string{"RS256"}, Client: client,
	}
}

func newRSAJWTTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

func rsaJWKS(t *testing.T, key *rsa.PublicKey, keyID, algorithm string) []byte {
	t.Helper()
	return marshalJWKS(t, map[string]any{
		"kty": "RSA", "kid": keyID, "use": "sig", "alg": algorithm,
		"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	})
}

func marshalJWKS(t *testing.T, key map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{"keys": []any{key}})
	if err != nil {
		t.Fatalf("marshal JWKS: %v", err)
	}
	return body
}

func cloneJWTTestMap(source map[string]any) map[string]any {
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func verifyJWTTestAlgorithm(t *testing.T, privateKey any, method jwtlib.SigningMethod, algorithm, keyID string, jwks []byte) {
	t.Helper()
	server := newJWKSTestServer(t, jwks)
	defer server.Close()
	config := jwtTestConfig(server.URL, server.Client())
	config.Algorithms = []string{algorithm}
	config.RefreshInterval = time.Hour
	verifier, err := NewJWTVerifier(config)
	if err != nil {
		t.Fatalf("NewJWTVerifier() error = %v", err)
	}
	defer verifier.Close()
	token := signJWTTestToken(t, privateKey, keyID, method, validJWTTestClaims())
	if _, err := verifier.Verify(context.Background(), token); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}

func signJWTTestToken(t *testing.T, privateKey any, keyID string, method jwtlib.SigningMethod, claims jwtlib.MapClaims) string {
	t.Helper()
	token := jwtlib.NewWithClaims(method, claims)
	token.Header["kid"] = keyID
	signed, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return signed
}

func validJWTTestClaims() jwtlib.MapClaims {
	return jwtlib.MapClaims{
		"iss": testJWTIssuer, "aud": testJWTAudience,
		"exp": time.Now().Add(time.Hour).Unix(), "client_id": "client-1",
	}
}
