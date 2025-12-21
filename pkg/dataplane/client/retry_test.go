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

func TestDefaultRetryConfig(t *testing.T) {
	config := DefaultRetryConfig()

	assert.Equal(t, 1, config.MaxAttempts)
	assert.Nil(t, config.RetryIf)
	assert.Equal(t, BackoffNone, config.Backoff)
	assert.Equal(t, 100*time.Millisecond, config.BaseDelay)
	assert.Nil(t, config.Logger)
}

func TestWithRetry_Success(t *testing.T) {
	config := RetryConfig{
		MaxAttempts: 3,
		RetryIf:     IsVersionConflict(),
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
		RetryIf:     IsVersionConflict(),
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

func TestWithRetry_RetriesOnVersionConflict(t *testing.T) {
	config := RetryConfig{
		MaxAttempts: 3,
		RetryIf:     IsVersionConflict(),
		Backoff:     BackoffNone,
	}

	attempts := 0
	result, err := WithRetry(context.Background(), config, func(attempt int) (string, error) {
		attempts++
		if attempt < 3 {
			return "", &VersionConflictError{
				ExpectedVersion: 1,
				ActualVersion:   "2",
			}
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
		RetryIf:     IsVersionConflict(),
		Backoff:     BackoffNone,
	}

	attempts := 0
	result, err := WithRetry(context.Background(), config, func(attempt int) (string, error) {
		attempts++
		return "", &VersionConflictError{
			ExpectedVersion: 1,
			ActualVersion:   "2",
		}
	})

	require.Error(t, err)
	assert.Equal(t, "", result)
	assert.Equal(t, 3, attempts, "should exhaust all attempts")
	var vce *VersionConflictError
	assert.True(t, errors.As(err, &vce), "should return version conflict error")
}

func TestWithRetry_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	config := RetryConfig{
		MaxAttempts: 3,
		RetryIf:     IsVersionConflict(),
	}

	attempts := 0
	result, err := WithRetry(ctx, config, func(attempt int) (string, error) {
		attempts++
		return "", &VersionConflictError{
			ExpectedVersion: 1,
			ActualVersion:   "2",
		}
	})

	require.Error(t, err)
	assert.Equal(t, "", result)
	assert.Equal(t, 0, attempts, "should not execute when context is cancelled")
	assert.ErrorIs(t, err, context.Canceled)
}

func TestWithRetry_BackoffLinear(t *testing.T) {
	config := RetryConfig{
		MaxAttempts: 3,
		RetryIf:     IsVersionConflict(),
		Backoff:     BackoffLinear,
		BaseDelay:   50 * time.Millisecond,
	}

	start := time.Now()
	attempts := 0
	_, _ = WithRetry(context.Background(), config, func(attempt int) (string, error) {
		attempts++
		return "", &VersionConflictError{
			ExpectedVersion: 1,
			ActualVersion:   "2",
		}
	})
	elapsed := time.Since(start)

	assert.Equal(t, 3, attempts)
	// Should have 2 delays of 50ms each (between attempts 1-2 and 2-3)
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond, "should apply linear backoff")
}

func TestWithRetry_BackoffExponential(t *testing.T) {
	config := RetryConfig{
		MaxAttempts: 4,
		RetryIf:     IsVersionConflict(),
		Backoff:     BackoffExponential,
		BaseDelay:   50 * time.Millisecond,
	}

	start := time.Now()
	attempts := 0
	_, _ = WithRetry(context.Background(), config, func(attempt int) (string, error) {
		attempts++
		return "", &VersionConflictError{
			ExpectedVersion: 1,
			ActualVersion:   "2",
		}
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
		return "", &VersionConflictError{
			ExpectedVersion: 1,
			ActualVersion:   "2",
		}
	})

	require.Error(t, err)
	assert.Equal(t, "", result)
	assert.Equal(t, 1, attempts, "should not retry when RetryIf is nil")
}

func TestWithRetry_MaxAttemptsValidation(t *testing.T) {
	config := RetryConfig{
		MaxAttempts: 0, // Invalid
		RetryIf:     IsVersionConflict(),
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
		strategy BackoffStrategy
		attempt  int
		expected time.Duration
	}{
		{BackoffNone, 1, 0},
		{BackoffNone, 2, 0},
		{BackoffLinear, 1, 100 * time.Millisecond},
		{BackoffLinear, 2, 100 * time.Millisecond},
		{BackoffLinear, 3, 100 * time.Millisecond},
		{BackoffExponential, 1, 100 * time.Millisecond},
		{BackoffExponential, 2, 200 * time.Millisecond},
		{BackoffExponential, 3, 400 * time.Millisecond},
		{BackoffExponential, 4, 800 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(string(tt.strategy), func(t *testing.T) {
			actual := calculateBackoff(tt.strategy, baseDelay, tt.attempt)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestIsVersionConflict(t *testing.T) {
	condition := IsVersionConflict()

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name: "version conflict error",
			err: &VersionConflictError{
				ExpectedVersion: 1,
				ActualVersion:   "2",
			},
			expected: true,
		},
		{
			name:     "other error",
			err:      errors.New("some error"),
			expected: false,
		},
		{
			name:     "wrapped version conflict",
			err:      errors.New("wrapped: " + (&VersionConflictError{ExpectedVersion: 1, ActualVersion: "2"}).Error()),
			expected: false, // errors.As doesn't work with string wrapping
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, condition(tt.err))
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
			err:      errors.New("failed to parse configuration"),
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
		Backoff:     BackoffNone,
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
		Backoff:     BackoffNone,
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
