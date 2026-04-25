// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package executors

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dispatchAndCheck is the tiny wrapper EVERY executor function in
// this package calls after issuing its version-specific HTTP request.
// It collapses the repeated pattern:
//
//	if err != nil { return err }
//	defer resp.Body.Close()
//	return client.CheckResponse(resp, description)
//
// into one call. The contract is small but load-bearing — every
// executor relies on it. Two branches must hold:
//
//  1. err != nil short-circuit: the function returns the error
//     verbatim AND does NOT touch resp. This matters because when a
//     dispatch call returns (nil, err), reading resp.Body would
//     panic. A refactor that swapped the order of the err check and
//     defer would silently introduce nil-deref panics across every
//     executor.
//
//  2. err == nil happy/sad path: the function MUST close the body
//     (otherwise we leak a connection per executor call), AND it
//     MUST forward CheckResponse's verdict — success returns nil,
//     failure returns an error containing the operation description
//     so log scrapers can correlate.

// closeTrackingBody is an io.ReadCloser that records whether Close()
// was invoked. Used to verify dispatchAndCheck honours the deferred
// close on the success path.
type closeTrackingBody struct {
	io.Reader
	closed atomic.Bool
}

func (c *closeTrackingBody) Close() error {
	c.closed.Store(true)
	return nil
}

func TestDispatchAndCheck_NilErr_Success(t *testing.T) {
	body := &closeTrackingBody{Reader: strings.NewReader("ok")}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       body,
	}

	err := dispatchAndCheck(resp, nil, "test op")

	require.NoError(t, err, "2xx status must yield nil error")
	assert.True(t, body.closed.Load(),
		"response body MUST be closed on the success path; "+
			"otherwise every executor would leak a TCP connection per call")
}

func TestDispatchAndCheck_NilErr_BadStatusCloses(t *testing.T) {
	// Even when CheckResponse reports an error (non-2xx), the body
	// must still be closed. The defer is the only thing guaranteeing
	// this — a refactor that moved the close into the success branch
	// would silently leak connections on every API failure.
	body := &closeTrackingBody{Reader: strings.NewReader(`{"error":"not found"}`)}
	resp := &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       body,
	}

	err := dispatchAndCheck(resp, nil, "create backend")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "create backend",
		"error must include the operation description so log scrapers can correlate the failure to the executor that produced it")
	assert.Contains(t, err.Error(), "404",
		"error must include the HTTP status code so operators can quickly classify the failure")
	assert.True(t, body.closed.Load(),
		"response body MUST be closed even on the failure path; "+
			"the defer is the only thing guaranteeing this — moving it would leak connections on every API failure")
}

func TestDispatchAndCheck_NonNilErr_ShortCircuits(t *testing.T) {
	// When the dispatch call itself fails, resp is nil. The function
	// MUST short-circuit on the err check BEFORE touching resp,
	// otherwise the deferred resp.Body.Close() would panic with a
	// nil-pointer deref.
	dispatchErr := errors.New("connection refused")

	require.NotPanics(t, func() {
		err := dispatchAndCheck(nil, dispatchErr, "test op")
		require.Error(t, err)
		assert.Same(t, dispatchErr, err,
			"the dispatch error MUST be returned verbatim (same pointer) so callers can errors.Is/As against it")
	})
}

func TestDispatchAndCheck_PreservesErrorIdentity(t *testing.T) {
	// The contract says errors propagate unchanged. Verify with a
	// custom error type that callers might type-assert on.
	custom := &sentinelErr{msg: "bespoke failure"}

	err := dispatchAndCheck(nil, custom, "anything")

	var asSentinel *sentinelErr
	require.True(t, errors.As(err, &asSentinel),
		"custom error types must propagate unchanged so callers can errors.As against them; "+
			"any wrapping in dispatchAndCheck would break that contract")
	assert.Equal(t, "bespoke failure", asSentinel.msg)
}

// sentinelErr is a custom error type used to verify that
// dispatchAndCheck propagates errors unchanged so callers can
// errors.As against them.
type sentinelErr struct{ msg string }

func (e *sentinelErr) Error() string { return e.msg }
