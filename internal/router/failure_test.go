package router

import (
	"errors"
	"strings"
	"testing"
)

func TestForwardErrorKeepsMetadataOutOfErrorString(t *testing.T) {
	cause := errors.New("dial failed with token=secret")
	err := &ForwardError{
		Class:       FailureClassTransport,
		Endpoint:    "http://10.0.0.1:8080/gateway/upstream",
		Attempts:    2,
		MaxAttempts: 2,
		Retryable:   true,
		Decision:    FailoverDecisionExhausted,
		Cause:       cause,
	}

	if !errors.Is(err, cause) {
		t.Fatal("ForwardError does not unwrap its cause")
	}
	for _, secret := range []string{"10.0.0.1", "gateway/upstream", "token=secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("ForwardError.Error() = %q, contains %q", err.Error(), secret)
		}
	}
	if got := err.Error(); got != "upstream forward failed: class=transport decision=exhausted" {
		t.Fatalf("ForwardError.Error() = %q", got)
	}
}
