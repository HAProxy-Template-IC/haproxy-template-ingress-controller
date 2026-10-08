// Copyright 2026 Philipp Hossner
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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

type heldValidationKey struct{}

type blockingValidationExecutor struct {
	started chan struct{}
	release chan struct{}
}

func (e *blockingValidationExecutor) Version(context.Context) (string, error) {
	return "HAProxy version 3.0.29", nil
}

func (e *blockingValidationExecutor) Check(ctx context.Context, _ string, _ ...string) ([]byte, error) {
	if ctx.Value(heldValidationKey{}) == true {
		close(e.started)
		select {
		case <-e.release:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	return []byte("[ALERT] invalid configuration in validation-gate regression"), errors.New("HAProxy refused configuration")
}

func TestFullValidationGatesDoNotBlockEachOther(t *testing.T) {
	for _, blocked := range []string{"input acceptance", "admission"} {
		t.Run(blocked, func(t *testing.T) {
			executor := &blockingValidationExecutor{started: make(chan struct{}), release: make(chan struct{})}
			t.Cleanup(dataplane.SetHAProxyExecutor(executor))
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			cfg := &coreconfig.Config{}
			services := map[string]*validation.ValidationService{
				"input acceptance": newFullValidator(cfg, logger),
				"admission":        newFullValidator(cfg, logger),
			}
			other := "admission"
			if blocked == other {
				other = "input acceptance"
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			done := make(chan *validation.ValidationResult, 1)
			go func() {
				done <- services[blocked].ValidateWithChecksum(context.WithValue(ctx, heldValidationKey{}, true), "global\n", nil, "same-content")
			}()
			select {
			case <-executor.started:
			case <-ctx.Done():
				t.Fatal("first validation did not reach HAProxy")
			}
			result := services[other].ValidateWithChecksum(ctx, "global\n", nil, "same-content")
			close(executor.release)
			first := <-done
			require.NoError(t, ctx.Err(), "independent validation queued behind %s", blocked)
			for _, verdict := range []*validation.ValidationResult{first, result} {
				assert.False(t, verdict.Valid)
				require.ErrorIs(t, verdict.Error, dataplane.ErrHAProxyRefused)
			}
		})
	}
}
