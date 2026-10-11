package main

import (
	"testing"
	"time"
)

func TestRetryConfigFromEnvironmentDefaults(t *testing.T) {
	for _, key := range []string{
		"GRPC_RETRY_MAX_ATTEMPTS",
		"GRPC_PER_ATTEMPT_TIMEOUT",
		"GRPC_RETRY_INITIAL_BACKOFF",
		"GRPC_RETRY_MAX_BACKOFF",
	} {
		t.Setenv(key, "")
	}
	config, err := retryConfigFromEnvironment()
	if err != nil {
		t.Fatalf("retryConfigFromEnvironment() error = %v", err)
	}
	if config.MaxAttempts != 3 ||
		config.PerAttemptTimeout != 2*time.Second ||
		config.InitialBackoff != 100*time.Millisecond ||
		config.MaxBackoff != time.Second ||
		config.JitterFraction != defaultRetryJitterFraction {
		t.Fatalf("config = %+v", config)
	}
}

func TestRetryConfigFromEnvironmentRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "attempts", key: "GRPC_RETRY_MAX_ATTEMPTS", value: "0"},
		{name: "attempt timeout", key: "GRPC_PER_ATTEMPT_TIMEOUT", value: "not-a-duration"},
		{name: "initial backoff", key: "GRPC_RETRY_INITIAL_BACKOFF", value: "-1s"},
		{name: "maximum backoff", key: "GRPC_RETRY_MAX_BACKOFF", value: "0s"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, key := range []string{
				"GRPC_RETRY_MAX_ATTEMPTS",
				"GRPC_PER_ATTEMPT_TIMEOUT",
				"GRPC_RETRY_INITIAL_BACKOFF",
				"GRPC_RETRY_MAX_BACKOFF",
			} {
				t.Setenv(key, "")
			}
			t.Setenv(test.key, test.value)
			if _, err := retryConfigFromEnvironment(); err == nil {
				t.Fatal("retryConfigFromEnvironment() error = nil")
			}
		})
	}
}
