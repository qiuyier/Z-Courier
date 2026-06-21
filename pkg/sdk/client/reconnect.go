package client

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

// ReconnectError reports that automatic reconnect exhausted its configured
// number of attempts.
type ReconnectError struct {
	Attempts int
	Err      error
}

func (err *ReconnectError) Error() string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%v after %d attempts: %v", ErrReconnectExhausted, err.Attempts, err.Err)
}

func (err *ReconnectError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

func (err *ReconnectError) Is(target error) bool {
	return target == ErrReconnectExhausted || errors.Is(err.Err, target)
}

// WaitReady waits for an in-progress Connect or automatic reconnect to reach
// StateReady. It returns immediately when no connection attempt is active.
func (client *Client) WaitReady(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrInvalidConfig)
	}
	for {
		client.mu.RLock()
		state := client.state
		closed := client.closed
		reconnecting := client.reconnectRunning
		lastError := client.lastError
		stateChanged := client.stateChanged
		client.mu.RUnlock()

		if state == StateReady {
			return nil
		}
		if closed || state == StateClosed || state == StateClosing {
			return ErrClientClosed
		}
		if state == StateDisconnected && !reconnecting {
			if lastError != nil {
				return lastError
			}
			return ErrNotReady
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-stateChanged:
		}
	}
}

func (client *Client) reconnectLoop(initialFailure error) {
	lastFailure := initialFailure
	for attempt := 1; ; attempt++ {
		if maxAttempts := client.config.reconnect.maxAttempts; maxAttempts > 0 && attempt > maxAttempts {
			client.finishReconnect(&ReconnectError{Attempts: maxAttempts, Err: lastFailure})
			return
		}

		delay := reconnectDelay(client.config.reconnect, attempt, rand.Float64())
		if !client.waitReconnectDelay(delay) {
			return
		}

		client.connectMu.Lock()
		if !client.beginReconnectAttempt() {
			client.connectMu.Unlock()
			return
		}
		err := client.connectOnce(client.lifecycleContext)
		client.connectMu.Unlock()

		if err == nil {
			if client.finishReconnectSuccess() {
				return
			}
			lastFailure = client.LastError()
			continue
		}
		lastFailure = err
		if !isRetryableReconnectError(err) {
			client.finishReconnect(err)
			return
		}
		client.prepareReconnectRetry(err)
	}
}

func (client *Client) waitReconnectDelay(delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-client.lifecycleContext.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (client *Client) beginReconnectAttempt() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		client.reconnectRunning = false
		client.setStateLocked(StateClosed)
		return false
	}
	if client.state == StateReady {
		client.reconnectRunning = false
		return false
	}
	client.binding = Binding{}
	client.pendingBeforeReady = nil
	client.setStateLocked(StateConnecting)
	return true
}

func (client *Client) prepareReconnectRetry(err error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		client.reconnectRunning = false
		client.setStateLocked(StateClosed)
		return
	}
	client.lastError = err
	client.setStateLocked(StateReconnectWait)
}

func (client *Client) finishReconnectSuccess() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		client.reconnectRunning = false
		client.setStateLocked(StateClosed)
		return true
	}
	if client.state != StateReady || client.runtime == nil {
		client.setStateLocked(StateReconnectWait)
		return false
	}
	client.reconnectRunning = false
	client.lastError = nil
	return true
}

func (client *Client) finishReconnect(err error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.reconnectRunning = false
	client.lastError = err
	client.binding = Binding{}
	if client.closed {
		client.setStateLocked(StateClosed)
	} else {
		client.setStateLocked(StateDisconnected)
	}
}

func isRetryableReconnectError(err error) bool {
	return !errors.Is(err, ErrClientClosed) &&
		!errors.Is(err, context.Canceled) &&
		!errors.Is(err, ErrAuthenticationFailed) &&
		!errors.Is(err, ErrBindRejected) &&
		!errors.Is(err, ErrUnexpectedBindAck)
}

func reconnectDelay(config normalizedReconnectConfig, attempt int, random float64) time.Duration {
	delay := float64(config.initialDelay)
	maximum := float64(config.maxDelay)
	for current := 1; current < attempt && delay < maximum; current++ {
		delay *= config.multiplier
		if delay > maximum {
			delay = maximum
		}
	}
	if config.jitter > 0 {
		delay *= 1 + ((2*random)-1)*config.jitter
	}
	if delay < 0 {
		return 0
	}
	if delay > maximum {
		delay = maximum
	}
	return time.Duration(delay)
}
