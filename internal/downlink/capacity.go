package downlink

import "fmt"

const (
	QueueCapacityScopeGlobal = "global"
	QueueCapacityScopeDevice = "device"
)

// QueueCapacity limits admission of newly pending reliable messages. Zero
// values disable the corresponding limit.
type QueueCapacity struct {
	MaxPendingGlobal    int
	MaxPendingPerDevice int
}

func (capacity QueueCapacity) Enabled() bool {
	return capacity.MaxPendingGlobal > 0 || capacity.MaxPendingPerDevice > 0
}

// QueueCapacityError reports a rejected admission without persisting the new
// message. Pending is the observed count before the rejected write.
type QueueCapacityError struct {
	Scope   string
	Limit   int
	Pending int
}

func (e *QueueCapacityError) Error() string {
	if e == nil {
		return ErrQueueCapacityExceeded.Error()
	}
	return fmt.Sprintf(
		"%s: scope=%s pending=%d limit=%d",
		ErrQueueCapacityExceeded,
		e.Scope,
		e.Pending,
		e.Limit,
	)
}

func (e *QueueCapacityError) Unwrap() error {
	return ErrQueueCapacityExceeded
}

func newQueueCapacityError(scope string, pending, limit int) error {
	return &QueueCapacityError{Scope: scope, Pending: pending, Limit: limit}
}
