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

package deployer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	agentclient "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// The agent's 409 reasons (api.Conflict.Reason).
const (
	conflictPrevMismatch         = "prev_mismatch"
	conflictStaleEpoch           = "stale_epoch"
	conflictUnknownBaseline      = "unknown_baseline"
	conflictWorkerOpsMismatch    = "worker_ops_mismatch"
	conflictRevertTargetMismatch = "revert_target_mismatch"
)

const (
	// maxConcurrentPods bounds one deployment's fan-out.
	maxConcurrentPods = 16

	// maxApplyAttempts bounds one pod's apply: the composed apply, one re-diff
	// after a baseline conflict, and one full-state reload.
	maxApplyAttempts = 3

	// maxStatusReasons matches the CRD's MaxItems on PodDeploymentStatus.reasons.
	maxStatusReasons = 8
)

// errStaleEpoch reports that a newer leader epoch owns this pod: this
// controller is no longer the fleet's writer and must stop dispatching.
var errStaleEpoch = errors.New("a newer leader epoch owns this pod")

// errEpochReclaimed reports a pod that outranked this controller because the
// epoch counter regressed, not because a rival exists: the epoch was lifted
// past the fleet's and the next deployment carries it.
var errEpochReclaimed = errors.New("the leader epoch had regressed below the fleet and was reclaimed")

// epochRefused decides what a pod refusing this controller's epoch means. A
// Lease this controller still holds at the epoch it claimed proves there is no
// rival — the counter regressed, which a recreated or restored Lease does — so
// the epoch is lifted past the fleet's and this deployment fails into the
// scheduler's retry. Anything else is a newer leader, and standing down is the
// only correct answer to it.
func (c *Component) epochRefused(ctx context.Context, endpoint *dataplane.Endpoint, podEpoch, ourEpoch uint64) error {
	outranked := fmt.Errorf("pod is at epoch %d, this controller at %d", podEpoch, ourEpoch)
	if c.fence == nil {
		return fmt.Errorf("%w: %w", errStaleEpoch, outranked)
	}
	claimed, err := c.fence.Reclaim(ctx, podEpoch)
	if err != nil {
		c.Logger().Error("A pod outranks this controller's leader epoch and the lease agrees",
			"pod", endpoint.PodName, "error", err)
		return fmt.Errorf("%w: %w", errStaleEpoch, outranked)
	}
	c.Logger().Warn("The leader epoch had regressed below the fleet, reclaimed it",
		"pod", endpoint.PodName, "pod_epoch", podEpoch, "epoch", claimed)
	return fmt.Errorf("%w: %w", errEpochReclaimed, outranked)
}

// podOutcome is what one pod answered, and whether it now runs the render.
type podOutcome struct {
	result   *api.ApplyResult
	decision deployplan.Decision
	sent     []api.Op
	phases   events.DeployPhases
	// notes explain what the controller decided about this pod before the diff
	// ran — a contract skew, a dropped baseline — ahead of the diff's reasons.
	notes     []string
	converged bool
}

// reasons are the notes and the diff's reasons, most significant first.
func (o *podOutcome) reasons() []string {
	return append(append([]string(nil), o.notes...), o.decision.Reasons...)
}

// applyToPod brings one pod to the request's plan: read its baseline, diff
// against it, and send the resulting applies. A conflict re-reads the pod's
// state and diffs again; a pod whose baseline this controller cannot produce
// gets the complete file set and a reload.
func (c *Component) applyToPod(ctx context.Context, endpoint *dataplane.Endpoint, req *deployRequest) (*podOutcome, error) {
	client, err := c.clients.For(endpoint)
	if err != nil {
		return nil, fmt.Errorf("creating agent client: %w", err)
	}
	stateStarted := time.Now()
	state, err := client.State(ctx, api.StateRead{Verify: req.verify})
	if err != nil {
		return nil, fmt.Errorf("reading agent state: %w", err)
	}
	c.notePodPlans(endpoint, state.AppliedPlanProof, state.RunningPlanProof, state.WorkerOpsPlanProof)
	attempt := &podApply{client: client, endpoint: endpoint, req: req, state: state}
	attempt.phases.Pod = endpoint.PodName
	attempt.phases.StateMs = time.Since(stateStarted).Milliseconds()
	attempt.full, attempt.notes = c.applyPosture(endpoint, state)

	for round := 1; ; round++ {
		outcome, err := c.applyOnce(ctx, attempt)
		var conflict *agentclient.ConflictError
		if !errors.As(err, &conflict) {
			if outcome != nil {
				outcome.notes = attempt.notes
				outcome.phases = attempt.phases
			}
			return outcome, err
		}
		if conflict.Conflict.Reason == conflictStaleEpoch {
			return nil, c.epochRefused(ctx, endpoint, conflict.Conflict.AppliedToken.LeaderEpoch, req.token.LeaderEpoch)
		}
		if round == maxApplyAttempts {
			return nil, err
		}
		attempt.full = attempt.full || conflict.Conflict.Reason == conflictUnknownBaseline
		c.Logger().Info("Agent rejected the apply against its baseline, re-reading its state",
			"pod", endpoint.PodName, "reason", conflict.Conflict.Reason, "full_state", attempt.full)
		stateStarted = time.Now()
		if attempt.state, err = client.State(ctx, api.StateRead{}); err != nil {
			return nil, fmt.Errorf("re-reading agent state: %w", err)
		}
		attempt.phases.StateMs += time.Since(stateStarted).Milliseconds()
		c.notePodPlans(endpoint, attempt.state.AppliedPlanProof, attempt.state.RunningPlanProof, attempt.state.WorkerOpsPlanProof)
		if conflict.Conflict.Reason == conflictWorkerOpsMismatch {
			// Only the worker moved on (its pacer fired between the state read
			// and the apply); the applied plan the blob describes is intact.
			continue
		}
		attempt.notes = append(attempt.notes, "the agent's baseline had moved on ("+conflict.Conflict.Reason+")")
		// A conflict means this pod's stored plan is not the one this
		// controller composed against; the next apply carries it again.
		attempt.resend = true
	}
}

// podApply is one pod's apply in progress: what it currently reports and how
// much of the desired state this round is sending it.
type podApply struct {
	client   *agentclient.Client
	endpoint *dataplane.Endpoint
	req      *deployRequest
	state    *api.State
	full     bool     // send the complete file set and reload, ops composed against nothing
	resend   bool     // carry the plan blob even though the pod holds a baseline
	notes    []string // what the controller decided before the diff ran
	phases   events.DeployPhases
}

// applyOnce composes the decision for the pod's current state and sends every
// chunk of it. Each chunk is fenced on what the previous one applied.
func (c *Component) applyOnce(ctx context.Context, attempt *podApply) (*podOutcome, error) {
	authority := podKey(attempt.endpoint)
	diffStarted := time.Now()
	decision := attempt.req.decisionFor(attempt.state, c.plans, authority)
	attempt.phases.DiffMs += time.Since(diffStarted).Milliseconds()
	if decision.Verdict == deployplan.VerdictReload {
		c.Logger().Debug("Reload required: this change cannot run as runtime ops",
			"pod", attempt.endpoint.PodName, "reasons", decision.Reasons)
	}
	outcome := &podOutcome{decision: decision}
	prev := fenceOf(attempt.state)
	validated := attempt.req.validatedPlanFor(authority, attempt.state)

	chunks := decision.Chunk()
	if attempt.full || len(chunks) == 0 {
		chunks = [][]api.Op{nil}
	}
	// The blob rides the last chunk only. Every successful chunk gets a fresh
	// agent role proof, so only the final chunk can bind the stored blob to the
	// role the completed deployment reports.
	blob := attempt.sendsPlanBlob(c.plans.Baseline(authority, attempt.state),
		c.keeper.Delivers(attempt.endpoint) && decision.Verdict != deployplan.VerdictReload)
	for i, ops := range chunks {
		manifest := attempt.req.manifest(&decision, ops, &prev, attempt.full, validated)
		if i > 0 {
			manifest.InPlaceOps = nil
		}
		result, err := c.send(ctx, attempt, manifest, blob && i == len(chunks)-1)
		if err != nil {
			return nil, err
		}
		outcome.result = result
		outcome.sent = append(outcome.sent, ops...)
		if !result.OK {
			return outcome, nil
		}
		if err := c.bindApplyResult(attempt, authority, &decision, result); err != nil {
			return nil, err
		}
		if !blob && i == len(chunks)-1 && result.AppliedPlanID == attempt.req.planID {
			c.keeper.Offer(attempt.endpoint, result.AppliedPlanID, result.AppliedPlanProof, attempt.req.blob)
		}
		prev = fence{
			planID:         result.AppliedPlanID,
			planProof:      result.AppliedPlanProof,
			token:          result.AppliedToken,
			workerOps:      result.WorkerOpsPlanID,
			workerOpsProof: result.WorkerOpsPlanProof,
		}
	}
	outcome.converged = outcome.result.OK &&
		outcome.result.AppliedPlanID == attempt.req.planID &&
		outcome.result.AppliedPlanProof != "" &&
		outcome.result.Mode != api.ResultScheduled
	return outcome, nil
}

func (c *Component) bindApplyResult(
	attempt *podApply,
	authority string,
	decision *deployplan.Decision,
	result *api.ApplyResult,
) error {
	// The agent reports the plan it actually holds, which is not always the one
	// this apply sent: a revert lands the last known good set, and a baseline
	// invalidated mid-apply clears the applied plan outright. Both come back OK
	// with a different -- or empty -- id, and binding this plan under it would
	// describe a set the pod is not on. They are simply not converged, which the
	// caller already computes, so the next attempt re-derives the decision.
	// A stale proof for the plan we DID send is still a fault and still caught.
	if result.AppliedPlanID != attempt.req.planID {
		return nil
	}
	if err := c.plans.BindOccurrence(
		authority, result.AppliedPlanID, result.AppliedPlanProof, attempt.req.identity,
	); err != nil {
		return fmt.Errorf("agent reused or omitted the applied plan proof: %w", err)
	}
	if decision.WorkerPlan == nil || result.WorkerOpsPlanID != decision.WorkerPlan.ID ||
		(result.WorkerOpsPlanProof == result.AppliedPlanProof && !exactPlan(decision.WorkerPlan, attempt.req.plan)) {
		return nil
	}
	if !c.plans.Bind(authority, result.WorkerOpsPlanID, result.WorkerOpsPlanProof, decision.WorkerPlan) {
		return fmt.Errorf("agent reused or omitted the worker plan proof")
	}
	return nil
}

// send performs one apply, resending the file parts the agent turns out not to
// hold. Only that resend is retried here; a baseline conflict belongs to the
// caller, which has to diff again.
func (c *Component) send(ctx context.Context, attempt *podApply, manifest *api.Manifest, withBlob bool) (*api.ApplyResult, error) {
	held := attempt.state.Files
	if attempt.full {
		held = nil
	}
	held = c.prepareContentProofs(attempt, manifest, held)
	for {
		parts, uploaded, err := attempt.req.parts(manifest.Files, held)
		if err != nil {
			return nil, err
		}
		blobStarted := time.Now()
		blob := attempt.planBlob(withBlob)
		attempt.phases.BlobWaitMs += time.Since(blobStarted).Milliseconds()
		sendStarted := time.Now()
		result, err := attempt.client.Apply(ctx, manifest, parts, blob)
		attempt.phases.SendMs += time.Since(sendStarted).Milliseconds()
		attempt.phases.UploadBytes += uploaded
		if result != nil {
			attempt.phases.Agent = addApplyTiming(attempt.phases.Agent, result.Timing)
		}
		var missing *agentclient.MissingError
		if !errors.As(err, &missing) || held == nil {
			if err == nil && result != nil && result.OK {
				c.acceptContentProofs(attempt, manifest)
			}
			return result, err
		}
		c.Logger().Debug("Agent is missing file parts, resending them",
			"pod", attempt.endpoint.PodName, "files", len(missing.Missing))
		held = nil
	}
}

type contentProof struct {
	proof   string
	content string
}

func (c *Component) prepareContentProofs(
	attempt *podApply, manifest *api.Manifest, held map[string]api.FileAt,
) map[string]api.FileAt {
	c.contentProofMu.Lock()
	known := c.contentProofs[podKey(attempt.endpoint)]
	c.contentProofMu.Unlock()
	safe := make(map[string]api.FileAt, len(held))
	for i := range manifest.Files {
		file := &manifest.Files[i]
		content, available := attempt.req.contents[file.Path]
		proof, proved := known[file.Path]
		at, remote := held[file.Path]
		if available && proved && remote && proof.proof != "" && proof.proof == at.Proof && proof.content == content &&
			at.Digest == file.Digest && at.Size == file.Size {
			file.Proof = proof.proof
			safe[file.Path] = at
			continue
		}
		file.Proof = fmt.Sprintf("%d:%d:%d", attempt.req.token.LeaderEpoch, attempt.req.token.RenderSeq, i)
	}
	if len(safe) == 0 {
		return nil
	}
	return safe
}

func (c *Component) acceptContentProofs(attempt *podApply, manifest *api.Manifest) {
	known := make(map[string]contentProof, len(manifest.Files))
	held := make(map[string]api.FileAt, len(manifest.Files))
	for i := range manifest.Files {
		file := &manifest.Files[i]
		content, available := attempt.req.contents[file.Path]
		if available && file.Proof != "" {
			known[file.Path] = contentProof{proof: file.Proof, content: content}
		}
		held[file.Path] = api.FileAt{Digest: file.Digest, Proof: file.Proof, Size: file.Size}
	}
	c.contentProofMu.Lock()
	c.contentProofs[podKey(attempt.endpoint)] = known
	c.contentProofMu.Unlock()
	attempt.state.Files = held
}

// sendsPlanBlob reports whether this apply has to carry the plan. The pod is
// what a leader with a cold cache reads its baseline from, and a pod with none
// costs a full-state reload. A pod that re-reads its whole state, or resends,
// or is verified, gets the blob with the apply; so does one that reloads, the
// cold path where the blob's cost does not count. Otherwise the keeper
// delivers it afterwards, unless the pod's agent has no plan endpoint, in
// which case every apply that moves the applied plan on brings the blob.
func (a *podApply) sendsPlanBlob(baseline *renderplan.Plan, keeperDelivers bool) bool {
	if a.full || a.resend || a.req.verify {
		return true
	}
	if keeperDelivers {
		return false
	}
	return !a.req.isPlan(baseline) || !a.state.HoldsAppliedPlan()
}

// planBlob is the part to send, nil when the blob is not sent or could not be
// encoded. The wait for the encode lands here, after the state read and the
// diff, which is what it runs alongside.
func (a *podApply) planBlob(send bool) io.Reader {
	if !send {
		return nil
	}
	blob := a.req.blob.bytes()
	if len(blob) == 0 {
		return nil
	}
	return bytes.NewReader(blob)
}

// fence is the baseline one apply is composed against.
type fence struct {
	planID         string
	planProof      string
	token          api.Token
	workerOps      string
	workerOpsProof string
}

func fenceOf(state *api.State) fence {
	return fence{
		planID:         state.AppliedPlanID,
		planProof:      state.AppliedPlanProof,
		token:          state.AppliedToken,
		workerOps:      state.WorkerOpsPlanID,
		workerOpsProof: state.WorkerOpsPlanProof,
	}
}

// manifest composes one apply from the decision. full overrides the verdict:
// a pod whose baseline is unknown or whose agent is a foreign version gets the
// complete file set and a reload, never ops composed against a guess.
func (r *deployRequest) manifest(
	decision *deployplan.Decision,
	ops []api.Op,
	prev *fence,
	full bool,
	validated planReference,
) *api.Manifest {
	manifest := &api.Manifest{
		IdentityVersion:       api.ExactIdentityVersion,
		PlanID:                r.planID,
		PlanSchemaVersion:     r.plan.SchemaVersion,
		Token:                 r.token,
		ExpectedPrevPlanID:    prev.planID,
		ExpectedPrevPlanProof: prev.planProof,
		ExpectedPrevToken:     prev.token,
		ValidatedPlanID:       validated.id,
		ValidatedPlanProof:    validated.proof,
		Files:                 slices.Clone(decision.Files),
		Ops:                   ops,
		InPlaceOps:            decision.InPlace,
		Mode:                  decision.Mode,
	}
	if len(manifest.InPlaceOps) > 0 {
		manifest.ExpectedWorkerOpsPlanID = prev.workerOps
		manifest.ExpectedWorkerOpsPlanProof = prev.workerOpsProof
		manifest.WorkerOpsPlanID = decision.WorkerPlan.ID
	}
	if full {
		manifest.Ops, manifest.InPlaceOps = nil, nil
		manifest.ExpectedWorkerOpsPlanID, manifest.ExpectedWorkerOpsPlanProof = "", ""
		manifest.WorkerOpsPlanID, manifest.WorkerOpsPlanProof = "", ""
		manifest.Mode = api.ModeReload
	}
	return manifest
}

// addApplyTiming sums the agent's split over the chunks of one apply.
func addApplyTiming(sum, next api.ApplyTiming) api.ApplyTiming {
	return api.ApplyTiming{
		StageMs: sum.StageMs + next.StageMs,
		WriteMs: sum.WriteMs + next.WriteMs,
		OpsMs:   sum.OpsMs + next.OpsMs,
		TotalMs: sum.TotalMs + next.TotalMs,
	}
}

// parts carries every file the agent has not proved it holds, and how many
// bytes that is.
func (r *deployRequest) parts(files []api.File, held map[string]api.FileAt) (parts map[string]io.Reader, uploaded int64, err error) {
	parts = make(map[string]io.Reader, len(files))
	for i := range files {
		file := &files[i]
		if at, ok := held[file.Path]; ok && file.Proof != "" && at.Proof == file.Proof &&
			at.Digest == file.Digest && at.Size == file.Size {
			continue
		}
		content, ok := r.contents[file.Path]
		if !ok {
			return nil, 0, fmt.Errorf("render carries no content for %s (digest %s)", file.Path, file.Digest)
		}
		parts[file.Path] = strings.NewReader(content)
		uploaded += int64(len(content))
	}
	return parts, uploaded, nil
}

// decisionFor diffs the render against what this pod applied, reusing the
// answer across pods that report the same baseline and capabilities.
func (r *deployRequest) decisionFor(state *api.State, plans *planCache, authority string) deployplan.Decision {
	caps := deployplan.CapsFor(state.HAProxy.Version, state.AgentOps)
	applied := plans.Baseline(authority, state)
	running := plans.Plan(authority, state.RunningPlanID, state.RunningPlanProof)
	workerOps := plans.Plan(authority, state.WorkerOpsPlanID, state.WorkerOpsPlanProof)
	baseline := deployplan.Baseline{
		Applied:               applied,
		Running:               running,
		WorkerOps:             workerOps,
		Inventory:             state.Inventory,
		Caps:                  caps,
		PendingServerDeletes:  len(state.PendingDeletes.Servers),
		PendingBackendDeletes: len(state.PendingDeletes.Backends),
		ReloadPending:         state.ReloadPendingAt != "",
	}
	decision := r.diffs.get(&diffKey{
		applied:         applied,
		running:         running,
		workerOps:       workerOps,
		caps:            state.HAProxy.Version + "\x00" + strings.Join(state.AgentOps, ","),
		inventory:       inventoryIdentity(&state.Inventory),
		pendingServers:  baseline.PendingServerDeletes,
		pendingBackends: baseline.PendingBackendDeletes,
		reloadPending:   baseline.ReloadPending,
	}, func() deployplan.Decision {
		return deployplan.Diff(r.plan, &baseline)
	})
	return decision
}

// inventoryDigest identifies what the worker has loaded by its content: the
// generation next to it counts one pod's reloads, so two pods on the same plan
// can report the same generation over different sets.
func inventoryIdentity(inventory *api.Inventory) string {
	var sets strings.Builder
	for _, paths := range [][]string{
		inventory.Maps, inventory.Certs, inventory.CAFiles, inventory.CRLFiles, inventory.CRTLists,
	} {
		for _, path := range paths {
			fmt.Fprintf(&sets, "%d:", len(path))
			sets.WriteString(path)
		}
		sets.WriteByte('|')
	}
	return sets.String()
}
