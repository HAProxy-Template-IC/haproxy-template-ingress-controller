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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventCollector_Register(t *testing.T) {
	t.Run("basic registration", func(t *testing.T) {
		c := NewEventCollector()
		require.NoError(t, c.Register("default", "route-a", "networking.k8s.io/v1", "Ingress",
			EventTypeWarning, "RouteConflict", "host x path / already served"))

		events := c.Events()
		require.Len(t, events, 1)
		assert.Equal(t, "default", events[0].Namespace)
		assert.Equal(t, "route-a", events[0].Name)
		assert.Equal(t, "networking.k8s.io/v1", events[0].APIVersion)
		assert.Equal(t, "Ingress", events[0].Kind)
		assert.Equal(t, EventTypeWarning, events[0].Type)
		assert.Equal(t, "RouteConflict", events[0].Reason)
		assert.Equal(t, "host x path / already served", events[0].Message)
	})

	t.Run("dedup identical events", func(t *testing.T) {
		c := NewEventCollector()
		for range 3 {
			require.NoError(t, c.Register("ns", "n", "networking.k8s.io/v1", "Ingress",
				EventTypeWarning, "RouteConflict", "same message"))
		}
		assert.Len(t, c.Events(), 1, "identical (resource, type, reason, message) tuples collapse to one")
	})

	t.Run("distinct messages are distinct events", func(t *testing.T) {
		c := NewEventCollector()
		require.NoError(t, c.Register("ns", "n", "networking.k8s.io/v1", "Ingress", EventTypeWarning, "RouteConflict", "path /a"))
		require.NoError(t, c.Register("ns", "n", "networking.k8s.io/v1", "Ingress", EventTypeWarning, "RouteConflict", "path /b"))
		assert.Len(t, c.Events(), 2)
	})

	t.Run("deterministic sorted output", func(t *testing.T) {
		c := NewEventCollector()
		require.NoError(t, c.Register("ns", "zzz", "networking.k8s.io/v1", "Ingress", EventTypeWarning, "RouteConflict", "m"))
		require.NoError(t, c.Register("ns", "aaa", "networking.k8s.io/v1", "Ingress", EventTypeWarning, "RouteConflict", "m"))
		events := c.Events()
		require.Len(t, events, 2)
		assert.Equal(t, "aaa", events[0].Name, "Events() is sorted by key regardless of registration order")
		assert.Equal(t, "zzz", events[1].Name)
	})

	t.Run("validation", func(t *testing.T) {
		c := NewEventCollector()
		assert.Error(t, c.Register("ns", "", "v1", "Ingress", EventTypeWarning, "R", "m"), "name required")
		assert.Error(t, c.Register("ns", "n", "", "Ingress", EventTypeWarning, "R", "m"), "apiVersion required")
		assert.Error(t, c.Register("ns", "n", "v1", "", EventTypeWarning, "R", "m"), "kind required")
		assert.Error(t, c.Register("ns", "n", "v1", "Ingress", EventTypeWarning, "", "m"), "reason required")
		assert.Error(t, c.Register("ns", "n", "v1", "Ingress", EventTypeWarning, "R", ""), "message required")
		assert.Error(t, c.Register("ns", "n", "v1", "Ingress", "Bogus", "R", "m"), "type must be Warning/Normal")
		assert.Empty(t, c.Events(), "no invalid event is stored")
	})

	t.Run("concurrent registration is race-free", func(t *testing.T) {
		c := NewEventCollector()
		var wg sync.WaitGroup
		for i := range 50 {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				// Half the goroutines register the same event (dedup), half unique.
				if i%2 == 0 {
					_ = c.Register("ns", "shared", "v1", "Ingress", EventTypeWarning, "RouteConflict", "shared")
				} else {
					_ = c.Register("ns", "uniq", "v1", "Ingress", EventTypeWarning, "RouteConflict", string(rune('a'+i)))
				}
			}(i)
		}
		wg.Wait()
		// 1 shared + 25 unique messages.
		assert.Len(t, c.Events(), 26)
	})
}

// TestScriggoRecordEvent_EndToEnd renders templates that call recordEvent() and
// verifies the collector wired into the render context — the same path the
// production renderer and testrunner use — including the best-effort contract
// that a bad call never fails the render.
func TestScriggoRecordEvent_EndToEnd(t *testing.T) {
	t.Run("valid call records one event", func(t *testing.T) {
		engine, err := New(map[string]string{
			"t": `{% recordEvent("team-a", "route-x", "networking.k8s.io/v1", "Ingress", "RouteConflict", "collision on /x") %}rendered`,
		}, nil)
		require.NoError(t, err)

		collector := NewEventCollector()
		out, err := engine.Render(context.Background(), "t", map[string]any{"recordEventCollector": collector})
		require.NoError(t, err)
		assert.Contains(t, out, "rendered")
		assert.NotContains(t, out, "RouteConflict", "recordEvent is side-effect only, it must not leak event content into output")

		events := collector.Events()
		require.Len(t, events, 1)
		assert.Equal(t, "team-a", events[0].Namespace)
		assert.Equal(t, "route-x", events[0].Name)
		assert.Equal(t, EventTypeWarning, events[0].Type)
		assert.Equal(t, "RouteConflict", events[0].Reason)
		assert.Equal(t, "collision on /x", events[0].Message)
	})

	t.Run("invalid arg (empty reason) does not fail the render", func(t *testing.T) {
		engine, err := New(map[string]string{
			"t": `{% recordEvent("ns", "n", "networking.k8s.io/v1", "Ingress", "", "msg") %}rendered`,
		}, nil)
		require.NoError(t, err)

		collector := NewEventCollector()
		out, err := engine.Render(context.Background(), "t", map[string]any{"recordEventCollector": collector})
		require.NoError(t, err, "a bad recordEvent arg must not abort the render (best-effort)")
		assert.Contains(t, out, "rendered")
		assert.Empty(t, collector.Events(), "the invalid event is dropped, not recorded")
	})

	t.Run("missing collector does not fail the render", func(t *testing.T) {
		engine, err := New(map[string]string{
			"t": `{% recordEvent("ns", "n", "networking.k8s.io/v1", "Ingress", "RouteConflict", "msg") %}rendered`,
		}, nil)
		require.NoError(t, err)

		out, err := engine.Render(context.Background(), "t", map[string]any{}) // no recordEventCollector
		require.NoError(t, err, "a missing collector must not abort the render (best-effort)")
		assert.Contains(t, out, "rendered")
	})
}
