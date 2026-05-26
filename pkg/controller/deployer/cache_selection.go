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
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// pickCachedParsedConfig returns the parsed config that the deployer's
// version-cache should store for an endpoint after a sync.
//
// Prefer result.PostSyncParsedConfig (the pod's ACTUAL post-sync state,
// fetched and parsed by the orchestrator) over desired (the caller's
// input intent). The two diverge when the dataplane API applies
// incremental patches against pods with different starting baselines —
// e.g. a rolling HAProxy Deployment where pod A is synced twice and
// pod B is synced once. Both pods end up "logically equal to desired"
// but byte-different on disk, and caching the input desired hides that
// drift from every subsequent reconcile: the cache says "at desired",
// the comparator compares desired-to-desired, sees no diff, and never
// re-syncs the divergent pod. Caching the actual post-sync state lets
// the next reconcile compare actual-vs-desired and produce fixup ops
// until convergence.
//
// The orchestrator only populates PostSyncParsedConfig when ops were
// actually applied AND the post-sync fetch+parse succeeded; for
// no-changes paths or fetch/parse failures we fall back to desired
// (which is equivalent to the live state in those cases anyway,
// because the no-changes path already verified pod==desired).
func pickCachedParsedConfig(result *dataplane.SyncResult, desired *parserconfig.StructuredConfig) *parserconfig.StructuredConfig {
	if result.PostSyncParsedConfig != nil {
		return result.PostSyncParsedConfig
	}
	return desired
}
