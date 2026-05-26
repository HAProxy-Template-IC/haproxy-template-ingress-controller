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

package deployer

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// TestPickCachedParsedConfig_PrefersPostSyncActual is the architectural
// regression test for the cross-pod cache divergence bug: when the
// orchestrator returns a SyncResult with PostSyncParsedConfig set (= the
// pod's ACTUAL post-sync state, fetched and parsed by the orchestrator
// after a sync that applied operations), the deployer MUST cache that
// pointer — not the caller's input desired. Caching desired hides
// post-sync byte-divergence between pods that reached "logically
// desired" from different starting baselines (e.g. pod A synced twice
// vs pod B synced once during a rolling Deployment).
//
// If a future refactor flips the preference, drift_prevention will
// stop detecting cross-pod divergence and silent broken-routing
// recurs.
func TestPickCachedParsedConfig_PrefersPostSyncActual(t *testing.T) {
	actualPostSync := &parserconfig.StructuredConfig{}
	desired := &parserconfig.StructuredConfig{}

	result := &dataplane.SyncResult{
		PostSyncParsedConfig: actualPostSync,
	}

	got := pickCachedParsedConfig(result, desired)

	assert.Same(t, actualPostSync, got,
		"helper must prefer result.PostSyncParsedConfig over desired so the cache reflects "+
			"what the pod actually serves, not what the caller wanted it to serve")
}

// TestPickCachedParsedConfig_FallsBackToDesired covers the no-changes
// path and the post-sync fetch/parse failure path: when the orchestrator
// did not populate PostSyncParsedConfig, fall back to the caller's
// desired. In the no-changes path the pod was already verified to
// match desired before this code runs; in the failure path we don't
// have anything better to cache, and using desired keeps the previous
// (now-correct) behaviour.
func TestPickCachedParsedConfig_FallsBackToDesired(t *testing.T) {
	desired := &parserconfig.StructuredConfig{}

	result := &dataplane.SyncResult{
		PostSyncParsedConfig: nil,
	}

	got := pickCachedParsedConfig(result, desired)

	assert.Same(t, desired, got,
		"helper must fall back to desired when orchestrator did not populate PostSyncParsedConfig")
}
