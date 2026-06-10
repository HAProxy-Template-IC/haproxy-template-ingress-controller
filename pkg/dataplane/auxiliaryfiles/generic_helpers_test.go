// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package auxiliaryfiles

import (
	"context"
	"errors"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isAlreadyExistsError gates the Create→Update fallback that recovers from
// the "file is on disk but missing from the storage listing" condition that
// happens after a raw config push + reload. The classifier is a substring
// check, so pin the patterns it must accept and the negatives it must reject
// — a future refactor that switched to errors.Is/errors.As without a
// replacement would silently break the recovery.
func TestIsAlreadyExistsError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error is not 'already exists'", err: nil, want: false},
		{name: "exact 'already exists' message", err: errors.New("already exists"), want: true},
		{name: "API error wrapping 'already exists'", err: errors.New("HTTP 409: file 'x' already exists in storage"), want: true},
		{name: "wrapped error preserves substring", err: errors.New("create failed: already exists in storage"), want: true},
		{name: "different conflict text is rejected", err: errors.New("HTTP 409: conflict"), want: false},
		{name: "case mismatch is rejected (Contains is case-sensitive)", err: errors.New("Already Exists"), want: false},
		{name: "unrelated error is rejected", err: errors.New("network timeout"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAlreadyExistsError(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// clientFileOps.apiID normalizes the controller-side identifier before each
// per-id API call so storage APIs that take a filename (rather than a full
// path) get the right value. The nil-hook fast path is the default for
// non-storage identifiers — pin both branches so a future refactor can't
// silently drop the basename normalization that crtlist/general-file paths
// rely on.
func TestClientFileOps_ApiID(t *testing.T) {
	t.Run("no idForAPI hook returns input verbatim", func(t *testing.T) {
		o := &clientFileOps[GeneralFile]{}
		assert.Equal(t, "/etc/haproxy/files/400.http", o.apiID("/etc/haproxy/files/400.http"))
	})

	t.Run("idForAPI hook is applied to the input", func(t *testing.T) {
		o := &clientFileOps[GeneralFile]{idForAPI: path.Base}
		assert.Equal(t, "400.http", o.apiID("/etc/haproxy/files/400.http"))
	})

	t.Run("idForAPI hook is applied even for already-bare ids", func(t *testing.T) {
		o := &clientFileOps[GeneralFile]{idForAPI: path.Base}
		assert.Equal(t, "400.http", o.apiID("400.http"))
	})
}

// clientFileOps.Create must fall back to Update when the underlying create
// reports "already exists". This is the recovery path after a raw push +
// reload where the storage listing is stale but the file is on disk.
// Use the controller-side id consistently; apiID is exercised via the
// fakes so we also pin that the fallback re-runs through Update (which
// re-applies apiID).
func TestClientFileOps_Create_FallsBackToUpdateOnAlreadyExists(t *testing.T) {
	var createCalls, updateCalls int
	o := &clientFileOps[GeneralFile]{
		idForAPI: path.Base,
		create: func(_ context.Context, id, content string) (string, error) {
			createCalls++
			assert.Equal(t, "400.http", id, "create must receive the apiID-normalized id")
			assert.Equal(t, "BODY", content)
			return "", errors.New("HTTP 409: file already exists")
		},
		update: func(_ context.Context, id, content string) (string, error) {
			updateCalls++
			assert.Equal(t, "400.http", id, "update fallback must also see the apiID-normalized id")
			assert.Equal(t, "BODY", content)
			return "reload-1", nil
		},
	}

	reloadID, err := o.Create(context.Background(), "/etc/haproxy/files/400.http", "BODY")
	require.NoError(t, err)
	assert.Equal(t, "reload-1", reloadID)
	assert.Equal(t, 1, createCalls, "create must be attempted exactly once")
	assert.Equal(t, 1, updateCalls, "update fallback must run exactly once")
}

func TestClientFileOps_Create_PassesThroughOtherErrors(t *testing.T) {
	createErr := errors.New("HTTP 500: backend error")
	var updateCalls int
	o := &clientFileOps[GeneralFile]{
		create: func(_ context.Context, _, _ string) (string, error) {
			return "", createErr
		},
		update: func(_ context.Context, _, _ string) (string, error) {
			updateCalls++
			return "", nil
		},
	}

	reloadID, err := o.Create(context.Background(), "x", "y")
	assert.Equal(t, "", reloadID)
	assert.ErrorIs(t, err, createErr, "non-AlreadyExists errors must surface unchanged")
	assert.Equal(t, 0, updateCalls, "update must NOT run for non-AlreadyExists errors")
}
