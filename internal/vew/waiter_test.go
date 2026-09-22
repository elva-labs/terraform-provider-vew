package vew

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestWaiterReturnsWhenEvaluatorIsDone(t *testing.T) {
	tests := []struct {
		name      string
		statuses  []string
		doneAt    int
		wantReads int
	}{
		{name: "done on first status", statuses: []string{"VALIDATED"}, doneAt: 0, wantReads: 1},
		{name: "waits for evaluator", statuses: []string{"CREATING", "VALIDATED"}, doneAt: 1, wantReads: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			w := testWaiter()
			err := w.Until(context.Background(), time.Minute, 0, func(context.Context) (PollResult, error) {
				status := tc.statuses[reads]
				reads++
				return PollResult{Status: status}, nil
			}, func(status string) (bool, error) {
				return status == tc.statuses[tc.doneAt], nil
			})
			if err != nil {
				t.Fatalf("Until() error = %v", err)
			}
			if reads != tc.wantReads {
				t.Fatalf("reads = %d, want %d", reads, tc.wantReads)
			}
		})
	}
}

func TestWaiterPassesEveryObservedStatusToEvaluator(t *testing.T) {
	tests := []struct {
		name     string
		statuses []string
	}{
		{name: "one transition", statuses: []string{"CREATING", "VALIDATED"}},
		{name: "multiple transitions", statuses: []string{"CREATING", "TESTING", "VALIDATED"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var observed []string
			reads := 0
			w := testWaiter()
			if err := w.Until(context.Background(), time.Minute, 0, func(context.Context) (PollResult, error) {
				status := tc.statuses[reads]
				reads++
				return PollResult{Status: status}, nil
			}, func(status string) (bool, error) {
				observed = append(observed, status)
				return status == "VALIDATED", nil
			}); err != nil {
				t.Fatalf("Until() error = %v", err)
			}
			if !reflect.DeepEqual(observed, tc.statuses) {
				t.Fatalf("observed statuses = %v, want %v", observed, tc.statuses)
			}
		})
	}
}

func TestWaiterUsesInitialAndRetryAfterDelays(t *testing.T) {
	var delays []time.Duration
	var events []string
	reads := 0
	w := testWaiter()
	// Capture delays through the injected sleep function while preserving its
	// no-real-time behavior.
	w.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		events = append(events, "sleep "+delay.String())
		return nil
	}
	if err := w.Until(context.Background(), time.Minute, 5*time.Second, func(context.Context) (PollResult, error) {
		reads++
		events = append(events, "read")
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
	if want := []string{"sleep 5s", "read", "sleep 7s", "read"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestWaiterUsesBoundedBackoffWhenRetryAfterIsAbsent(t *testing.T) {
	var delays []time.Duration
	reads := 0
	clock := &fakeClock{current: time.Now()}
	w := testWaiter()
	w.now = clock.now
	w.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		clock.current = clock.current.Add(delay)
		return nil
	}
	err := w.Until(context.Background(), 30*time.Second, 0, func(context.Context) (PollResult, error) {
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
	clock := &fakeClock{current: time.Now()}
	w := testWaiter()
	w.now = clock.now
	w.sleep = func(_ context.Context, delay time.Duration) error {
		clock.current = clock.current.Add(delay)
		if delay < time.Second {
			t.Fatalf("backoff delay = %v, want at least one second", delay)
		}
		return nil
	}
	reads := 0
	err := w.Until(context.Background(), time.Second, 0, func(context.Context) (PollResult, error) {
		reads++
		if reads > 1 {
			t.Fatal("read called after fake waiter deadline")
		}
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

func TestWaiterPreservesPrematureDeadlineErrors(t *testing.T) {
	tests := []struct {
		name         string
		initialDelay time.Duration
		sleepErr     bool
		read         StatusReader
	}{
		{name: "sleep", initialDelay: time.Second, sleepErr: true, read: func(context.Context) (PollResult, error) {
			return PollResult{Status: "TESTING"}, nil
		}},
		{name: "read", initialDelay: 0, sleepErr: false, read: func(context.Context) (PollResult, error) {
			return PollResult{}, context.DeadlineExceeded
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := testWaiter()
			if tc.sleepErr {
				w.sleep = func(context.Context, time.Duration) error { return context.DeadlineExceeded }
			}
			err := w.Until(context.Background(), time.Hour, tc.initialDelay, tc.read, func(string) (bool, error) {
				return false, nil
			})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Until() error = %v, want premature context.DeadlineExceeded", err)
			}
			var timeoutErr *TimeoutError
			if errors.As(err, &timeoutErr) {
				t.Fatalf("Until() mislabeled premature deadline as TimeoutError: %v", err)
			}
		})
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

type fakeClock struct{ current time.Time }

func (c *fakeClock) now() time.Time { return c.current }
