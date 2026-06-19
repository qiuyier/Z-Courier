package signing

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	Algorithm = "ZCOURIER-HMAC-SHA256"

	HeaderKeyID     = "X-ZCourier-Key-ID"
	HeaderTimestamp = "X-ZCourier-Timestamp"
	HeaderNonce     = "X-ZCourier-Nonce"
	HeaderSignature = "X-ZCourier-Signature"

	DefaultMaxClockSkew    = 30 * time.Second
	DefaultNonceTTL        = time.Minute
	DefaultMaxNonceEntries = 10000

	minimumSecretSize = 32
	nonceBytes        = 18
)

// SignerConfig identifies one HMAC key. Secret must contain at least 32 bytes.
type SignerConfig struct {
	KeyID  string
	Secret []byte
}

// Signer adds Z-Courier HMAC headers to internal HTTP requests.
type Signer struct {
	keyID  string
	secret []byte
	now    func() time.Time
	nonce  func() (string, error)
}

// NewSigner creates an immutable, concurrency-safe signer.
func NewSigner(config SignerConfig) (*Signer, error) {
	keyID := strings.TrimSpace(config.KeyID)
	if !validKeyID(keyID) {
		return nil, fmt.Errorf("%w: key id is required and must contain 1-128 visible ASCII characters", ErrInvalidConfig)
	}
	if len(config.Secret) < minimumSecretSize {
		return nil, fmt.Errorf("%w: secret must contain at least %d bytes", ErrInvalidConfig, minimumSecretSize)
	}

	return &Signer{
		keyID:  keyID,
		secret: append([]byte(nil), config.Secret...),
		now:    time.Now,
		nonce:  randomNonce,
	}, nil
}

// Sign replaces all signature headers on request using body as the exact bytes
// sent on the wire. It does not read or replace request.Body.
func (s *Signer) Sign(request *http.Request, body []byte) error {
	if s == nil || request == nil || request.URL == nil {
		return ErrInvalidRequest
	}

	nonce, err := s.nonce()
	if err != nil {
		return fmt.Errorf("signing: generate nonce: %w", err)
	}
	if !validNonce(nonce) {
		return ErrInvalidNonce
	}
	timestamp := strconv.FormatInt(s.now().Unix(), 10)
	canonical, err := CanonicalString(request, body, timestamp, nonce)
	if err != nil {
		return err
	}

	signature := signatureFor(s.secret, canonical)
	request.Header.Set(HeaderKeyID, s.keyID)
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set(HeaderNonce, nonce)
	request.Header.Set(HeaderSignature, base64.RawURLEncoding.EncodeToString(signature))
	return nil
}

// VerifierConfig configures accepted HMAC keys and replay protection.
type VerifierConfig struct {
	Keys            map[string][]byte
	MaxClockSkew    time.Duration
	NonceTTL        time.Duration
	MaxNonceEntries int
	NonceStore      NonceStore
}

// Verifier validates signatures and atomically consumes valid nonces.
type Verifier struct {
	keys         map[string][]byte
	maxClockSkew time.Duration
	nonceTTL     time.Duration
	nonces       NonceStore
	now          func() time.Time
}

// NewVerifier creates an immutable, concurrency-safe verifier.
func NewVerifier(config VerifierConfig) (*Verifier, error) {
	if len(config.Keys) == 0 {
		return nil, fmt.Errorf("%w: at least one key is required", ErrInvalidConfig)
	}
	keys := make(map[string][]byte, len(config.Keys))
	for rawKeyID, secret := range config.Keys {
		keyID := strings.TrimSpace(rawKeyID)
		if !validKeyID(keyID) {
			return nil, fmt.Errorf("%w: invalid key id %q", ErrInvalidConfig, rawKeyID)
		}
		if len(secret) < minimumSecretSize {
			return nil, fmt.Errorf("%w: secret for key %q must contain at least %d bytes", ErrInvalidConfig, keyID, minimumSecretSize)
		}
		if _, exists := keys[keyID]; exists {
			return nil, fmt.Errorf("%w: duplicate key id %q", ErrInvalidConfig, keyID)
		}
		keys[keyID] = append([]byte(nil), secret...)
	}

	maxClockSkew := config.MaxClockSkew
	if maxClockSkew < 0 {
		return nil, fmt.Errorf("%w: max clock skew must not be negative", ErrInvalidConfig)
	}
	if maxClockSkew == 0 {
		maxClockSkew = DefaultMaxClockSkew
	}
	nonceTTL := config.NonceTTL
	if nonceTTL < 0 {
		return nil, fmt.Errorf("%w: nonce TTL must not be negative", ErrInvalidConfig)
	}
	if nonceTTL == 0 {
		nonceTTL = DefaultNonceTTL
	}
	if nonceTTL < 2*maxClockSkew {
		return nil, fmt.Errorf("%w: nonce TTL must be at least twice max clock skew", ErrInvalidConfig)
	}

	nonceStore := config.NonceStore
	if nonceStore == nil {
		maxEntries := config.MaxNonceEntries
		if maxEntries < 0 {
			return nil, fmt.Errorf("%w: max nonce entries must not be negative", ErrInvalidConfig)
		}
		if maxEntries == 0 {
			maxEntries = DefaultMaxNonceEntries
		}
		var err error
		nonceStore, err = NewMemoryNonceStore(maxEntries)
		if err != nil {
			return nil, err
		}
	}

	return &Verifier{
		keys:         keys,
		maxClockSkew: maxClockSkew,
		nonceTTL:     nonceTTL,
		nonces:       nonceStore,
		now:          time.Now,
	}, nil
}

// Verify validates request headers and body, then consumes the nonce. A valid
// signed request can therefore succeed only once within the nonce TTL.
func (v *Verifier) Verify(request *http.Request, body []byte) error {
	if v == nil || request == nil || request.URL == nil {
		return ErrInvalidRequest
	}

	keyID := request.Header.Get(HeaderKeyID)
	timestamp := request.Header.Get(HeaderTimestamp)
	nonce := request.Header.Get(HeaderNonce)
	encodedSignature := request.Header.Get(HeaderSignature)
	if keyID == "" || timestamp == "" || nonce == "" || encodedSignature == "" {
		return ErrMissingSignature
	}

	secret, ok := v.keys[keyID]
	if !ok {
		return ErrUnknownKey
	}
	signedUnix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrInvalidTimestamp
	}
	signedAt := time.Unix(signedUnix, 0)
	now := v.now()
	clockDelta := now.Sub(signedAt)
	if clockDelta < 0 {
		clockDelta = -clockDelta
	}
	if clockDelta > v.maxClockSkew {
		return ErrExpired
	}
	if !validNonce(nonce) {
		return ErrInvalidNonce
	}

	providedSignature, err := base64.RawURLEncoding.DecodeString(encodedSignature)
	if err != nil || len(providedSignature) != sha256.Size {
		return ErrInvalidSignature
	}
	canonical, err := CanonicalString(request, body, timestamp, nonce)
	if err != nil {
		return err
	}
	expectedSignature := signatureFor(secret, canonical)
	if !hmac.Equal(providedSignature, expectedSignature) {
		return ErrInvalidSignature
	}

	if err := v.nonces.Consume(keyID, nonce, now, signedAt.Add(v.nonceTTL)); err != nil {
		return err
	}
	return nil
}

// CanonicalString returns the exact UTF-8 string covered by HMAC-SHA256.
func CanonicalString(request *http.Request, body []byte, timestamp, nonce string) (string, error) {
	if request == nil || request.URL == nil || strings.TrimSpace(request.Method) == "" {
		return "", ErrInvalidRequest
	}
	query, err := canonicalQuery(request.URL.RawQuery)
	if err != nil {
		return "", fmt.Errorf("%w: invalid query: %v", ErrInvalidRequest, err)
	}
	path := request.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	bodyDigest := sha256.Sum256(body)

	parts := []string{
		Algorithm,
		timestamp,
		nonce,
		strings.ToUpper(request.Method),
		path,
		query,
		hex.EncodeToString(bodyDigest[:]),
	}
	return strings.Join(parts, "\n"), nil
}

func signatureFor(secret []byte, canonical string) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(canonical))
	return mac.Sum(nil)
}

func canonicalQuery(raw string) (string, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "", err
	}
	type pair struct {
		key   string
		value string
	}
	pairs := make([]pair, 0, len(values))
	for key, items := range values {
		if len(items) == 0 {
			items = []string{""}
		}
		for _, value := range items {
			pairs = append(pairs, pair{key: percentEncode(key), value: percentEncode(value)})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].key != pairs[j].key {
			return pairs[i].key < pairs[j].key
		}
		return pairs[i].value < pairs[j].value
	})
	encoded := make([]string, 0, len(pairs))
	for _, item := range pairs {
		encoded = append(encoded, item.key+"="+item.value)
	}
	return strings.Join(encoded, "&"), nil
}

func percentEncode(value string) string {
	const hexDigits = "0123456789ABCDEF"
	var encoded strings.Builder
	encoded.Grow(len(value))
	for _, character := range []byte(value) {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '.' || character == '_' || character == '~' {
			encoded.WriteByte(character)
			continue
		}
		encoded.WriteByte('%')
		encoded.WriteByte(hexDigits[character>>4])
		encoded.WriteByte(hexDigits[character&0x0f])
	}
	return encoded.String()
}

func randomNonce() (string, error) {
	data := make([]byte, nonceBytes)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func validNonce(nonce string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(nonce)
	return err == nil && len(decoded) >= 16 && len(decoded) <= 64
}

func validKeyID(keyID string) bool {
	if len(keyID) == 0 || len(keyID) > 128 {
		return false
	}
	for _, character := range []byte(keyID) {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}
