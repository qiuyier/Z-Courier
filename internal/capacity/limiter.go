package capacity

type Limiter struct {
	permits chan struct{}
}

func NewLimiter(limit int) *Limiter {
	if limit <= 0 {
		return nil
	}

	return &Limiter{permits: make(chan struct{}, limit)}
}

func (l *Limiter) TryAcquire() bool {
	if l == nil {
		return true
	}

	select {
	case l.permits <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l *Limiter) Release() {
	if l == nil {
		return
	}

	select {
	case <-l.permits:
	default:
	}
}

func (l *Limiter) Limit() int {
	if l == nil {
		return 0
	}

	return cap(l.permits)
}

func (l *Limiter) InFlight() int {
	if l == nil {
		return 0
	}

	return len(l.permits)
}
