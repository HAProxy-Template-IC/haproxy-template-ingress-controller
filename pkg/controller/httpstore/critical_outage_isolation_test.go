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

package httpstore_test

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/haproxy-haptic/HAPTIC/pkg/controller/events"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/inputisolation"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/pipeline"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/rendercontext"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/testutil"
	"github.com/haproxy-haptic/HAPTIC/pkg/stores"
)

// readCountingProvider counts input isolation's reads of the watched stores,
// which it makes once per captured candidate.
type readCountingProvider struct {
	stores.StoreProvider
	reads atomic.Int64
}

func (p *readCountingProvider) GetStore(name string) stores.Store {
	p.reads.Add(1)
	return p.StoreProvider.GetStore(name)
}

// Issue #354: a replica without accepted inputs captured and applied the whole
// watched set on every trigger while its render was held.
func TestHeldRenderCapturesNoInputsWithoutAcceptedBaseline(t *testing.T) {
	// The leader's deploy renders withhold unaccepted content; its acceptance
	// attempts and the follower's renders do not.
	for _, leader := range []bool{false, true} {
		t.Run(map[bool]string{false: "follower", true: "leader"}[leader], func(t *testing.T) {
			h := newOutageHarness(t)
			selector := inputisolation.New(h.pipeline, h.watches, nil)
			observed := &readCountingProvider{StoreProvider: h.provider}
			execute := func() (*pipeline.PipelineResult, error) {
				return selector.Execute(t.Context(), observed, rendercontext.RenderModeReconcile)
			}
			deploy := execute
			if leader {
				deploy = func() (*pipeline.PipelineResult, error) {
					return selector.Execute(pipeline.WithPendingContentWithheld(t.Context()), observed, rendercontext.RenderModeReconcile)
				}
			}

			_, err := execute()
			require.True(t, pipeline.CriticalContentUnavailable(err), "the first render reports the unavailable source: %v", err)
			require.False(t, pipeline.RenderHeld(err))
			captured := observed.reads.Load()
			require.Positive(t, captured)

			for i := range 10 {
				h.changeRoute(t, "-"+string(rune('a'+i)))
				_, err := deploy()
				require.True(t, pipeline.RenderHeld(err), "trigger %d: %v", i, err)
				require.True(t, pipeline.CriticalContentUnavailable(err))
			}
			assert.Equal(t, captured, observed.reads.Load(), "held triggers must not capture the watched set")
			_, _, ready := selector.Snapshot()
			assert.False(t, ready, "nothing is accepted while the source is down")

			h.component.ProbeOutagesNow()
			recheck := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, h.triggers, testutil.EventTimeout)
			assert.Equal(t, "critical_http_content_recheck", recheck.Reason)
			_, err = execute()
			require.True(t, pipeline.CriticalContentUnavailable(err))
			require.False(t, pipeline.RenderHeld(err), "skipped inputs are re-checked once per refetch")
			assert.Equal(t, 2*captured, observed.reads.Load(), "the re-check captures once")

			h.up.Store(true)
			h.component.ProbeOutagesNow()
			recovered := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, h.triggers, testutil.EventTimeout)
			assert.Equal(t, "critical_http_content_available", recovered.Reason)
			h.changeRoute(t, "!")
			result, err := execute()
			require.NoError(t, err, "the trigger after recovery accepts the inputs")
			assert.Equal(t, "a=page!\n", result.HAProxyConfig, "the latest watched revision is accepted")
			accepted, _, ready := selector.Snapshot()
			require.True(t, ready)
			items, err := accepted.GetStore("routes").List()
			require.NoError(t, err)
			assert.Len(t, items, 1)
		})
	}
}
