// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package statusapplier

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// isRetriable is the per-error policy that decides whether a failed
// status patch will be retried on the next reconciliation cycle vs
// declared permanent. The classification has FIVE meaningful branches
// and one default. Misclassifying any of them has on-call consequences:
//
//   - Permanent errors retried as transient → operators get paged for
//     the same NotFound/Forbidden over and over.
//   - Transient errors classified as permanent → status updates
//     silently disappear during a brief API blip.
//   - Default-to-retriable is the documented bias ("avoid silently
//     dropping updates") and a regression that flipped it would
//     silently drop status updates on every unknown error.

func TestIsRetriable_DispatchTable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		// Transient API server errors → must retry
		{
			name: "Timeout error → retriable (API blip recovery)",
			err:  apierrors.NewTimeoutError("timeout", 1),
			want: true,
		},
		{
			name: "ServerTimeout → retriable",
			err:  apierrors.NewServerTimeout(schema.GroupResource{Resource: "ingresses"}, "patch", 1),
			want: true,
		},
		{
			name: "ServiceUnavailable (503) → retriable",
			err:  apierrors.NewServiceUnavailable("503"),
			want: true,
		},
		{
			name: "TooManyRequests (429) → retriable",
			err:  apierrors.NewTooManyRequests("rate limited", 1),
			want: true,
		},
		{
			name: "InternalError → retriable",
			err:  apierrors.NewInternalError(errors.New("internal boom")),
			want: true,
		},

		// Permanent errors → must NOT retry. Pages on-call if we did.
		{
			name: "NotFound → permanent (resource was deleted)",
			err:  apierrors.NewNotFound(schema.GroupResource{Resource: "ingresses"}, "missing"),
			want: false,
		},
		{
			name: "Forbidden → permanent (RBAC won't change between retries)",
			err:  apierrors.NewForbidden(schema.GroupResource{Resource: "ingresses"}, "x", errors.New("denied")),
			want: false,
		},
		{
			name: "Invalid → permanent (bad payload won't fix itself)",
			err:  apierrors.NewInvalid(schema.GroupKind{Kind: "Ingress"}, "x", nil),
			want: false,
		},
		{
			name: "MethodNotSupported → permanent (operation not valid for this resource)",
			err:  apierrors.NewMethodNotSupported(schema.GroupResource{Resource: "ingresses"}, "patch"),
			want: false,
		},

		// Network-level transient
		{
			name: "net.Error with Timeout()=true → retriable",
			err:  &timeoutNetErr{},
			want: true,
		},
		{
			name: "net.Error with Timeout()=false → NOT retriable (early return from net branch)",
			// The net.Error branch returns netErr.Timeout() DIRECTLY
			// (it does not fall through to the default). Pin this so
			// a regression that flipped the return — e.g. `return
			// !netErr.Timeout()` — surfaces. The current behaviour
			// classifies "connection refused" as permanent, which is
			// correct: the server is actively rejecting connections,
			// not slow.
			err:  &nonTimeoutNetErr{},
			want: false,
		},

		// Context-level transient
		{
			name: "context.DeadlineExceeded → retriable",
			err:  context.DeadlineExceeded,
			want: true,
		},

		// Unknown error → defaults to retriable (documented bias).
		// A regression flipping this default would silently drop
		// updates on every unknown error.
		{
			name: "plain unknown error → retriable (documented default)",
			err:  errors.New("unknown failure mode"),
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isRetriable(tt.err),
				"isRetriable misclassification has on-call consequences: "+
					"permanent-as-transient pages on-call repeatedly; "+
					"transient-as-permanent silently drops updates during "+
					"API blips")
		})
	}
}

// timeoutNetErr satisfies net.Error and reports Timeout()=true so
// isRetriable's `errors.AsType[net.Error](err)` branch matches and the
// subsequent `netErr.Timeout()` check returns true.
type timeoutNetErr struct{}

func (timeoutNetErr) Error() string   { return "i/o timeout" }
func (timeoutNetErr) Timeout() bool   { return true }
func (timeoutNetErr) Temporary() bool { return false }

// nonTimeoutNetErr is the negative — exercises the branch where
// errors.AsType succeeds but Timeout() returns false. The net branch
// returns netErr.Timeout() directly, so this classifies as
// NOT retriable (permanent — e.g. connection refused).
type nonTimeoutNetErr struct{}

func (nonTimeoutNetErr) Error() string   { return "connection refused" }
func (nonTimeoutNetErr) Timeout() bool   { return false }
func (nonTimeoutNetErr) Temporary() bool { return false }

// Compile-time assertions that our test types satisfy net.Error.
var (
	_ net.Error = (*timeoutNetErr)(nil)
	_ net.Error = (*nonTimeoutNetErr)(nil)
)
