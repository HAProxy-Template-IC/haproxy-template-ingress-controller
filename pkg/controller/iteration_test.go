// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"golang.org/x/sync/errgroup"
)

func TestBuildAndRegisterPluggableValidatorManagerRejectsMalformedGlob(t *testing.T) {
	setup := &componentSetup{}
	cfg := &coreconfig.Config{
		Validators: []coreconfig.ValidatorConfig{{
			Name:       "spoa-hub",
			SocketPath: "/var/run/haptic-validators/spoa-hub.sock",
			Files:      []string{"general/[broken"},
		}},
	}

	mgr, cleanup, err := buildAndRegisterPluggableValidatorManager(setup, cfg, nil)
	require.Error(t, err)
	assert.Nil(t, mgr)
	assert.Nil(t, cleanup)
	assert.ErrorContains(t, err, "invalid file glob")
}

func TestFinishIterationStartupRejectsCanceledIteration(t *testing.T) {
	iterCtx, cancelCause := context.WithCancelCause(t.Context())
	failure := errors.New("required component failed")
	cancelCause(failure)
	state := &configState{}
	infra := &persistentInfra{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	err := finishIterationStartup(&componentSetup{IterCtx: iterCtx}, state, infra, logger)
	require.ErrorIs(t, err, failure)
	assert.False(t, state.IsInitialized())
	infra.graceMu.Lock()
	assert.False(t, infra.everInitialized)
	infra.graceMu.Unlock()
}

func TestWaitForIterationExitReturnsCancellationCause(t *testing.T) {
	iterCtx, cancelCause := context.WithCancelCause(t.Context())
	failure := errors.New("required component failed")
	cancelCause(failure)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	err := waitForIterationExit(&componentSetup{IterCtx: iterCtx}, logger)
	require.ErrorIs(t, err, failure)
}

func TestCompleteIterationPreservesCancellationCause(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	deliveryErr := &criticalEventDeliveryError{drop: busevents.DropInfo{
		SubscriberName: "blocked",
		EventType:      "test.delivery",
	}}
	tests := []struct {
		name      string
		cause     error
		stageErr  error
		want      error
		wantTyped bool
	}{
		{name: "typed cause replaces generic cancellation", cause: deliveryErr, stageErr: context.Canceled, want: context.Canceled, wantTyped: true},
		{name: "ordinary cancellation remains ordinary", cause: context.Canceled, stageErr: context.Canceled, want: context.Canceled},
		{name: "unrelated stage error remains", stageErr: errors.New("stage failed")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent, cancelCause := context.WithCancelCause(t.Context())
			group, groupCtx := errgroup.WithContext(parent)
			setup := &componentSetup{
				IterCtx:  groupCtx,
				Cancel:   func() { cancelCause(nil) },
				ErrGroup: group,
			}
			if test.cause != nil {
				cancelCause(test.cause)
			}

			err := completeIteration(setup, test.stageErr, logger)
			if test.want != nil {
				require.ErrorIs(t, err, test.want)
			}
			if test.stageErr != nil {
				require.ErrorIs(t, err, test.stageErr)
			}
			var typed *criticalEventDeliveryError
			assert.Equal(t, test.wantTyped, errors.As(err, &typed))
		})
	}
}
