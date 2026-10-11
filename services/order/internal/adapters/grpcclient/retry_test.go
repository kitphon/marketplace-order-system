package grpcclient

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRetryPolicyTransientFailureThenSuccess(t *testing.T) {
	waits := &recordingWaiter{}
	policy := newTestRetryPolicy(t, RetryConfig{
		MaxAttempts:       3,
		PerAttemptTimeout: time.Second,
		InitialBackoff:    10 * time.Millisecond,
		MaxBackoff:        time.Second,
		JitterFraction:    0,
	}, waits, fixedRandom(0.5))

	attempts := 0
	exhausted, err := policy.Do(context.Background(), func(context.Context) error {
		attempts++
		if attempts == 1 {
			return status.Error(codes.Unavailable, "temporarily unavailable")
		}
		return nil
	})
	if err != nil || exhausted {
		t.Fatalf("Do() = exhausted %v, error %v; want success", exhausted, err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestRetryPolicyMaximumAttemptsAndExponentialBackoff(t *testing.T) {
	waits := &recordingWaiter{}
	policy := newTestRetryPolicy(t, RetryConfig{
		MaxAttempts:       4,
		PerAttemptTimeout: time.Second,
		InitialBackoff:    10 * time.Millisecond,
		MaxBackoff:        25 * time.Millisecond,
		JitterFraction:    0,
	}, waits, fixedRandom(0.5))

	attempts := 0
	exhausted, err := policy.Do(context.Background(), func(context.Context) error {
		attempts++
		return status.Error(codes.Unavailable, "still unavailable")
	})
	if !exhausted || status.Code(err) != codes.Unavailable {
		t.Fatalf("Do() = exhausted %v, error %v; want exhausted Unavailable", exhausted, err)
	}
	if attempts != 4 {
		t.Fatalf("attempts = %d, want 4", attempts)
	}
	want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 25 * time.Millisecond}
	assertDurations(t, waits.durations, want)
}

func TestRetryPolicyJitterStaysInDocumentedRange(t *testing.T) {
	config := RetryConfig{
		MaxAttempts:       2,
		PerAttemptTimeout: time.Second,
		InitialBackoff:    100 * time.Millisecond,
		MaxBackoff:        time.Second,
		JitterFraction:    0.2,
	}
	low := newTestRetryPolicy(t, config, &recordingWaiter{}, fixedRandom(0)).jitteredBackoff(100 * time.Millisecond)
	high := newTestRetryPolicy(t, config, &recordingWaiter{}, fixedRandom(1)).jitteredBackoff(100 * time.Millisecond)
	if low != 80*time.Millisecond || high != 120*time.Millisecond {
		t.Fatalf("jitter range = [%v, %v], want [80ms, 120ms]", low, high)
	}
}

func TestRetryPolicyBusinessFailureUsesOneAttempt(t *testing.T) {
	waits := &recordingWaiter{}
	policy := newTestRetryPolicy(t, defaultTestRetryConfig(), waits, fixedRandom(0.5))
	attempts := 0
	exhausted, err := policy.Do(context.Background(), func(context.Context) error {
		attempts++
		return status.Error(codes.FailedPrecondition, "business failure")
	})
	if exhausted || status.Code(err) != codes.FailedPrecondition || attempts != 1 {
		t.Fatalf("Do() = exhausted %v, error %v, attempts %d", exhausted, err, attempts)
	}
	if len(waits.durations) != 0 {
		t.Fatalf("waits = %v, want none", waits.durations)
	}
}

func TestRetryPolicyAlreadyCanceledParentDoesNotCall(t *testing.T) {
	policy := newTestRetryPolicy(t, defaultTestRetryConfig(), &recordingWaiter{}, fixedRandom(0.5))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attempts := 0
	_, err := policy.Do(ctx, func(context.Context) error {
		attempts++
		return nil
	})
	if !errors.Is(err, context.Canceled) || attempts != 0 {
		t.Fatalf("Do() error = %v, attempts = %d; want canceled and zero", err, attempts)
	}
}

func TestRetryPolicyCancellationDuringBackoffStopsImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	waits := &recordingWaiter{wait: func(waitContext context.Context, _ time.Duration) error {
		cancel()
		return waitContext.Err()
	}}
	policy := newTestRetryPolicy(t, defaultTestRetryConfig(), waits, fixedRandom(0.5))
	attempts := 0
	_, err := policy.Do(ctx, func(context.Context) error {
		attempts++
		return status.Error(codes.Unavailable, "temporarily unavailable")
	})
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("Do() error = %v, attempts = %d; want canceled after one", err, attempts)
	}
}

func TestRetryPolicyParentDeadlineReturnedUnchanged(t *testing.T) {
	policy := newTestRetryPolicy(t, defaultTestRetryConfig(), &recordingWaiter{}, fixedRandom(0.5))
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	attempts := 0
	_, err := policy.Do(ctx, func(context.Context) error {
		attempts++
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || attempts != 0 {
		t.Fatalf("Do() error = %v, attempts = %d; want deadline and zero", err, attempts)
	}
}

func TestRetryPolicyPerAttemptDeadlineCanRetryWhileParentActive(t *testing.T) {
	policy := newTestRetryPolicy(t, defaultTestRetryConfig(), &recordingWaiter{}, fixedRandom(0.5))
	attempts := 0
	exhausted, err := policy.Do(context.Background(), func(attemptContext context.Context) error {
		attempts++
		if _, ok := attemptContext.Deadline(); !ok {
			t.Fatal("attempt context has no deadline")
		}
		if attempts == 1 {
			return context.DeadlineExceeded
		}
		return nil
	})
	if err != nil || exhausted || attempts != 2 {
		t.Fatalf("Do() = exhausted %v, error %v, attempts %d; want success on retry", exhausted, err, attempts)
	}
}

func newTestRetryPolicy(
	t *testing.T,
	config RetryConfig,
	waits *recordingWaiter,
	random fixedRandom,
) *RetryPolicy {
	t.Helper()
	policy, err := newRetryPolicy(config, withWaiter(waits), withRandom(random))
	if err != nil {
		t.Fatalf("NewRetryPolicy() error = %v", err)
	}
	return policy
}

func defaultTestRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:       3,
		PerAttemptTimeout: time.Second,
		InitialBackoff:    time.Millisecond,
		MaxBackoff:        10 * time.Millisecond,
		JitterFraction:    0,
	}
}

type recordingWaiter struct {
	durations []time.Duration
	wait      func(context.Context, time.Duration) error
}

func (waiter *recordingWaiter) Wait(ctx context.Context, duration time.Duration) error {
	waiter.durations = append(waiter.durations, duration)
	if waiter.wait != nil {
		return waiter.wait(ctx, duration)
	}
	return nil
}

type fixedRandom float64

func (value fixedRandom) Float64() float64 { return float64(value) }

func assertDurations(t *testing.T, got, want []time.Duration) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("durations = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("durations = %v, want %v", got, want)
		}
	}
}
