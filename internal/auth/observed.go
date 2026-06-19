package auth

import (
	"context"
	"time"

	"github.com/qiuyier/Z-Courier/internal/metrics"
)

type ObservedVerifier struct {
	provider string
	delegate Verifier
}

func NewObservedVerifier(delegate Verifier) Verifier {
	if delegate == nil {
		return nil
	}
	if _, ok := delegate.(*ObservedVerifier); ok {
		return delegate
	}

	return &ObservedVerifier{
		provider: ProviderName(delegate),
		delegate: delegate,
	}
}

func (v *ObservedVerifier) Provider() string {
	if v == nil {
		return ProviderCustom
	}

	return v.provider
}

func (v *ObservedVerifier) Close() error {
	if v == nil || v.delegate == nil {
		return nil
	}
	if closer, ok := v.delegate.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

func (v *ObservedVerifier) Verify(ctx context.Context, token string) (*Principal, error) {
	if v == nil || v.delegate == nil {
		return nil, ErrMisconfigured
	}

	startedAt := time.Now()
	metrics.AddAuthInFlight(v.provider, 1)
	defer metrics.AddAuthInFlight(v.provider, -1)

	principal, err := v.delegate.Verify(ctx, token)
	metrics.RecordAuthVerify(v.provider, VerificationResult(err), time.Since(startedAt))
	return principal, err
}
