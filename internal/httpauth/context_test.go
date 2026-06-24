package httpauth

import (
	"context"
	"testing"
)

func TestIdentityContextRoundTrip(t *testing.T) {
	ctx := WithIdentity(context.Background(), Identity{
		Mode:  " hmac ",
		KeyID: " backend-1 ",
	})

	identity, ok := IdentityFromContext(ctx)
	if !ok {
		t.Fatal("IdentityFromContext() ok = false, want true")
	}
	if identity.Mode != ModeHMAC || identity.KeyID != "backend-1" {
		t.Fatalf("identity = %+v, want hmac backend-1", identity)
	}
}

func TestIdentityFromEmptyContext(t *testing.T) {
	identity, ok := IdentityFromContext(context.Background())
	if ok {
		t.Fatalf("IdentityFromContext() = %+v, true; want false", identity)
	}
}
