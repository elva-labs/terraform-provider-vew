package vew

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

// PollResult is the status and optional server-provided delay from one poll.
type PollResult struct {
	Status     string
	RetryAfter time.Duration
}

// StatusReader reads the current status of an asynchronous VEW operation.
type StatusReader func(context.Context) (PollResult, error)

// StatusEvaluator decides whether a status completes the wait.
type StatusEvaluator func(status string) (done bool, err error)

// Waiter waits for an asynchronous operation to reach a desired status.
type Waiter interface {
	Until(ctx context.Context, timeout, initialDelay time.Duration, read StatusReader, evaluate StatusEvaluator) error
}

type waiter struct {
	now    func() time.Time
	sleep  func(context.Context, time.Duration) error
	jitter func(time.Duration) time.Duration
}

// NewWaiter returns a cancellation-aware status waiter.
func NewWaiter() Waiter {
	return &waiter{
		now:    time.Now,
		sleep:  sleepContext,
		jitter: jitterDelay,
	}
}

// TerminalStatusError indicates that an operation reached a non-success
// terminal status.
type TerminalStatusError struct{ Status string }

func (e *TerminalStatusError) Error() string {
	return "VEW operation reached terminal status " + e.Status
}

// TimeoutError indicates that the waiter deadline elapsed.
type TimeoutError struct{ LastStatus string }

func (e *TimeoutError) Error() string {
	return "timed out waiting for VEW operation; last status: " + e.LastStatus
}

func (w *waiter) Until(parent context.Context, timeout, initialDelay time.Duration, read StatusReader, evaluate StatusEvaluator) error {
	if parent == nil {
		parent = context.Background()
	}
	if w.now == nil {
		w.now = time.Now
	}
	if w.sleep == nil {
		w.sleep = sleepContext
	}
	if w.jitter == nil {
		w.jitter = jitterDelay
	}

	ctx, cancel := context.WithDeadline(parent, w.now().Add(timeout))
	defer cancel()

	lastStatus := ""
	delay := initialDelay
	attempt := 0
	for {
		if err := w.wait(ctx, delay); err != nil {
			return waiterError(parent, ctx, lastStatus, err)
		}

		result, err := read(ctx)
		if err != nil {
			return waiterError(parent, ctx, lastStatus, err)
		}
		lastStatus = result.Status

		done, err := evaluate(result.Status)
		if err != nil {
			return err
		}
		if done {
			return nil
		}

		attempt++
		if result.RetryAfter > 0 {
			delay = result.RetryAfter
		} else {
			delay = w.jitter(backoffDelay(attempt))
		}
	}
}

func (w *waiter) wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	return w.sleep(ctx, delay)
}

func waiterError(parent, ctx context.Context, lastStatus string, err error) error {
	if parentErr := parent.Err(); parentErr != nil {
		return parentErr
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &TimeoutError{LastStatus: lastStatus}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func backoffDelay(attempt int) time.Duration {
	if attempt <= 0 {
		return time.Second
	}
	if attempt >= 5 {
		return 15 * time.Second
	}
	return time.Second << (attempt - 1)
}

func jitterDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return delay
	}
	// Keep calculated delays within ±20% while preserving a non-negative wait.
	return time.Duration(float64(delay) * (0.8 + rand.Float64()*0.4))
}
