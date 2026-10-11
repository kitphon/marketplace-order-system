package grpcclient

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RetryConfig controls bounded outbound RPC retries. MaxAttempts includes the
// first call, so a value of three permits at most two retries.
type RetryConfig struct {
	MaxAttempts       int
	PerAttemptTimeout time.Duration
	InitialBackoff    time.Duration
	MaxBackoff        time.Duration
	JitterFraction    float64
}

type RetryPolicy struct {
	config RetryConfig
	waiter waiter
	random randomSource
}

type waiter interface {
	Wait(ctx context.Context, duration time.Duration) error
}

type randomSource interface {
	Float64() float64
}

type retryOption func(*RetryPolicy)

func withWaiter(value waiter) retryOption {
	return func(policy *RetryPolicy) { policy.waiter = value }
}

func withRandom(value randomSource) retryOption {
	return func(policy *RetryPolicy) { policy.random = value }
}

func NewRetryPolicy(config RetryConfig) (*RetryPolicy, error) {
	return newRetryPolicy(config)
}

func newRetryPolicy(config RetryConfig, options ...retryOption) (*RetryPolicy, error) {
	if config.MaxAttempts < 1 {
		return nil, errors.New("maximum attempts must be at least one")
	}
	if config.PerAttemptTimeout <= 0 {
		return nil, errors.New("per-attempt timeout must be positive")
	}
	if config.InitialBackoff <= 0 {
		return nil, errors.New("initial backoff must be positive")
	}
	if config.MaxBackoff < config.InitialBackoff {
		return nil, errors.New("maximum backoff must be at least the initial backoff")
	}
	if config.JitterFraction < 0 || config.JitterFraction > 1 {
		return nil, errors.New("jitter fraction must be between zero and one")
	}
	policy := &RetryPolicy{
		config: config,
		waiter: timerWaiter{},
		random: globalRandom{},
	}
	for _, option := range options {
		option(policy)
	}
	return policy, nil
}

// Do runs operation under the parent context. The boolean result is true only
// when a retryable error consumed every configured attempt.
func (policy *RetryPolicy) Do(
	ctx context.Context,
	operation func(context.Context) error,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	backoff := policy.config.InitialBackoff
	for attempt := 1; attempt <= policy.config.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}

		attemptContext, cancel := context.WithTimeout(ctx, policy.config.PerAttemptTimeout)
		err := operation(attemptContext)
		cancel()
		if err == nil {
			return false, nil
		}
		if parentErr := ctx.Err(); parentErr != nil {
			return false, parentErr
		}
		if !retryableRPCError(err) {
			return false, err
		}
		if attempt == policy.config.MaxAttempts {
			return true, err
		}

		delay := policy.jitteredBackoff(backoff)
		if err := policy.waiter.Wait(ctx, delay); err != nil {
			if parentErr := ctx.Err(); parentErr != nil {
				return false, parentErr
			}
			return false, fmt.Errorf("wait before retry: %w", err)
		}
		backoff = nextBackoff(backoff, policy.config.MaxBackoff)
	}
	panic("unreachable retry loop")
}

func retryableRPCError(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) ||
		status.Code(err) == codes.DeadlineExceeded ||
		status.Code(err) == codes.Unavailable
}

func (policy *RetryPolicy) jitteredBackoff(base time.Duration) time.Duration {
	factor := 1 - policy.config.JitterFraction +
		(2 * policy.config.JitterFraction * policy.random.Float64())
	delay := time.Duration(float64(base) * factor)
	if delay > policy.config.MaxBackoff {
		return policy.config.MaxBackoff
	}
	return delay
}

func nextBackoff(current, maximum time.Duration) time.Duration {
	if current >= maximum/2 {
		return maximum
	}
	return current * 2
}

type timerWaiter struct{}

func (timerWaiter) Wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type globalRandom struct{}

func (globalRandom) Float64() float64 { return rand.Float64() }
