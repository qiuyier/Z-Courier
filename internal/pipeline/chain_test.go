package pipeline

import (
	"errors"
	"testing"
)

func TestChainStopsOnFirstError(t *testing.T) {
	wantErr := errors.New("stop")
	calls := 0
	chain := NewChain(
		HandlerFunc(func(_ *Context) error {
			calls++
			return wantErr
		}),
		HandlerFunc(func(_ *Context) error {
			calls++
			return nil
		}),
	)

	err := chain.Run(&Context{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want %v", err, wantErr)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}
