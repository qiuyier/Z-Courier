package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/qiuyier/Z-Courier/internal/metrics"
)

const (
	defaultJWTClientIDClaim      = "client_id"
	defaultJWTTokenIDClaim       = "jti"
	defaultJWTScopesClaim        = "scope"
	defaultJWTRefreshInterval    = 5 * time.Minute
	defaultJWKSFetchTimeout      = 2 * time.Second
	defaultJWKSMaxResponseBody   = int64(1 << 20)
	defaultJWTMaxTokenSize       = 64 << 10
	defaultJWKSRefreshCooldown   = time.Second
	JWKSRefreshResultSuccess     = "success"
	JWKSRefreshResultTimeout     = "timeout"
	JWKSRefreshResultUnavailable = "unavailable"
)

type JWTVerifierConfig struct {
	Issuer              string
	Audience            string
	JWKSURL             string
	Algorithms          []string
	ClientIDClaim       string
	TokenIDClaim        string
	ScopesClaim         string
	ClockSkew           time.Duration
	RefreshInterval     time.Duration
	FetchTimeout        time.Duration
	MaxResponseBodySize int64
	Client              *http.Client
	Clock               func() time.Time
}

type JWTVerifier struct {
	issuer          string
	audience        string
	jwksURL         string
	algorithms      map[string]struct{}
	algorithmNames  []string
	clientIDClaim   string
	tokenIDClaim    string
	scopesClaim     string
	refreshInterval time.Duration
	fetchTimeout    time.Duration
	maxBodySize     int64
	client          *http.Client
	parser          *jwtlib.Parser

	keysMu            sync.RWMutex
	keys              map[string]jwtVerificationKey
	refreshVersion    uint64
	lastRefreshError  error
	refreshMu         sync.Mutex
	lastForcedRefresh time.Time
	refreshCancel     context.CancelFunc
	refreshDone       chan struct{}
	closeOnce         sync.Once
}

type jwtVerificationKey struct {
	key       any
	algorithm string
	keyType   string
	curve     string
}

type jwksDocument struct {
	Keys []jwkDocument `json:"keys"`
}

type jwkDocument struct {
	KeyType   string `json:"kty"`
	KeyID     string `json:"kid"`
	Use       string `json:"use"`
	Algorithm string `json:"alg"`
	Modulus   string `json:"n"`
	Exponent  string `json:"e"`
	Curve     string `json:"crv"`
	X         string `json:"x"`
	Y         string `json:"y"`
}

type jwtHeader struct {
	algorithm string
	keyID     string
}

func NewJWTVerifier(config JWTVerifierConfig) (*JWTVerifier, error) {
	issuer := strings.TrimSpace(config.Issuer)
	audience := strings.TrimSpace(config.Audience)
	endpoint := strings.TrimSpace(config.JWKSURL)
	if issuer == "" || audience == "" || endpoint == "" || len(config.Algorithms) == 0 {
		return nil, fmt.Errorf("%w: jwt provider requires issuer, audience, jwks_url, and algorithms", ErrMisconfigured)
	}
	parsedURL, err := url.Parse(endpoint)
	if err != nil || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.Fragment != "" ||
		(parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return nil, fmt.Errorf("%w: jwt provider requires an absolute http or https JWKS URL", ErrMisconfigured)
	}

	algorithms := make(map[string]struct{}, len(config.Algorithms))
	algorithmNames := make([]string, 0, len(config.Algorithms))
	for _, name := range config.Algorithms {
		name = strings.TrimSpace(name)
		if !supportedJWKSMethod(jwtlib.GetSigningMethod(name)) {
			return nil, fmt.Errorf("%w: jwt provider algorithm %q is unsupported for JWKS verification", ErrMisconfigured, name)
		}
		if _, exists := algorithms[name]; !exists {
			algorithms[name] = struct{}{}
			algorithmNames = append(algorithmNames, name)
		}
	}
	if config.ClockSkew < 0 {
		return nil, fmt.Errorf("%w: jwt provider clock_skew must not be negative", ErrMisconfigured)
	}
	refreshInterval, err := positiveDurationOrDefault(config.RefreshInterval, defaultJWTRefreshInterval, "refresh_interval")
	if err != nil {
		return nil, err
	}
	fetchTimeout, err := positiveDurationOrDefault(config.FetchTimeout, defaultJWKSFetchTimeout, "fetch_timeout")
	if err != nil {
		return nil, err
	}
	maxBodySize := config.MaxResponseBodySize
	if maxBodySize == 0 {
		maxBodySize = defaultJWKSMaxResponseBody
	}
	if maxBodySize < 0 {
		return nil, fmt.Errorf("%w: jwt provider max_response_body_size must be greater than 0", ErrMisconfigured)
	}

	client := config.Client
	if client == nil {
		client = &http.Client{}
	} else {
		cloned := *client
		client = &cloned
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	clock := config.Clock
	if clock == nil {
		clock = time.Now
	}

	verifier := &JWTVerifier{
		issuer:          issuer,
		audience:        audience,
		jwksURL:         endpoint,
		algorithms:      algorithms,
		algorithmNames:  algorithmNames,
		clientIDClaim:   claimName(config.ClientIDClaim, defaultJWTClientIDClaim),
		tokenIDClaim:    claimName(config.TokenIDClaim, defaultJWTTokenIDClaim),
		scopesClaim:     claimName(config.ScopesClaim, defaultJWTScopesClaim),
		refreshInterval: refreshInterval,
		fetchTimeout:    fetchTimeout,
		maxBodySize:     maxBodySize,
		client:          client,
		parser: jwtlib.NewParser(
			jwtlib.WithValidMethods(algorithmNames),
			jwtlib.WithIssuer(issuer),
			jwtlib.WithAudience(audience),
			jwtlib.WithExpirationRequired(),
			jwtlib.WithLeeway(config.ClockSkew),
			jwtlib.WithTimeFunc(clock),
		),
	}
	if err := verifier.refresh(context.Background()); err != nil {
		return nil, err
	}

	refreshCtx, cancel := context.WithCancel(context.Background())
	verifier.refreshCancel = cancel
	verifier.refreshDone = make(chan struct{})
	go verifier.runRefresh(refreshCtx)
	return verifier, nil
}

func (*JWTVerifier) Provider() string { return ProviderJWT }

func (v *JWTVerifier) Ping(ctx context.Context) error {
	if v == nil || v.client == nil {
		return ErrMisconfigured
	}
	return v.refresh(ctx)
}

func (v *JWTVerifier) Close() error {
	if v == nil {
		return nil
	}
	v.closeOnce.Do(func() {
		if v.refreshCancel != nil {
			v.refreshCancel()
		}
		if v.refreshDone != nil {
			<-v.refreshDone
		}
	})
	return nil
}

func (v *JWTVerifier) Verify(ctx context.Context, token string) (*Principal, error) {
	if v == nil || v.client == nil || v.parser == nil || len(v.algorithms) == 0 {
		return nil, ErrMisconfigured
	}
	if token == "" || len(token) > defaultJWTMaxTokenSize || strings.Count(token, ".") != 2 {
		return nil, ErrInvalidToken
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	header, err := v.parseHeader(token)
	if err != nil {
		return nil, err
	}
	keys, refreshVersion, _ := v.keySnapshot()
	if _, ok := keys[header.keyID]; !ok {
		if err := v.refreshMissingKey(ctx, header.keyID, refreshVersion); err != nil {
			return nil, err
		}
		keys, _, _ = v.keySnapshot()
		if _, ok := keys[header.keyID]; !ok {
			return nil, ErrInvalidToken
		}
	}

	claims := jwtlib.MapClaims{}
	parsed, err := v.parser.ParseWithClaims(token, claims, func(parsedToken *jwtlib.Token) (any, error) {
		if parsedToken.Method == nil || parsedToken.Method.Alg() != header.algorithm {
			return nil, ErrInvalidToken
		}
		verificationKey, ok := keys[header.keyID]
		if !ok {
			return nil, ErrInvalidToken
		}
		return keyForAlgorithm(verificationKey, header.algorithm)
	})
	if err != nil {
		return nil, classifyJWTError(err)
	}
	if !parsed.Valid {
		return nil, ErrInvalidToken
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return v.toPrincipal(claims)
}

func (v *JWTVerifier) parseHeader(token string) (jwtHeader, error) {
	parsed, _, err := jwtlib.NewParser().ParseUnverified(token, jwtlib.MapClaims{})
	if err != nil || parsed.Method == nil {
		return jwtHeader{}, ErrInvalidToken
	}
	algorithm := parsed.Method.Alg()
	if _, ok := v.algorithms[algorithm]; !ok {
		return jwtHeader{}, ErrInvalidToken
	}
	keyID, ok := parsed.Header["kid"].(string)
	if !ok || strings.TrimSpace(keyID) == "" {
		return jwtHeader{}, ErrInvalidToken
	}
	return jwtHeader{algorithm: algorithm, keyID: keyID}, nil
}

func (v *JWTVerifier) toPrincipal(claims jwtlib.MapClaims) (*Principal, error) {
	clientID, err := requiredStringClaim(claims, v.clientIDClaim)
	if err != nil {
		return nil, ErrInvalidToken
	}
	tokenID, err := optionalStringClaim(claims, v.tokenIDClaim)
	if err != nil {
		return nil, ErrInvalidToken
	}
	scopes, err := optionalScopesClaim(claims, v.scopesClaim)
	if err != nil {
		return nil, ErrInvalidToken
	}
	subject, err := claims.GetSubject()
	if err != nil {
		return nil, ErrInvalidToken
	}
	expiresAt, err := claims.GetExpirationTime()
	if err != nil || expiresAt == nil {
		return nil, ErrInvalidToken
	}
	return &Principal{
		ClientID:  clientID,
		TokenID:   tokenID,
		Subject:   subject,
		Scopes:    scopes,
		ExpiresAt: expiresAt.Time,
	}, nil
}

func (v *JWTVerifier) runRefresh(ctx context.Context) {
	defer close(v.refreshDone)
	ticker := time.NewTicker(v.refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = v.refresh(ctx)
		}
	}
}

func (v *JWTVerifier) refreshMissingKey(ctx context.Context, keyID string, observedVersion uint64) error {
	v.refreshMu.Lock()
	defer v.refreshMu.Unlock()
	keys, currentVersion, lastErr := v.keySnapshot()
	if currentVersion != observedVersion {
		if _, ok := keys[keyID]; ok {
			return nil
		}
		return lastErr
	}
	if time.Since(v.lastForcedRefresh) < defaultJWKSRefreshCooldown {
		return lastErr
	}
	v.lastForcedRefresh = time.Now()
	return v.fetchAndStore(ctx)
}

func (v *JWTVerifier) refresh(ctx context.Context) error {
	v.refreshMu.Lock()
	defer v.refreshMu.Unlock()
	return v.fetchAndStore(ctx)
}

func (v *JWTVerifier) fetchAndStore(ctx context.Context) error {
	startedAt := time.Now()
	keys, err := v.fetchJWKS(ctx)
	classified := classifyJWKSFetchError(err)
	metrics.RecordAuthJWKSRefresh(jwksRefreshResult(classified), time.Since(startedAt))

	v.keysMu.Lock()
	v.refreshVersion++
	v.lastRefreshError = classified
	if classified == nil {
		v.keys = keys
	}
	v.keysMu.Unlock()
	return classified
}

func (v *JWTVerifier) fetchJWKS(ctx context.Context) (map[string]jwtVerificationKey, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fetchCtx, cancel := context.WithTimeout(ctx, v.fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, v.maxBodySize))
		return nil, fmt.Errorf("JWKS endpoint returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, v.maxBodySize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > v.maxBodySize {
		return nil, fmt.Errorf("JWKS response is too large")
	}
	return decodeJWKS(body, v.algorithms)
}

func (v *JWTVerifier) keySnapshot() (map[string]jwtVerificationKey, uint64, error) {
	v.keysMu.RLock()
	defer v.keysMu.RUnlock()
	return v.keys, v.refreshVersion, v.lastRefreshError
}

func decodeJWKS(body []byte, allowedAlgorithms map[string]struct{}) (map[string]jwtVerificationKey, error) {
	var document jwksDocument
	if err := sonic.Unmarshal(body, &document); err != nil {
		return nil, fmt.Errorf("decode JWKS: %w", err)
	}
	keys := make(map[string]jwtVerificationKey)
	for _, encoded := range document.Keys {
		if encoded.Use != "" && encoded.Use != "sig" {
			continue
		}
		if encoded.Algorithm != "" {
			if _, ok := allowedAlgorithms[encoded.Algorithm]; !ok {
				continue
			}
		}
		if encoded.KeyID == "" {
			return nil, fmt.Errorf("JWKS signing key is missing kid")
		}
		if _, exists := keys[encoded.KeyID]; exists {
			return nil, fmt.Errorf("JWKS contains duplicate kid %q", encoded.KeyID)
		}
		key, supported, err := decodeJWK(encoded)
		if err != nil {
			return nil, err
		}
		if supported {
			if encoded.Algorithm != "" {
				if _, err := keyForAlgorithm(key, encoded.Algorithm); err != nil {
					return nil, fmt.Errorf("JWKS key %q is incompatible with algorithm %q", encoded.KeyID, encoded.Algorithm)
				}
			}
			keys[encoded.KeyID] = key
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("JWKS contains no supported signing keys")
	}
	return keys, nil
}

func decodeJWK(encoded jwkDocument) (jwtVerificationKey, bool, error) {
	switch encoded.KeyType {
	case "RSA":
		key, err := decodeRSAJWK(encoded)
		return jwtVerificationKey{key: key, algorithm: encoded.Algorithm, keyType: encoded.KeyType}, true, err
	case "EC":
		key, err := decodeECDSAJWK(encoded)
		return jwtVerificationKey{key: key, algorithm: encoded.Algorithm, keyType: encoded.KeyType, curve: encoded.Curve}, true, err
	case "OKP":
		if encoded.Curve != "Ed25519" {
			return jwtVerificationKey{}, false, nil
		}
		key, err := decodeEd25519JWK(encoded)
		return jwtVerificationKey{key: key, algorithm: encoded.Algorithm, keyType: encoded.KeyType, curve: encoded.Curve}, true, err
	default:
		return jwtVerificationKey{}, false, nil
	}
}

func decodeRSAJWK(encoded jwkDocument) (*rsa.PublicKey, error) {
	modulus, err := base64.RawURLEncoding.DecodeString(encoded.Modulus)
	if err != nil || len(modulus) == 0 {
		return nil, fmt.Errorf("decode RSA modulus for kid %q", encoded.KeyID)
	}
	exponentBytes, err := base64.RawURLEncoding.DecodeString(encoded.Exponent)
	if err != nil || len(exponentBytes) == 0 || len(exponentBytes) > 4 {
		return nil, fmt.Errorf("decode RSA exponent for kid %q", encoded.KeyID)
	}
	exponent := 0
	for _, value := range exponentBytes {
		exponent = exponent<<8 | int(value)
	}
	publicKey := &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: exponent}
	if publicKey.N.BitLen() < 2048 || publicKey.E < 3 || publicKey.E%2 == 0 {
		return nil, fmt.Errorf("invalid RSA key for kid %q", encoded.KeyID)
	}
	return publicKey, nil
}

func decodeECDSAJWK(encoded jwkDocument) (*ecdsa.PublicKey, error) {
	var curve elliptic.Curve
	switch encoded.Curve {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	case "P-521":
		curve = elliptic.P521()
	default:
		return nil, fmt.Errorf("unsupported EC curve %q for kid %q", encoded.Curve, encoded.KeyID)
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(encoded.X)
	if err != nil || len(xBytes) == 0 {
		return nil, fmt.Errorf("decode EC x coordinate for kid %q", encoded.KeyID)
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(encoded.Y)
	if err != nil || len(yBytes) == 0 {
		return nil, fmt.Errorf("decode EC y coordinate for kid %q", encoded.KeyID)
	}
	publicKey := &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(xBytes), Y: new(big.Int).SetBytes(yBytes)}
	if !curve.IsOnCurve(publicKey.X, publicKey.Y) {
		return nil, fmt.Errorf("invalid EC point for kid %q", encoded.KeyID)
	}
	return publicKey, nil
}

func decodeEd25519JWK(encoded jwkDocument) (ed25519.PublicKey, error) {
	key, err := base64.RawURLEncoding.DecodeString(encoded.X)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid Ed25519 key for kid %q", encoded.KeyID)
	}
	return ed25519.PublicKey(append([]byte(nil), key...)), nil
}

func keyForAlgorithm(key jwtVerificationKey, algorithm string) (any, error) {
	if key.algorithm != "" && key.algorithm != algorithm {
		return nil, ErrInvalidToken
	}
	switch algorithm {
	case "RS256", "RS384", "RS512", "PS256", "PS384", "PS512":
		if key.keyType != "RSA" {
			return nil, ErrInvalidToken
		}
	case "ES256":
		if key.keyType != "EC" || key.curve != "P-256" {
			return nil, ErrInvalidToken
		}
	case "ES384":
		if key.keyType != "EC" || key.curve != "P-384" {
			return nil, ErrInvalidToken
		}
	case "ES512":
		if key.keyType != "EC" || key.curve != "P-521" {
			return nil, ErrInvalidToken
		}
	case "EdDSA":
		if key.keyType != "OKP" || key.curve != "Ed25519" {
			return nil, ErrInvalidToken
		}
	default:
		return nil, ErrInvalidToken
	}
	return key.key, nil
}

func supportedJWKSMethod(method jwtlib.SigningMethod) bool {
	switch method.(type) {
	case *jwtlib.SigningMethodRSA, *jwtlib.SigningMethodRSAPSS, *jwtlib.SigningMethodECDSA, *jwtlib.SigningMethodEd25519:
		return true
	default:
		return false
	}
}

func classifyJWTError(err error) error {
	if errors.Is(err, jwtlib.ErrTokenExpired) {
		return ErrExpiredToken
	}
	return ErrInvalidToken
}

func classifyJWKSFetchError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: JWKS request deadline exceeded", ErrProviderTimeout)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("%w: JWKS request timed out", ErrProviderTimeout)
	}
	return fmt.Errorf("%w: JWKS refresh failed", ErrProviderUnavailable)
}

func jwksRefreshResult(err error) string {
	switch {
	case err == nil:
		return JWKSRefreshResultSuccess
	case errors.Is(err, ErrProviderTimeout):
		return JWKSRefreshResultTimeout
	default:
		return JWKSRefreshResultUnavailable
	}
}

func positiveDurationOrDefault(value, fallback time.Duration, name string) (time.Duration, error) {
	if value == 0 {
		return fallback, nil
	}
	if value < 0 {
		return 0, fmt.Errorf("%w: jwt provider %s must be greater than 0", ErrMisconfigured, name)
	}
	return value, nil
}

func claimName(configured, fallback string) string {
	if configured = strings.TrimSpace(configured); configured != "" {
		return configured
	}
	return fallback
}

func requiredStringClaim(claims jwtlib.MapClaims, name string) (string, error) {
	value, err := optionalStringClaim(claims, name)
	if err != nil || strings.TrimSpace(value) == "" {
		return "", ErrInvalidToken
	}
	return value, nil
}

func optionalStringClaim(claims jwtlib.MapClaims, name string) (string, error) {
	raw, ok := claims[name]
	if !ok {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", ErrInvalidToken
	}
	return value, nil
}

func optionalScopesClaim(claims jwtlib.MapClaims, name string) ([]string, error) {
	value, ok := claims[name]
	if !ok {
		return nil, nil
	}
	switch value := value.(type) {
	case string:
		return strings.Fields(value), nil
	case []string:
		return append([]string(nil), value...), nil
	case []any:
		scopes := make([]string, 0, len(value))
		for _, item := range value {
			scope, ok := item.(string)
			if !ok {
				return nil, ErrInvalidToken
			}
			scopes = append(scopes, scope)
		}
		return scopes, nil
	default:
		return nil, ErrInvalidToken
	}
}
