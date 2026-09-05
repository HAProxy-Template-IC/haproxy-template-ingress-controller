// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/scriggo"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestBundledRenderedRootsHaveExactCycleProtocols(t *testing.T) {
	cfg, setup, _, cleanup := bundledChartSetup(t)
	defer cleanup()
	extraction := helpers.ExtractTemplatesFromConfig(cfg)
	private := make(map[string]struct{}, len(extraction.IncrementalEntryPoints)+len(extraction.IncrementalBindingEntryPoints))
	for _, name := range extraction.IncrementalEntryPoints {
		private[name] = struct{}{}
	}
	for _, name := range extraction.IncrementalBindingEntryPoints {
		private[name] = struct{}{}
	}
	roots := make([]string, 0, len(extraction.EntryPoints)-len(private))
	for _, name := range extraction.EntryPoints {
		if _, isPrivate := private[name]; !isPrivate {
			roots = append(roots, name)
		}
	}
	preparer, ok := setup.Engine.(interface {
		PrepareExactCycleReplay([]string) (*templating.ExactCycleReplayProgram, error)
	})
	require.True(t, ok)
	program, err := preparer.PrepareExactCycleReplay(roots)
	if err != nil {
		introspector := setup.Engine.(interface {
			EntryPointUsedNativeValueAccesses(string) []scriggo.UsedNativeValueAccess
		})
		for _, name := range roots {
			for _, access := range introspector.EntryPointUsedNativeValueAccesses(name) {
				t.Logf("%s: %#v", name, access)
			}
		}
	}
	require.NoError(t, err)
	require.NotNil(t, program)
	requiresAllRoots, err := program.RequiresUnchangedInputRoots()
	require.NoError(t, err)
	require.False(t, requiresAllRoots)
}
