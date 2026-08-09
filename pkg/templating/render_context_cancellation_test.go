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

package templating

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Renders driven by a CANCELLABLE context take a different path through the
// template engine than renders driven by context.Background(): the engine
// starts a cancellation watchdog per run, which the plain path never does.
//
// The controller always renders with a cancellable context — it bounds every
// render with a timeout. Nothing in this package did, so the whole offline
// suite was structurally blind to anything that only breaks on that path. A
// VM-lifecycle change in the engine's Scriggo fork crash-looped the controller
// on its first render with 6625 unit tests and 2526 chart assertions green;
// the tests below are what would have caught it.
//
// Keep at least one render here on a cancellable context, and keep at least
// one of them calling a template closure per element — that combination is
// what the offline suite was missing.

func TestRenderWithCancellableContext(t *testing.T) {
	const tpl = `{%%
  var out = eps |
    flat_map(func(s Slice) []EP { return s.Endpoints }) |
    reject(func(e EP) bool { return e.TargetRef.Name == "" }) |
    map(func(e EP) string { return e.TargetRef.Name })
%%}{{ join(out, ",") }}`

	engine, err := New(map[string]string{"t": tpl}, &Options{
		Declarations: pipelineDeclarations(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := engine.Render(ctx, "t", map[string]any{"eps": pipelineFixture()})
	require.NoError(t, err)
	assert.Equal(t, "pod-a,pod-a,pod-b\n", out)
}

// TestRenderWithCancellableContextConcurrently mirrors how the controller and
// the validation-test runner actually render: several goroutines, each with a
// cancellable context, sharing the engine. The engine pools VM state across
// renders, so a reset that misses a field only misbehaves once a recycled VM
// is handed to a second caller.
func TestRenderWithCancellableContextConcurrently(t *testing.T) {
	const tpl = `{%%
  var out = eps |
    flat_map(func(s Slice) []EP { return s.Endpoints }) |
    filter(func(e EP) bool { return e.Ready })
%%}{{ len(out) }}`

	engine, err := New(map[string]string{"t": tpl}, &Options{
		Declarations: pipelineDeclarations(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 25 {
				got, err := engine.Render(ctx, "t", map[string]any{"eps": pipelineFixture()})
				if err != nil {
					errs <- err
					return
				}
				if got != "3\n" {
					errs <- assert.AnError
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err, "concurrent renders on a cancellable context must agree")
	}
}

// TestRenderCancelledContextIsReported pins that cancellation still works.
// The crash this file exists for was in the cancellation watchdog, and the
// cheapest wrong fix is to stop watching — which no other test would notice.
//
// The render has to be long enough to observe the cancellation: the engine
// checks periodically, so a trivial template finishes first and legitimately
// succeeds.
func TestRenderCancelledContextIsReported(t *testing.T) {
	engine, err := New(map[string]string{"t": `{%%
  var total = 0
  for i := 0; i < 200000000; i++ { total = total + i }
%%}{{ total }}`}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		_, renderErr := engine.Render(ctx, "t", nil)
		done <- renderErr
	}()

	select {
	case renderErr := <-done:
		require.Error(t, renderErr, "a cancelled context must abort the render")
	case <-time.After(30 * time.Second):
		t.Fatal("render ignored a cancelled context — the watchdog is not running")
	}
}
