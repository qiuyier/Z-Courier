package server

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/qiuyier/Z-Courier/internal/downlink"
	"github.com/qiuyier/Z-Courier/internal/metrics"
	"github.com/qiuyier/Z-Courier/pkg/sdk/signing"
	"go.uber.org/zap"
)

type internalHMACHandler struct {
	next               http.Handler
	verifier           *signing.Verifier
	maxRequestBodySize int64
	logger             *zap.Logger
}

func newInternalHMACHandler(next http.Handler, config Config, logger *zap.Logger) (http.Handler, error) {
	verifier, err := signing.NewVerifier(signing.VerifierConfig{
		Keys:            config.InternalHTTPAuth.HMAC.Keys,
		MaxClockSkew:    config.InternalHTTPAuth.HMAC.MaxClockSkew,
		NonceTTL:        config.InternalHTTPAuth.HMAC.NonceTTL,
		MaxNonceEntries: config.InternalHTTPAuth.HMAC.MaxNonceEntries,
	})
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &internalHMACHandler{
		next:               next,
		verifier:           verifier,
		maxRequestBodySize: config.InternalMaxRequestBodySize,
		logger:             logger,
	}, nil
}

func (h *internalHMACHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !requiresInternalHMAC(r.URL.Path) {
		h.next.ServeHTTP(w, r)
		return
	}

	body, err := h.readBody(w, r)
	if err != nil {
		metrics.RecordInternalHTTPSignature("request_too_large")
		writeInternalAuthJSON(w, http.StatusRequestEntityTooLarge, "request_too_large")
		return
	}
	if err := h.verifier.Verify(r, body); err != nil {
		result, status, code := internalHMACError(err)
		metrics.RecordInternalHTTPSignature(result)
		h.logger.Warn(
			"internal HTTP HMAC verification failed",
			zap.String("result", result),
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
		)
		writeInternalAuthJSON(w, status, code)
		return
	}

	metrics.RecordInternalHTTPSignature("success")
	h.next.ServeHTTP(w, r)
}

func (h *internalHMACHandler) readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	return readHMACBody(w, r, h.maxRequestBodySize)
}

func readHMACBody(w http.ResponseWriter, r *http.Request, maxRequestBodySize int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	reader := http.MaxBytesReader(w, r.Body, maxRequestBodySize)
	body, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func requiresInternalHMAC(path string) bool {
	return strings.HasPrefix(path, "/internal/") && path != downlink.PeerPushPath
}

func internalHMACError(err error) (result string, status int, code string) {
	switch {
	case errors.Is(err, signing.ErrMissingSignature):
		return "missing", http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, signing.ErrUnknownKey):
		return "unknown_key", http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, signing.ErrExpired), errors.Is(err, signing.ErrInvalidTimestamp):
		return "expired", http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, signing.ErrInvalidNonce):
		return "invalid_nonce", http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, signing.ErrInvalidSignature):
		return "invalid_signature", http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, signing.ErrReplay):
		return "replay", http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, signing.ErrNonceStoreFull):
		return "nonce_store_full", http.StatusServiceUnavailable, "auth_unavailable"
	default:
		return "invalid_request", http.StatusUnauthorized, "unauthorized"
	}
}

func writeInternalAuthJSON(w http.ResponseWriter, status int, code string) {
	body, err := sonic.Marshal(struct {
		Code string `json:"code"`
	}{Code: code})
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
