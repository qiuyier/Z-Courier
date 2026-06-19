package server

import (
	"net/http"

	"github.com/qiuyier/Z-Courier/internal/metrics"
	"github.com/qiuyier/Z-Courier/pkg/sdk/signing"
	"go.uber.org/zap"
)

type peerHMACHandler struct {
	next               http.Handler
	verifier           *signing.Verifier
	maxRequestBodySize int64
	logger             *zap.Logger
}

func newPeerHMACHandler(next http.Handler, config ClusterPeerHMACConfig, maxRequestBodySize int64, logger *zap.Logger) (http.Handler, error) {
	verifier, err := signing.NewVerifier(signing.VerifierConfig{
		Keys:            config.Keys,
		MaxClockSkew:    config.MaxClockSkew,
		NonceTTL:        config.NonceTTL,
		MaxNonceEntries: config.MaxNonceEntries,
	})
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &peerHMACHandler{
		next:               next,
		verifier:           verifier,
		maxRequestBodySize: maxRequestBodySize,
		logger:             logger,
	}, nil
}

func (h *peerHMACHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := readHMACBody(w, r, h.maxRequestBodySize)
	if err != nil {
		metrics.RecordClusterPeerSignature("request_too_large")
		writeInternalAuthJSON(w, http.StatusRequestEntityTooLarge, "request_too_large")
		return
	}
	if err := h.verifier.Verify(r, body); err != nil {
		result, status, code := internalHMACError(err)
		metrics.RecordClusterPeerSignature(result)
		h.logger.Warn(
			"cluster peer HMAC verification failed",
			zap.String("result", result),
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
		)
		writeInternalAuthJSON(w, status, code)
		return
	}

	metrics.RecordClusterPeerSignature("success")
	h.next.ServeHTTP(w, r)
}
