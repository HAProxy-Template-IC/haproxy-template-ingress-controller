// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package pipeline

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PipelineError is the structured error returned from Pipeline.Execute when
// a phase fails. Two pieces of behaviour are load-bearing for its callers:
//
//  1. Error() formatting: the validation phase carries an extra
//     ValidationPhase sub-tag (syntax/schema/semantic) so commentator
//     insights and metrics can attribute the failure precisely. The render
//     phase, plus any validation failure missing a sub-phase, falls back to
//     the simpler "<phase> failed: <cause>" form. A regression that flipped
//     these branches would silently change the user-visible message and the
//     phase-level grouping in the commentator.
//
//  2. Unwrap() returning the Cause: pkg/controller/reconciler/coordinator.go
//     uses `errors.AsType[*pipeline.PipelineError]` to extract the failed
//     phase from an arbitrarily-wrapped error chain so it can publish
//     ReconciliationFailedEvent with the right phase. A regression that
//     dropped Unwrap (or returned the wrong value) would leave coordinator
//     defaulting to "render" for every failure — a silent observability bug.
//
// These tests pin both contracts directly with synthetic errors, independent
// of the Pipeline.Execute code path so they don't break when rendering or
// validation internals are refactored.

func TestPipelineError_ErrorFormat(t *testing.T) {
	cause := errors.New("missing template")

	tests := []struct {
		name string
		err  *PipelineError
		want string
	}{
		{
			name: "render phase has no sub-phase tag",
			err: &PipelineError{
				Phase: PhaseRender,
				Cause: cause,
			},
			want: "render failed: missing template",
		},
		{
			name: "validation phase WITH sub-phase shows the sub-phase",
			// The interesting case: ValidationPhase is the syntax/schema/
			// semantic stage that failed. Commentator/metrics group failures
			// by this sub-phase, so it must appear in the error string.
			err: &PipelineError{
				Phase:           PhaseValidation,
				ValidationPhase: "syntax",
				Cause:           cause,
			},
			want: "validation failed in syntax phase: missing template",
		},
		{
			name: "validation phase WITHOUT sub-phase falls back to simple form",
			// Defensive: if the validator returns a Valid=false result with
			// an empty Phase string, we still produce a sensible message
			// rather than a stray empty phase tag like "in  phase".
			err: &PipelineError{
				Phase: PhaseValidation,
				Cause: cause,
			},
			want: "validation failed: missing template",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.err.Error(),
				"Error() formatting is part of the public contract; "+
					"a regression that swapped the branches would change "+
					"both user-visible logs and any string-matching downstream")
		})
	}
}

func TestPipelineError_UnwrapReturnsCause(t *testing.T) {
	// Direct Unwrap() call: this is the foundation that errors.Is/As/AsType
	// builds on. If Unwrap returns the wrong value (or nil), every chain
	// walk that bottoms out at this error will miss the underlying cause.
	cause := errors.New("root cause")
	pe := &PipelineError{
		Phase: PhaseRender,
		Cause: cause,
	}

	assert.Same(t, cause, pe.Unwrap(),
		"Unwrap must return the exact Cause pointer; coordinator.go's "+
			"errors.AsType walk and any errors.Is(err, sentinel) check rely "+
			"on this to traverse past PipelineError to the underlying error")
}

func TestPipelineError_ErrorsAsTypeFindsThroughWrap(t *testing.T) {
	// Integration with stdlib errors.AsType — this is the actual call site
	// in pkg/controller/reconciler/coordinator.go. We wrap PipelineError in
	// another fmt.Errorf("%w") layer to simulate the kind of additional
	// wrapping that may happen between Pipeline.Execute and the coordinator,
	// then verify AsType can still pull the structured error back out with
	// its Phase intact.
	original := &PipelineError{
		Phase:           PhaseValidation,
		ValidationPhase: "semantic",
		Cause:           errors.New("haproxy -c rejected the config"),
	}
	wrapped := fmt.Errorf("pipeline failed: %w", original)

	got, ok := errors.AsType[*PipelineError](wrapped)
	require.True(t, ok,
		"errors.AsType must find *PipelineError through %%w wrapping; "+
			"this is the exact call coordinator.go makes to attribute the "+
			"failed phase in ReconciliationFailedEvent — a regression here "+
			"would default every wrapped failure to the 'render' phase")
	assert.Equal(t, PhaseValidation, got.Phase)
	assert.Equal(t, "semantic", got.ValidationPhase)
}

func TestPipelineError_ErrorsIsFindsCauseSentinel(t *testing.T) {
	// Belt-and-suspenders: errors.Is also has to walk through Unwrap to
	// find a sentinel error stashed in Cause. This protects against a
	// regression where someone accidentally wraps Cause in a non-unwrapping
	// transform (e.g., turning it into a string and re-creating an error).
	sentinel := errors.New("sentinel")
	pe := &PipelineError{
		Phase: PhaseRender,
		Cause: fmt.Errorf("render step 3: %w", sentinel),
	}

	require.True(t, errors.Is(pe, sentinel),
		"errors.Is must traverse PipelineError.Unwrap() to find sentinel "+
			"errors stashed two layers down; without this, callers can't "+
			"match on package-defined sentinel errors after a pipeline failure")
}
