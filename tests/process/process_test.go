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

package process

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutorRejectsInvalidOrCancelledWorkBeforeStarting(t *testing.T) {
	_, err := (Executor{}).Start(t.Context(), &Command{})
	require.ErrorContains(t, err, "executable is required")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = (Executor{}).Start(ctx, &Command{Name: "must-not-run"})
	require.ErrorIs(t, err, context.Canceled)
}

func TestCapturedOutputCanBeReadDuringConcurrentWrites(t *testing.T) {
	var buffer outputBuffer
	var workers sync.WaitGroup
	for range 10 {
		workers.Go(func() {
			for range 20 {
				_, err := buffer.Write([]byte("complete line\n"))
				assert.NoError(t, err)
				_ = buffer.String()
			}
		})
	}
	workers.Wait()
	assert.Equal(t, strings.Repeat("complete line\n", 200), buffer.String())
}

func TestCapturePreservesSeparateStream(t *testing.T) {
	var captured, streamed bytes.Buffer
	_, err := capture(&captured, &streamed).Write([]byte("output"))
	require.NoError(t, err)
	assert.Equal(t, "output", captured.String())
	assert.Equal(t, "output", streamed.String())
}

func TestEnvironmentKeepsLiteralValues(t *testing.T) {
	assert.Equal(t, []string{"A=a=b", "B=$(never-execute)"}, environment(map[string]string{"B": "$(never-execute)", "A": "a=b"}))
}
