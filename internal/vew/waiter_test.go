package vew

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestWaiterReturnsWhenEvaluatorIsDone(t *testing.T) {
	statuses := []string{"CREATING", "VALIDATED"}
	reads := 0
	w := testWaiter()
	err := w.Until(context.Background(), time.Minute, 0, func(context.Context) (PollResult, error) {
		status := statuses[reads]
		reads++
		return PollResult{Status: status}, nil
	}, func(status string) (bool, error) {
		return status == "VALIDATED", nil
	})
	if err != nil {
		t.Fatalf("Until() error = %v", err)
	}
	if reads != 2 {
		t.Fatalf("reads = %d, want 2", reads)
	}
}

func TestWaiterPassesEveryObservedStatusToEvaluator(t *testing.T) {
	statuses := []string{"CREATING", "TESTING", "VALIDATED"}
	var observed []string
	reads := 0
	w := testWaiter()
	if err := w.Until(context.Background(), time.Minute, 0, func(context.Context) (PollResult, error) {
		status := statuses[reads]
		reads++
		return PollResult{Status: status}, nil
	}, func(status string) (bool, error) {
		observed = append(observed, status)
		return status == "VALIDATED", nil
	}); err != nil {
		t.Fatalf("Until() error = %v", err)
	}
	if !reflect.DeepEqual(observed, statuses) {
		t.Fatalf("observed statuses = %v, want %v", observed, statuses)
	}
}

func TestWaiterUsesInitialAndRetryAfterDelays(t *testing.T) {
	var delays []time.Duration
	reads := 0
	w := testWaiter()
	// Capture delays through the injected sleep function while preserving its
	// no-real-time behavior.
	w.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}
	if err := w.Until(context.Background(), time.Minute, 5*time.Second, func(context.Context) (PollResult, error) {
		reads++
		if reads == 1 {
			return PollResult{Status: "CREATING", RetryAfter: 7 * time.Second}, nil
		}
		return PollResult{Status: "VALIDATED"}, nil
	}, func(status string) (bool, error) {
		return status == "VALIDATED", nil
	}); err != nil {
		t.Fatalf("Until() error = %v", err)
	}
	if want := []time.Duration{5 * time.Second, 7 * time.Second}; !reflect.DeepEqual(delays, want) {
		t.Fatalf("delays = %v, want %v", delays, want)
	}
}

func TestWaiterUsesBoundedBackoffWhenRetryAfterIsAbsent(t *testing.T) {
	var delays []time.Duration
	reads := 0
	w := testWaiter()
	w.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		if len(delays) == 5 {
			return context.DeadlineExceeded
		}
		return nil
	}
	err := w.Until(context.Background(), time.Minute, 0, func(context.Context) (PollResult, error) {
		reads++
		return PollResult{Status: "CREATING"}, nil
	}, func(string) (bool, error) {
		return false, nil
	})
	var timeoutErr *TimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Until() error = %v, want TimeoutError", err)
	}
	if want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second}; !reflect.DeepEqual(delays, want) {
		t.Fatalf("delays = %v, want %v", delays, want)
	}
}

func TestWaiterReturnsTerminalStatusError(t *testing.T) {
	w := testWaiter()
	w.sleep = func(context.Context, time.Duration) error { return nil }
	want := &TerminalStatusError{Status: "FAILED"}
	err := w.Until(context.Background(), time.Minute, 0, func(context.Context) (PollResult, error) {
		return PollResult{Status: "FAILED"}, nil
	}, func(string) (bool, error) {
		return false, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("Until() error = %v, want terminal status error", err)
	}
	if got := err.Error(); got != "VEW operation reached terminal status FAILED" {
		t.Fatalf("error text = %q", got)
	}
}

func TestWaiterReturnsTimeoutWithLastStatus(t *testing.T) {
	w := testWaiter()
	w.sleep = func(context.Context, time.Duration) error { return context.DeadlineExceeded }
	err := w.Until(context.Background(), time.Minute, 0, func(context.Context) (PollResult, error) {
		return PollResult{Status: "TESTING"}, nil
	}, func(string) (bool, error) {
		return false, nil
	})
	var timeoutErr *TimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Until() error = %v, want TimeoutError", err)
	}
	if timeoutErr.LastStatus != "TESTING" {
		t.Fatalf("last status = %q, want TESTING", timeoutErr.LastStatus)
	}
	if got := timeoutErr.Error(); got != "timed out waiting for VEW operation; last status: TESTING" {
		t.Fatalf("error text = %q", got)
	}
}

func TestWaiterStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w := testWaiter()
	w.sleep = func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}
	err := w.Until(ctx, time.Minute, time.Second, func(context.Context) (PollResult, error) {
		t.Fatal("read called after cancellation")
		return PollResult{}, nil
	}, func(string) (bool, error) {
		t.Fatal("evaluate called after cancellation")
		return false, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Until() error = %v, want context.Canceled", err)
	}
}

func testWaiter() *waiter {
	return &waiter{
		now:    time.Now,
		sleep:  func(context.Context, time.Duration) error { return nil },
		jitter: func(delay time.Duration) time.Duration { return delay },
	}
}
