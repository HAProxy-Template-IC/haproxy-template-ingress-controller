// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package store

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// validateKeyCount is the shared key-count guard that MemoryStore and
// CachedStore funnel every Add / Update / Delete through to enforce
// the per-store index-key contract. Pin every branch and the wrapped-
// error structure callers depend on.
func TestValidateKeyCount(t *testing.T) {
	tests := []struct {
		name    string
		op      string
		keys    []string
		want    int
		wantErr bool
		wantOp  string
	}{
		{
			name: "exact match returns nil",
			op:   "add", keys: []string{"default", "x"}, want: 2,
			wantErr: false,
		},
		{
			name: "expecting 0 keys with empty input returns nil",
			op:   "delete", keys: nil, want: 0,
			wantErr: false,
		},
		{
			name: "too few keys returns wrapped StoreError",
			op:   "add", keys: []string{"default"}, want: 2,
			wantErr: true, wantOp: "add",
		},
		{
			name: "too many keys returns wrapped StoreError",
			op:   "update", keys: []string{"a", "b", "c"}, want: 2,
			wantErr: true, wantOp: "update",
		},
		{
			name: "empty keys when expecting 1 returns wrapped StoreError",
			op:   "delete", keys: nil, want: 1,
			wantErr: true, wantOp: "delete",
		},
		{
			name: "operation name is propagated verbatim into the error",
			op:   "custom-op", keys: []string{"x"}, want: 2,
			wantErr: true, wantOp: "custom-op",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateKeyCount(tt.op, tt.keys, tt.want)
			if !tt.wantErr {
				assert.NoError(t, err)
				return
			}

			// Must wrap a *StoreError so callers can errors.As it.
			var se *StoreError
			if assert.True(t, errors.As(err, &se), "validateKeyCount must return *StoreError on mismatch") {
				assert.Equal(t, tt.wantOp, se.Operation, "Operation field must propagate the caller's op string")
				assert.Equal(t, tt.keys, se.Keys, "Keys field must propagate the offending input")
				assert.NotNil(t, se.Cause, "Cause must describe the count mismatch")
			}

			// And the error message must mention the operation so logs
			// can pinpoint which call rejected the keys.
			assert.Contains(t, err.Error(), tt.wantOp,
				"error string must mention the operation name")
		})
	}
}
