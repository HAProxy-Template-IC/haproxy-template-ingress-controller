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

package renderer

import (
	"errors"
	"fmt"
	"maps"
	"reflect"

	iradix "github.com/hashicorp/go-immutable-radix/v2"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type incrementalStateSnapshotAuthentication struct {
	seal         *incrementalStateSnapshotAuthentication
	snapshot     *incrementalStateSnapshot
	cursors      map[string]incrementalStoreCursor
	httpCursor   incrementalHTTPCursor
	bindings     *iradix.Tree[string]
	bindingsRoot *iradix.Node[string]
	bindingsLen  int
	members      *iradix.Tree[struct{}]
	membersRoot  *iradix.Node[struct{}]
	membersLen   int
	activeGroups *incrementalActiveGroupIndex
	activeRoot   *iradix.Node[struct{}]
	activeLen    int
	retired      *iradix.Tree[struct{}]
	retiredRoot  *iradix.Node[struct{}]
	retiredLen   int
	results      *iradix.Tree[incremental.ExactValueRoot]
	resultsRoot  *iradix.Node[incremental.ExactValueRoot]
	resultsLen   int
	derived      *iradix.Tree[incrementalDerivedResource]
	derivedRoot  *iradix.Node[incrementalDerivedResource]
	derivedLen   int
	httpEffects  *iradix.Tree[*iradix.Tree[incrementalHTTPEffect]]
	httpRoot     *iradix.Node[*iradix.Tree[incrementalHTTPEffect]]
	httpLen      int
	catalog      *incrementalResourceCatalogSnapshot
	catalogRoot  *iradix.Node[struct{}]
	catalogLen   int
	groupIndexes map[string]*incrementalGroupIndex
	groupReady   map[string]bool
	preparedPlan *incrementalPreparedPlan
	statusPlan   *templating.StatusPatchProjectionPlan
	statusAuth   *incrementalStatusPatchPlanAuthentication
	bindingCache *incrementalBindingCache
	bindingInput *templating.IncrementalBindingInputSnapshot
	bindingPlan  *incrementalBindingPlan
}

func authenticateIncrementalStateSnapshot(snapshot *incrementalStateSnapshot) {
	if snapshot == nil {
		return
	}
	auth := &incrementalStateSnapshotAuthentication{
		snapshot: snapshot, cursors: maps.Clone(snapshot.cursors), httpCursor: snapshot.httpCursor,
		bindings: snapshot.bindings, members: snapshot.members, activeGroups: snapshot.activeGroups,
		retired: snapshot.retired, results: snapshot.results, derived: snapshot.derived,
		httpEffects: snapshot.httpEffects, catalog: snapshot.catalog,
		groupIndexes: maps.Clone(snapshot.groupIndexes),
		groupReady:   maps.Clone(snapshot.groupReady), preparedPlan: snapshot.preparedPlan,
		statusPlan: snapshot.statusPlan, statusAuth: snapshot.statusAuth, bindingCache: snapshot.bindingCache,
	}
	if snapshot.bindings != nil {
		auth.bindingsRoot, auth.bindingsLen = snapshot.bindings.Root(), snapshot.bindings.Len()
	}
	if snapshot.members != nil {
		auth.membersRoot, auth.membersLen = snapshot.members.Root(), snapshot.members.Len()
	}
	if snapshot.activeGroups != nil && snapshot.activeGroups.instances != nil {
		auth.activeRoot, auth.activeLen = snapshot.activeGroups.instances.Root(), snapshot.activeGroups.instances.Len()
	}
	if snapshot.retired != nil {
		auth.retiredRoot, auth.retiredLen = snapshot.retired.Root(), snapshot.retired.Len()
	}
	if snapshot.results != nil {
		auth.resultsRoot, auth.resultsLen = snapshot.results.Root(), snapshot.results.Len()
	}
	if snapshot.derived != nil {
		auth.derivedRoot, auth.derivedLen = snapshot.derived.Root(), snapshot.derived.Len()
	}
	if snapshot.httpEffects != nil {
		auth.httpRoot, auth.httpLen = snapshot.httpEffects.Root(), snapshot.httpEffects.Len()
	}
	if snapshot.catalog != nil && snapshot.catalog.valid() {
		auth.catalogRoot, auth.catalogLen = snapshot.catalog.Root(), snapshot.catalog.Len()
	}
	if snapshot.bindingCache != nil {
		auth.bindingInput = snapshot.bindingCache.inputs
		auth.bindingPlan = cloneIncrementalBindingPlan(snapshot.bindingCache.plan)
	}
	auth.seal = auth
	snapshot.auth = auth
}

func validateIncrementalStateSnapshotAuthentication(snapshot *incrementalStateSnapshot) error {
	if snapshot == nil || snapshot.auth == nil || snapshot.auth.seal != snapshot.auth ||
		snapshot.auth.snapshot != snapshot {
		return errors.New("incremental state snapshot has invalid provenance")
	}
	auth := snapshot.auth
	if err := validateIncrementalSnapshotRoots(snapshot, auth); err != nil {
		return err
	}
	if err := validateIncrementalSnapshotGroups(snapshot, auth); err != nil {
		return err
	}
	if err := validateIncrementalSnapshotPlans(snapshot, auth); err != nil {
		return err
	}
	return validateIncrementalSnapshotBindingCache(snapshot, auth)
}

func snapshotIndexRootsUnchanged(
	snapshot *incrementalStateSnapshot,
	auth *incrementalStateSnapshotAuthentication,
) bool {
	return snapshot.bindings == auth.bindings && snapshot.bindings != nil &&
		snapshot.bindings.Root() == auth.bindingsRoot && snapshot.bindings.Len() == auth.bindingsLen &&
		snapshot.members == auth.members && snapshot.members != nil &&
		snapshot.members.Root() == auth.membersRoot && snapshot.members.Len() == auth.membersLen &&
		snapshot.retired == auth.retired && snapshot.retired != nil &&
		snapshot.retired.Root() == auth.retiredRoot && snapshot.retired.Len() == auth.retiredLen
}

func snapshotResultRootsUnchanged(
	snapshot *incrementalStateSnapshot,
	auth *incrementalStateSnapshotAuthentication,
) bool {
	return snapshot.results == auth.results && snapshot.results != nil &&
		snapshot.results.Root() == auth.resultsRoot && snapshot.results.Len() == auth.resultsLen &&
		snapshot.derived == auth.derived && snapshot.derived != nil &&
		snapshot.derived.Root() == auth.derivedRoot && snapshot.derived.Len() == auth.derivedLen &&
		snapshot.httpEffects == auth.httpEffects && snapshot.httpEffects != nil &&
		snapshot.httpEffects.Root() == auth.httpRoot && snapshot.httpEffects.Len() == auth.httpLen
}

func validateIncrementalSnapshotRoots(
	snapshot *incrementalStateSnapshot,
	auth *incrementalStateSnapshotAuthentication,
) error {
	if !maps.Equal(snapshot.cursors, auth.cursors) || snapshot.httpCursor != auth.httpCursor {
		return errors.New("incremental state snapshot cursor root changed")
	}
	if !snapshotIndexRootsUnchanged(snapshot, auth) || !snapshotResultRootsUnchanged(snapshot, auth) {
		return errors.New("incremental state snapshot persistent root changed")
	}
	if snapshot.activeGroups != auth.activeGroups || snapshot.activeGroups == nil ||
		snapshot.activeGroups.instances == nil || snapshot.activeGroups.instances.Root() != auth.activeRoot ||
		snapshot.activeGroups.instances.Len() != auth.activeLen {
		return errors.New("incremental state snapshot active-group root changed")
	}
	if err := snapshot.activeGroups.validateAuthentication(); err != nil {
		return err
	}
	if snapshot.catalog != auth.catalog || !snapshot.catalog.valid() ||
		snapshot.catalog.Root() != auth.catalogRoot || snapshot.catalog.Len() != auth.catalogLen {
		return errors.New("incremental state snapshot resource catalog changed")
	}
	return nil
}

func validateIncrementalSnapshotPlans(
	snapshot *incrementalStateSnapshot,
	auth *incrementalStateSnapshotAuthentication,
) error {
	if snapshot.preparedPlan != auth.preparedPlan {
		return errors.New("incremental state snapshot prepared plan changed")
	}
	if snapshot.preparedPlan != nil {
		if err := snapshot.preparedPlan.validateAuthentication(snapshot.results.Root()); err != nil {
			return err
		}
	}
	if snapshot.statusPlan != auth.statusPlan || snapshot.statusAuth != auth.statusAuth {
		return errors.New("incremental state snapshot status plan changed")
	}
	return validateIncrementalStatusPatchPlanAuthentication(snapshot)
}

func validateIncrementalSnapshotGroups(
	snapshot *incrementalStateSnapshot,
	auth *incrementalStateSnapshotAuthentication,
) error {
	if len(snapshot.groupIndexes) != len(auth.groupIndexes) || !maps.Equal(snapshot.groupReady, auth.groupReady) {
		return errors.New("incremental state snapshot group roots changed")
	}
	for group, index := range auth.groupIndexes {
		if snapshot.groupIndexes[group] != index || index == nil {
			return fmt.Errorf("incremental state snapshot group %q changed", group)
		}
		if err := index.validateAuthentication(); err != nil {
			return fmt.Errorf("incremental state snapshot group %q: %w", group, err)
		}
	}
	return nil
}

func validateIncrementalSnapshotBindingCache(
	snapshot *incrementalStateSnapshot,
	auth *incrementalStateSnapshotAuthentication,
) error {
	if snapshot.bindingCache != auth.bindingCache {
		return errors.New("incremental state snapshot binding cache changed")
	}
	if snapshot.bindingCache == nil {
		return nil
	}
	if snapshot.bindingCache.inputs == nil || snapshot.bindingCache.inputs != auth.bindingInput ||
		snapshot.bindingCache.plan == nil || !reflect.DeepEqual(snapshot.bindingCache.plan, auth.bindingPlan) {
		return errors.New("incremental state snapshot binding cache failed authentication")
	}
	return nil
}
