package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/qiuyier/Z-Courier/internal/auth"
	"github.com/qiuyier/Z-Courier/internal/protocol"
	"go.uber.org/zap"
)

func TestAuthHandlerSetsPrincipal(t *testing.T) {
	handler := NewAuthHandler(fakeVerifier{
		principal: &auth.Principal{ClientID: "client-a", TokenID: "token-a"},
	}, zap.NewNop())
	ctx := &Context{
		BaseContext: context.Background(),
		Packet:      protocol.NewPacket(1000, nil),
	}
	ctx.Packet.Token = "token-a"

	if err := handler.Handle(ctx); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if ctx.Principal == nil || ctx.Principal.ClientID != "client-a" {
		t.Fatalf("Principal = %+v, want client-a", ctx.Principal)
	}
}

func TestAuthHandlerRejectsInvalidToken(t *testing.T) {
	wantErr := errors.New("invalid")
	handler := NewAuthHandler(fakeVerifier{err: wantErr}, zap.NewNop())
	ctx := &Context{
		BaseContext: context.Background(),
		Packet:      protocol.NewPacket(1000, nil),
	}

	err := handler.Handle(ctx)
	code, reason := AckError(err)
	if code != protocol.AckUnauthorized {
		t.Fatalf("Ack code = %s, want %s", code, protocol.AckUnauthorized)
	}
	if reason != wantErr.Error() {
		t.Fatalf("reason = %q, want %q", reason, wantErr.Error())
	}
}

func TestAuthHandlerMapsVerifierErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want protocol.AckCode
	}{
		{name: "invalid", err: auth.ErrInvalidToken, want: protocol.AckUnauthorized},
		{name: "expired", err: auth.ErrExpiredToken, want: protocol.AckUnauthorized},
		{name: "forbidden", err: auth.ErrForbidden, want: protocol.AckUnauthorized},
		{name: "timeout", err: auth.ErrProviderTimeout, want: protocol.AckAuthUnavailable},
		{name: "unavailable", err: auth.ErrProviderUnavailable, want: protocol.AckAuthUnavailable},
		{name: "misconfigured", err: auth.ErrMisconfigured, want: protocol.AckRejected},
		{name: "unknown", err: errors.New("unknown"), want: protocol.AckUnauthorized},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := NewAuthHandler(fakeVerifier{err: test.err}, zap.NewNop())
			ctx := &Context{
				BaseContext: context.Background(),
				Packet:      protocol.NewPacket(1000, nil),
			}

			code, _ := AckError(handler.Handle(ctx))
			if code != test.want {
				t.Fatalf("Ack code = %s, want %s", code, test.want)
			}
		})
	}
}

func TestAuthHandlerRejectsEmptyPrincipal(t *testing.T) {
	handler := NewAuthHandler(fakeVerifier{}, zap.NewNop())
	ctx := &Context{
		BaseContext: context.Background(),
		Packet:      protocol.NewPacket(1000, nil),
	}

	err := handler.Handle(ctx)
	code, _ := AckError(err)
	if code != protocol.AckUnauthorized {
		t.Fatalf("Ack code = %s, want %s", code, protocol.AckUnauthorized)
	}
}

type fakeVerifier struct {
	principal *auth.Principal
	err       error
}

func (v fakeVerifier) Verify(_ context.Context, _ string) (*auth.Principal, error) {
	if v.err != nil {
		return nil, v.err
	}

	return v.principal, nil
}
