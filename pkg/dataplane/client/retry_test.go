package client

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errRetriable is a local sentinel used to exercise WithRetry's RetryIf plumbing.
var errRetriable = errors.New("retriable test error")

func retryOnSentinel() RetryCondition {
	return func(err error) bool { return errors.Is(err, errRetriable) }
}

func TestWithRetry_Success(t *testing.T) {
	config := RetryConfig{
		MaxAttempts: 3,
		RetryIf:     retryOnSentinel(),
	}

	attempts := 0
	result, err := WithRetry(context.Background(), config, func(attempt int) (string, error) {
		attempts++
		return "success", nil
	})

	require.NoError(t, err)
	assert.Equal(t, "success", result)
	assert.Equal(t, 1, attempts, "should succeed on first attempt")
}

func TestWithRetry_NoRetryOnNonMatchingError(t *testing.T) {
	config := RetryConfig{
		MaxAttempts: 3,
		RetryIf:     retryOnSentinel(),
	}

	attempts := 0
	result, err := WithRetry(context.Background(), config, func(attempt int) (string, error) {
		attempts++
		return "", errors.New("some other error")
	})

	require.Error(t, err)
	assert.Equal(t, "", result)
	assert.Equal(t, 1, attempts, "should not retry on non-matching error")
	assert.Equal(t, "some other error", err.Error())
}

func TestWithRetry_RetriesOnMatchingError(t *testing.T) {
	config := RetryConfig{
		MaxAttempts: 3,
		RetryIf:     retryOnSentinel(),
	}

	attempts := 0
	result, err := WithRetry(context.Background(), config, func(attempt int) (string, error) {
		attempts++
		if attempt < 3 {
			return "", errRetriable
		}
		return "success", nil
	})

	require.NoError(t, err)
	assert.Equal(t, "success", result)
	assert.Equal(t, 3, attempts, "should retry twice before succeeding")
}

func TestWithRetry_ExhaustsMaxAttempts(t *testing.T) {
	config := RetryConfig{
		MaxAttempts: 3,
		RetryIf:     retryOnSentinel(),
	}

	attempts := 0
	result, err := WithRetry(context.Background(), config, func(attempt int) (string, error) {
		attempts++
		return "", errRetriable
	})

	require.Error(t, err)
	assert.Equal(t, "", result)
	assert.Equal(t, 3, attempts, "should exhaust all attempts")
	assert.ErrorIs(t, err, errRetriable, "should return the retriable error")
}

func TestWithRetry_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	config := RetryConfig{
		MaxAttempts: 3,
		RetryIf:     retryOnSentinel(),
	}

	attempts := 0
	result, err := WithRetry(ctx, config, func(attempt int) (string, error) {
		attempts++
		return "", errRetriable
	})

	require.Error(t, err)
	assert.Equal(t, "", result)
	assert.Equal(t, 0, attempts, "should not execute when context is cancelled")
	assert.ErrorIs(t, err, context.Canceled)
}

func TestWithRetry_BackoffExponential(t *testing.T) {
	config := RetryConfig{
		MaxAttempts: 4,
		RetryIf:     retryOnSentinel(),
		Backoff:     BackoffExponential,
		BaseDelay:   50 * time.Millisecond,
	}

	start := time.Now()
	attempts := 0
	_, _ = WithRetry(context.Background(), config, func(attempt int) (string, error) {
		attempts++
		return "", errRetriable
	})
	elapsed := time.Since(start)

	assert.Equal(t, 4, attempts)
	// Exponential backoff: 50ms + 100ms + 200ms = 350ms
	assert.GreaterOrEqual(t, elapsed, 350*time.Millisecond, "should apply exponential backoff")
}

func TestWithRetry_NoRetryIfNil(t *testing.T) {
	config := RetryConfig{
		MaxAttempts: 3,
		RetryIf:     nil, // No retry condition
	}

	attempts := 0
	result, err := WithRetry(context.Background(), config, func(attempt int) (string, error) {
		attempts++
		return "", errRetriable
	})

	require.Error(t, err)
	assert.Equal(t, "", result)
	assert.Equal(t, 1, attempts, "should not retry when RetryIf is nil")
}

func TestWithRetry_MaxAttemptsValidation(t *testing.T) {
	config := RetryConfig{
		MaxAttempts: 0, // Invalid
		RetryIf:     retryOnSentinel(),
	}

	attempts := 0
	result, err := WithRetry(context.Background(), config, func(attempt int) (string, error) {
		attempts++
		return "success", nil
	})

	require.NoError(t, err)
	assert.Equal(t, "success", result)
	assert.Equal(t, 1, attempts, "should default to 1 attempt when MaxAttempts is invalid")
}

func TestCalculateBackoff(t *testing.T) {
	baseDelay := 100 * time.Millisecond

	tests := []struct {
		name     string
		strategy BackoffStrategy
		attempt  int
		expected time.Duration
	}{
		{"zero-value means no delay", "", 1, 0},
		{"zero-value means no delay (attempt 2)", "", 2, 0},
		{"exponential attempt 1", BackoffExponential, 1, 100 * time.Millisecond},
		{"exponential attempt 2", BackoffExponential, 2, 200 * time.Millisecond},
		{"exponential attempt 3", BackoffExponential, 3, 400 * time.Millisecond},
		{"exponential attempt 4", BackoffExponential, 4, 800 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := calculateBackoff(tt.strategy, baseDelay, tt.attempt)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestIsConnectionError(t *testing.T) {
	condition := IsConnectionError()

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name: "connection refused - net.OpError",
			err: &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: syscall.ECONNREFUSED,
			},
			expected: true,
		},
		{
			name: "connection reset - net.OpError",
			err: &net.OpError{
				Op:  "read",
				Net: "tcp",
				Err: syscall.ECONNRESET,
			},
			expected: true,
		},
		{
			name:     "connection refused - error message",
			err:      errors.New("dial tcp 10.0.0.1:5555: connect: connection refused"),
			expected: true,
		},
		{
			name:     "connection reset - error message",
			err:      errors.New("read tcp 10.0.0.1:5555: connection reset by peer"),
			expected: true,
		},
		{
			name:     "no such host - error message",
			err:      errors.New("dial tcp: lookup invalid.host: no such host"),
			expected: true,
		},
		{
			name:     "generic dial error - error message",
			err:      errors.New("dial tcp 10.0.0.1:5555: i/o timeout"),
			expected: true,
		},
		{
			name:     "http error - should not retry",
			err:      errors.New("HTTP 404 Not Found"),
			expected: false,
		},
		{
			name:     "authentication error - should not retry",
			err:      errors.New("authentication failed"),
			expected: false,
		},
		{
			name:     "parsing error - should not retry",
			err:      errors.New("parsing configuration"),
			expected: false,
		},
		{
			name:     "context canceled - should not retry",
			err:      context.Canceled,
			expected: false,
		},
		{
			name:     "generic error - should not retry",
			err:      errors.New("some other error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := condition(tt.err)
			assert.Equal(t, tt.expected, actual,
				"IsConnectionError(%v) = %v, want %v", tt.err, actual, tt.expected)
		})
	}
}

func TestWithRetry_RetriesOnConnectionError(t *testing.T) {
	config := RetryConfig{
		MaxAttempts: 3,
		RetryIf:     IsConnectionError(),
	}

	attempts := 0
	result, err := WithRetry(context.Background(), config, func(attempt int) (string, error) {
		attempts++
		if attempt < 3 {
			// Simulate connection refused on first two attempts
			return "", &net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: syscall.ECONNREFUSED,
			}
		}
		return "success", nil
	})

	require.NoError(t, err)
	assert.Equal(t, "success", result)
	assert.Equal(t, 3, attempts, "should retry twice before succeeding")
}

func TestWithRetry_NoRetryOnNonConnectionError(t *testing.T) {
	config := RetryConfig{
		MaxAttempts: 3,
		RetryIf:     IsConnectionError(),
	}

	attempts := 0
	result, err := WithRetry(context.Background(), config, func(attempt int) (string, error) {
		attempts++
		return "", errors.New("HTTP 500 Internal Server Error")
	})

	require.Error(t, err)
	assert.Equal(t, "", result)
	assert.Equal(t, 1, attempts, "should not retry on non-connection error")
	assert.Equal(t, "HTTP 500 Internal Server Error", err.Error())
}

func TestContainsAny(t *testing.T) {
	tests := []struct {
		name       string
		s          string
		substrings []string
		expected   bool
	}{
		{
			name:       "contains first substring",
			s:          "connection refused",
			substrings: []string{"connection refused", "connection reset"},
			expected:   true,
		},
		{
			name:       "contains second substring",
			s:          "connection reset by peer",
			substrings: []string{"connection refused", "connection reset"},
			expected:   true,
		},
		{
			name:       "substring in middle",
			s:          "dial tcp 10.0.0.1:5555: connection refused",
			substrings: []string{"connection refused"},
			expected:   true,
		},
		{
			name:       "does not contain any substring",
			s:          "some other error",
			substrings: []string{"connection refused", "connection reset"},
			expected:   false,
		},
		{
			name:       "empty string",
			s:          "",
			substrings: []string{"connection refused"},
			expected:   false,
		},
		{
			name:       "empty substrings",
			s:          "connection refused",
			substrings: []string{},
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := containsAny(tt.s, tt.substrings...)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestIsReloadInProgress(t *testing.T) {
	cond := IsReloadInProgress()
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{
			name:     "master socket connection refused (deploy raw-push 500)",
			err:      errors.New(`raw config push with skip_reload failed with status 500: {"code":500,"message":"cannot execute SetServerState: dial unix /etc/haproxy/haproxy-master.sock: connect: connection refused"}`),
			expected: true,
		},
		{
			name:     "runtime server not found (fast-path PUT mid-reload)",
			err:      errors.New("runtime server replace api/SRV_2: runtime server 'api/SRV_2' not found"),
			expected: true,
		},
		{
			name:     "plain connection refused",
			err:      errors.New("dial tcp 10.0.0.1:5555: connect: connection refused"),
			expected: true,
		},
		{
			name:     "version conflict (409) is NOT a reload signature",
			err:      errors.New("raw config push failed with status 409"),
			expected: false,
		},
		{
			name:     "unrelated sentinel error is NOT a reload signature",
			err:      errors.New("some unrelated client error"),
			expected: false,
		},
		{
			name:     "genuine config error (500) is NOT a reload signature",
			err:      errors.New("raw config push failed with status 500: invalid section 'frontend'"),
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, cond(tt.err))
		})
	}
}
