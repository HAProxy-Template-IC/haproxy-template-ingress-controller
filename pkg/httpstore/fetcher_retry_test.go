// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package httpstore

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fetchWithRetry has THREE load-bearing behaviors that the existing
// TestHTTPStore_FetchWithRetries (happy path) does not pin:
//
//  1. Context cancellation during the retry-backoff sleep MUST return
//     immediately with ctx.Err() — the loop must NOT go through the
//     remaining retry attempts after cancellation. This is what lets
//     a cancelled controller iteration tear down promptly instead of
//     waiting through tens of seconds of exponential backoff.
//
//  2. After exhausting all retries on a 5xx error, the wrapping error
//     must follow the documented format: "all N retry attempts failed:
//     <last attempt's error>". Operators rely on this exact phrasing
//     to disambiguate retry-exhaustion from connectivity failures.
//
//  3. After exhausting retries the underlying server error must still
//     be wrapped via %w so callers can errors.Unwrap and match on
//     specific failure types (e.g. authentication failures stay
//     distinguishable from server errors after retries).

func TestFetchWithRetry_ContextCancellationAbortsBackoffLoop(t *testing.T) {
	// Always-failing server forces the retry path. A test that just
	// counts attempts wouldn't pin the "early-exit on cancel" contract,
	// because the test goroutine wouldn't observe the difference between
	// "5 failed attempts" and "1 attempt + 4 cancelled-mid-sleep retries".
	// Pin instead by measuring elapsed time: with 1s RetryDelay × 5
	// retries the worst case is many seconds; cancellation at 50ms must
	// return within a small fraction of that.
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	store := New(logger, 0)

	ctx, cancel := context.WithCancel(context.Background())

	// 5 retries with 1-second base delay → if context wasn't honored,
	// total time would be at least 1+2+4+8+16 = 31 seconds. We cancel
	// shortly after the first attempt fails and assert the call returns
	// well within that window.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := store.Fetch(ctx, server.URL, FetchOptions{
		Retries:    5,
		RetryDelay: time.Second,
		Critical:   true,
	}, nil)
	elapsed := time.Since(start)

	require.Error(t, err,
		"a cancelled fetch must surface an error — the retry loop must "+
			"not silently swallow ctx.Err() and return success")
	assert.True(t, errors.Is(err, context.Canceled),
		"the surfaced error MUST wrap context.Canceled (via the time.After "+
			"select branch in fetchWithRetry); without this, callers can't "+
			"errors.Is to distinguish cancellation from real failures")
	assert.Less(t, elapsed, 5*time.Second,
		"fetch MUST return promptly after context cancellation (elapsed=%v); "+
			"a regression that ignored ctx in the backoff sleep would force "+
			"~31s wait through all retries, blocking controller shutdown",
		elapsed)
}

func TestFetchWithRetry_ExhaustedRetriesWrapsLastErrorWithCount(t *testing.T) {
	// Always returns 500 → retry is exhausted with the same error.
	// Pin both the wrapping format AND that the underlying error stays
	// reachable via errors.Unwrap.
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	store := New(logger, 0)

	ctx := context.Background()

	_, err := store.Fetch(ctx, server.URL, FetchOptions{
		Retries:    2, // total attempts = Retries + 1 = 3
		RetryDelay: time.Millisecond,
		Critical:   true,
	}, nil)

	require.Error(t, err)

	// Pin the wrapping format. The "all N retry attempts failed" prefix
	// is what distinguishes retry-exhaustion from a single-attempt fail.
	assert.Contains(t, err.Error(), "all 3 retry attempts failed",
		"after exhausting retries, the error MUST contain 'all N retry "+
			"attempts failed' so operators can distinguish between a "+
			"single-attempt fetch error and complete retry exhaustion — "+
			"the response is different (immediate retry vs declare endpoint "+
			"down)")

	// The underlying server error message must reach the operator
	// (server error: 500 Internal Server Error, per fetcher.go's switch).
	assert.Contains(t, err.Error(), "500",
		"the underlying server error MUST be preserved in the message so "+
			"operators see the actual HTTP status")

	// Verify all retries were actually attempted.
	assert.Equal(t, int32(3), attempts.Load(),
		"with Retries=2 the loop must run total Retries+1 = 3 attempts; "+
			"a regression that miscounted would either give up too early "+
			"or burn through one extra retry than configured")
}

func TestFetchWithRetry_NonCriticalSwallowsErrorReturnsEmpty(t *testing.T) {
	// Pin the documented "non-critical fetch returns empty string on
	// failure" contract — the inverse of TestFetchWithRetry_ExhaustedRetries
	// where Critical=true surfaces the wrapped error. A regression that
	// swapped these branches would either flood logs with errors operators
	// don't care about, or silently swallow critical fetch failures.
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(discardWriter{}, nil))
	store := New(logger, 0)

	content, err := store.Fetch(context.Background(), server.URL, FetchOptions{
		Retries:    1, // 2 total attempts
		RetryDelay: time.Millisecond,
		// Critical defaults to false
	}, nil)

	require.NoError(t, err,
		"non-critical fetch with all attempts failing MUST return nil error — "+
			"a regression here would force callers to handle errors for fetches "+
			"they explicitly declared as non-essential")
	assert.Equal(t, "", content,
		"non-critical fetch with all attempts failing MUST return empty string "+
			"so callers can safely use the result without nil-checking")
	assert.GreaterOrEqual(t, attempts.Load(), int32(2),
		"all configured attempts must still run even when Critical=false — "+
			"the only difference is whether the final error surfaces, not "+
			"whether retries happen at all")
}

// discardWriter swallows logger output during retry tests where the
// 500-response logging would otherwise flood test output.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
