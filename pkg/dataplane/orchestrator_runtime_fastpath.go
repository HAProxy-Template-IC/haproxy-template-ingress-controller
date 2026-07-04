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

package dataplane

import (
	"context"
	"fmt"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// RuntimeServerUpdates is the precomputed result of diffing the previous render
// against the current one for runtime-eligible server changes. The render diff
// (comparator.Compare, O(config size)) is render-vs-render and so identical for
// every pod; callers compute it ONCE per fire via ComputeRuntimeServerUpdates
// and apply it to every pod, instead of re-diffing per pod.
type RuntimeServerUpdates struct {
	runtimeOps []comparator.Operation
	summary    comparator.DiffSummary
}

// ServerOpCount returns the number of runtime-eligible server changes in the set
// (0 = nothing to apply). Safe on a nil receiver.
func (u *RuntimeServerUpdates) ServerOpCount() int {
	if u == nil {
		return 0
	}
	return len(u.runtimeOps)
}

// StructuralOpCount returns the number of non-runtime-eligible (reload-inducing)
// operations in the render diff. Safe on a nil receiver (returns 0). The
// deployer's lane classifier uses this to decide runtime-raw vs structural: a
// diff with zero structural ops can apply purely via the runtime fast path
// (no reload), bypassing the deployment interval.
func (u *RuntimeServerUpdates) StructuralOpCount() int {
	if u == nil {
		return 0
	}
	return u.summary.StructuralOperations()
}

// IsRuntimeEligible reports whether this diff can be applied entirely through the
// runtime fast path: it carries at least one runtime-eligible server change and
// NO structural (reload-inducing) operation. A diff with structural ops, or with
// nothing at all to apply, is not runtime-eligible (the deployer routes it to the
// rate-limited structural lane).
func (u *RuntimeServerUpdates) IsRuntimeEligible() bool {
	return u != nil && u.StructuralOpCount() == 0 && u.ServerOpCount() > 0
}

// ComputeRuntimeServerUpdates diffs prev (last-dispatched render) against current
// (this render) for runtime-eligible server changes. It is a pure, pod-
// independent computation (no client, no fetch), so the deployer's fast path
// runs it ONCE per render and applies the result to every pod. Returns a non-nil
// result even when there are no changes.
func ComputeRuntimeServerUpdates(prev, current *parser.StructuredConfig) (*RuntimeServerUpdates, error) {
	diff, err := comparator.New().Compare(prev, current)
	if err != nil {
		return nil, fmt.Errorf("runtime fast-path render diff: %w", err)
	}
	runtimeOps, _ := partitionByRuntimeEligibility(diff.Operations)
	return &RuntimeServerUpdates{runtimeOps: runtimeOps, summary: diff.Summary}, nil
}

// syncRuntimeRawPush applies the shared render diff to the live worker without
// fetching: it pushes body (the desired render) with the runtime `set server`
// actions derived from updates, via a single skip_reload+skip_version push. Only
// the live worker is updated by the actions; the disk gains the desired config
// body without a reload. When body also carries structural changes (the
// scheduler's pre-interval apply of a structural render's runtime subset), those
// land on disk un-activated — and HEADERLESS, so the next structural sync
// refuses to trust its empty diff against them and force-reloads (see sync()) —
// they are never hidden from a reload indefinitely.
// There is no server-state-file (ADR-0011); the change persists across any later
// reload because that deploy re-renders the current endpoints.
//
// When opts.RestampVersionHeader is set AND the diff carries no structural op,
// a successful push is followed by one versioned skip_reload push of the same
// body to restore the `# _version` header — disk and worker are converged on
// the body at that point, so the header makes the state provable and keeps the
// next structural sync reload-free. Best-effort: on failure (e.g. a concurrent
// versioned writer bumped the version) the config stays headerless and the next
// structural sync converges it with one reload.
func (o *orchestrator) syncRuntimeRawPush(ctx context.Context, body string, updates *RuntimeServerUpdates, opts *SyncOptions, startTime time.Time) (*SyncResult, error) {
	actions := buildRuntimeActions(updates.runtimeOps)
	if actions == "" {
		return o.createNoChangesResult(startTime, &updates.summary), nil
	}
	o.logger.Debug("Runtime fast-path raw-push: shared render-diff actions, no fetch",
		"server_op_count", len(updates.runtimeOps), "action_count", actionCount(actions))

	if err := o.client.PushRawConfigurationSkipReloadSkipVersion(ctx, body, actions); err != nil {
		return nil, wrapApplyError(err)
	}
	if opts != nil && opts.RestampVersionHeader && updates.StructuralOpCount() == 0 {
		// The skip_version push above left the config headerless, which
		// GetVersion reads as the sentinel 1 — so 1 is the version the
		// optimistic-locking check must carry. A 409 means another writer
		// interleaved; leave headerless and let its (or the next) versioned
		// sync own the state.
		if err := o.client.PushRawConfigurationSkipReload(ctx, body, headerlessConfigVersion, ""); err != nil {
			o.logger.Debug("Version-header re-stamp failed; next structural sync converges with a reload",
				"error", err)
		}
	}
	return &SyncResult{
		Success:           true,
		AppliedOperations: convertOperationsToApplied(updates.runtimeOps),
		ReloadTriggered:   false,
		SyncMode:          SyncModeRuntime,
		Duration:          time.Since(startTime),
		Details:           convertDiffSummary(&updates.summary),
		Message:           fmt.Sprintf("Applied %d server updates via raw-push (no fetch)", len(updates.runtimeOps)),
	}, nil
}
