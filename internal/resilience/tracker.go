package resilience

import (
	"sync"
	"time"
)

const (
	DependencyStatusHealthy     = "healthy"
	DependencyStatusDegraded    = "degraded"
	DependencyStatusUnavailable = "unavailable"
)

type DependencyTrackerConfig struct {
	Name                 string
	DegradedThreshold    int
	UnavailableThreshold int
	Now                  func() time.Time
}

type DependencySnapshot struct {
	Name                string
	Status              string
	ConsecutiveFailures int
	LastReason          string
	LastFailureAt       time.Time
	LastSuccessAt       time.Time
	UpdatedAt           time.Time
}

type DependencyTracker struct {
	mu sync.RWMutex

	name                 string
	degradedThreshold    int
	unavailableThreshold int
	now                  func() time.Time

	status              string
	consecutiveFailures int
	lastReason          string
	lastFailureAt       time.Time
	lastSuccessAt       time.Time
	updatedAt           time.Time
}

func NewDependencyTracker(config DependencyTrackerConfig) *DependencyTracker {
	degradedThreshold := config.DegradedThreshold
	if degradedThreshold <= 0 {
		degradedThreshold = 3
	}
	unavailableThreshold := config.UnavailableThreshold
	if unavailableThreshold <= 0 {
		unavailableThreshold = degradedThreshold * 3
	}
	if unavailableThreshold < degradedThreshold {
		unavailableThreshold = degradedThreshold
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}

	return &DependencyTracker{
		name:                 config.Name,
		degradedThreshold:    degradedThreshold,
		unavailableThreshold: unavailableThreshold,
		now:                  now,
		status:               DependencyStatusHealthy,
	}
}

func (t *DependencyTracker) MarkSuccess() DependencySnapshot {
	if t == nil {
		return DependencySnapshot{Status: DependencyStatusHealthy}
	}

	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()

	t.status = DependencyStatusHealthy
	t.consecutiveFailures = 0
	t.lastReason = ""
	t.lastSuccessAt = now
	t.updatedAt = now
	return t.snapshotLocked()
}

func (t *DependencyTracker) MarkFailure(reason string) DependencySnapshot {
	if t == nil {
		return DependencySnapshot{Status: DependencyStatusHealthy}
	}
	if reason == "" {
		reason = "failure"
	}

	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()

	t.consecutiveFailures++
	t.lastReason = reason
	t.lastFailureAt = now
	t.updatedAt = now
	t.status = DependencyStatusHealthy
	if t.consecutiveFailures >= t.unavailableThreshold {
		t.status = DependencyStatusUnavailable
	} else if t.consecutiveFailures >= t.degradedThreshold {
		t.status = DependencyStatusDegraded
	}
	return t.snapshotLocked()
}

func (t *DependencyTracker) Snapshot() DependencySnapshot {
	if t == nil {
		return DependencySnapshot{Status: DependencyStatusHealthy}
	}

	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.snapshotLocked()
}

func (t *DependencyTracker) snapshotLocked() DependencySnapshot {
	return DependencySnapshot{
		Name:                t.name,
		Status:              t.status,
		ConsecutiveFailures: t.consecutiveFailures,
		LastReason:          t.lastReason,
		LastFailureAt:       t.lastFailureAt,
		LastSuccessAt:       t.lastSuccessAt,
		UpdatedAt:           t.updatedAt,
	}
}
