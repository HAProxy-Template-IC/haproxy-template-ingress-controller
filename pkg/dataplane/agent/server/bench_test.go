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

package server_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

// BenchmarkMapOnlyApply measures the agent's overhead for the class that has
// to be cheapest: one map file changes, one runtime command applies it, no
// reload. The design budget is under 3 ms per apply.
func BenchmarkMapOnlyApply(b *testing.B) {
	h := newHarness(b)
	config := "global\n" + strings.Repeat("# padding\n", 1000)
	entries := mapBody(2000)

	files := []file{
		{Path: configPath, Content: config, Reload: true},
		{Path: "maps/host.map", Content: entries},
	}
	m := buildManifest("plan-0", files)
	m.Mode = api.ModeReload
	previous := h.apply(&m, files)
	require.True(b, previous.OK)

	for i := 0; b.Loop(); i++ {
		files[1].Content = entries + fmt.Sprintf("bench%d.example.com be-a\n", i)
		next := buildManifest(fmt.Sprintf("plan-%d", i+1), files)
		next.ExpectedPrevPlanID = previous.AppliedPlanID
		next.ExpectedPrevToken = previous.AppliedToken
		next.Ops = []api.Op{{
			Kind: api.OpMapAdd, Path: "maps/host.map",
			Key: fmt.Sprintf("bench%d.example.com", i), Value: "be-a",
		}}
		previous = h.apply(&next, files)
		if !previous.OK {
			b.Fatalf("apply %d failed: %+v", i, previous.Error)
		}
	}
}

// BenchmarkOneMegabyteConfigWrite measures the write half alone: a full
// haproxy.cfg lands and the pod reloads. The design budget is under 15 ms of
// agent overhead, the reload itself excluded by the fake socket.
func BenchmarkOneMegabyteConfigWrite(b *testing.B) {
	h := newHarness(b)
	filler := strings.Repeat("# a line of configuration padding to reach a megabyte\n", 19000)

	files := []file{{Path: configPath, Content: "global\n" + filler, Reload: true}}
	m := buildManifest("plan-0", files)
	m.Mode = api.ModeReload
	previous := h.apply(&m, files)
	require.True(b, previous.OK)

	for i := 0; b.Loop(); i++ {
		files[0].Content = fmt.Sprintf("global\n  maxconn %d\n", i) + filler
		next := buildManifest(fmt.Sprintf("plan-%d", i+1), files)
		next.Mode = api.ModeReload
		next.ExpectedPrevPlanID = previous.AppliedPlanID
		next.ExpectedPrevToken = previous.AppliedToken
		previous = h.apply(&next, files)
		if !previous.OK {
			b.Fatalf("apply %d failed: %+v", i, previous.Error)
		}
	}
}

func mapBody(entries int) string {
	var b strings.Builder
	for i := range entries {
		fmt.Fprintf(&b, "host%04d.example.com be-%04d\n", i, i)
	}
	return b.String()
}
