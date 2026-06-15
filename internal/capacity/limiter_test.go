package capacity

import "testing"

func TestLimiterTryAcquire(t *testing.T) {
	limiter := NewLimiter(1)
	if !limiter.TryAcquire() {
		t.Fatal("first TryAcquire() = false, want true")
	}
	if limiter.TryAcquire() {
		t.Fatal("second TryAcquire() = true, want false")
	}
	if limiter.InFlight() != 1 {
		t.Fatalf("InFlight() = %d, want 1", limiter.InFlight())
	}

	limiter.Release()
	if limiter.InFlight() != 0 {
		t.Fatalf("InFlight() = %d, want 0", limiter.InFlight())
	}
	if !limiter.TryAcquire() {
		t.Fatal("TryAcquire() after Release = false, want true")
	}
}

func TestNilLimiterAllows(t *testing.T) {
	var limiter *Limiter
	if !limiter.TryAcquire() {
		t.Fatal("nil TryAcquire() = false, want true")
	}
	limiter.Release()
	if limiter.Limit() != 0 || limiter.InFlight() != 0 {
		t.Fatalf("nil limiter limit/inflight = %d/%d, want 0/0", limiter.Limit(), limiter.InFlight())
	}
}
