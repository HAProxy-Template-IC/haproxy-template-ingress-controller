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
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	agentclient "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// The agent's 409 reasons (api.Conflict.Reason).
const (
	conflictPrevMismatch    = "prev_mismatch"
	conflictStaleEpoch      = "stale_epoch"
	conflictUnknownBaseline = "unknown_baseline"
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

// podOutcome is what one pod answered, and whether it now runs the render.
type podOutcome struct {
	result   *api.ApplyResult
	decision deployplan.Decision
	sent     []api.Op
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
	state, err := client.State(ctx, req.verify)
	if err != nil {
		return nil, fmt.Errorf("reading agent state: %w", err)
	}
	attempt := &podApply{client: client, endpoint: endpoint, req: req, state: state}
	attempt.full, attempt.notes = c.applyPosture(endpoint, state)

	for round := 1; ; round++ {
		outcome, err := c.applyOnce(ctx, attempt)
		var conflict *agentclient.ConflictError
		if !errors.As(err, &conflict) {
			if outcome != nil {
				outcome.notes = attempt.notes
			}
			return outcome, err
		}
		if conflict.Conflict.Reason == conflictStaleEpoch {
			return nil, fmt.Errorf("%w: pod is at epoch %d, this controller at %d",
				errStaleEpoch, conflict.Conflict.AppliedToken.LeaderEpoch, req.token.LeaderEpoch)
		}
		if round == maxApplyAttempts {
			return nil, err
		}
		attempt.full = attempt.full || conflict.Conflict.Reason == conflictUnknownBaseline
		attempt.notes = append(attempt.notes, "the agent's baseline had moved on ("+conflict.Conflict.Reason+")")
		c.Logger().Info("Agent rejected the apply against its baseline, re-reading its state",
			"pod", endpoint.PodName, "reason", conflict.Conflict.Reason, "full_state", attempt.full)
		if attempt.state, err = client.State(ctx, false); err != nil {
			return nil, fmt.Errorf("re-reading agent state: %w", err)
		}
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
}

// applyOnce composes the decision for the pod's current state and sends every
// chunk of it. Each chunk is fenced on what the previous one applied.
func (c *Component) applyOnce(ctx context.Context, attempt *podApply) (*podOutcome, error) {
	decision := attempt.req.decisionFor(attempt.state, c.plans)
	outcome := &podOutcome{decision: decision}
	prev := fenceOf(attempt.state)

	chunks := decision.Chunk()
	if attempt.full || len(chunks) == 0 {
		chunks = [][]api.Op{nil}
	}
	for i, ops := range chunks {
		manifest := attempt.req.manifest(&decision, ops, prev, attempt.full)
		if i > 0 {
			manifest.InPlaceOps = nil
		}
		result, err := c.send(ctx, attempt, manifest)
		if err != nil {
			return nil, err
		}
		outcome.result = result
		outcome.sent = append(outcome.sent, ops...)
		if !result.OK {
			return outcome, nil
		}
		prev = fence{planID: result.AppliedPlanID, token: result.AppliedToken, workerOps: result.WorkerOpsPlanID}
	}
	outcome.converged = outcome.result.OK &&
		outcome.result.AppliedPlanID == attempt.req.planID &&
		outcome.result.Mode != api.ResultScheduled
	return outcome, nil
}

// send performs one apply, resending the file parts the agent turns out not to
// hold. Only that resend is retried here; a baseline conflict belongs to the
// caller, which has to diff again.
func (c *Component) send(ctx context.Context, attempt *podApply, manifest *api.Manifest) (*api.ApplyResult, error) {
	held := attempt.state.Files
	if attempt.full {
		held = nil
	}
	for {
		parts, err := attempt.req.parts(manifest.Files, held)
		if err != nil {
			return nil, err
		}
		result, err := attempt.client.Apply(ctx, manifest, parts, attempt.planBlob())
		var missing *agentclient.MissingError
		if !errors.As(err, &missing) || held == nil {
			return result, err
		}
		c.Logger().Debug("Agent is missing file parts, resending them",
			"pod", attempt.endpoint.PodName, "files", len(missing.Missing))
		held = nil
	}
}

// planBlob carries the plan to a pod that could otherwise not answer with a
// usable baseline later: it holds none, it stored one from another leader, a
// conflict proved its copy stale, or this is the drift pass that refreshes it.
func (a *podApply) planBlob() io.Reader {
	if len(a.req.blob) == 0 {
		return nil
	}
	if !a.full && !a.resend && !a.req.verify &&
		a.state.AppliedPlanID != "" && a.state.AppliedToken.LeaderEpoch == a.req.token.LeaderEpoch {
		return nil
	}
	return bytes.NewReader(a.req.blob)
}

// fence is the baseline one apply is composed against.
type fence struct {
	planID    string
	token     api.Token
	workerOps string
}

func fenceOf(state *api.State) fence {
	return fence{planID: state.AppliedPlanID, token: state.AppliedToken, workerOps: state.WorkerOpsPlanID}
}

// manifest composes one apply from the decision. full overrides the verdict:
// a pod whose baseline is unknown or whose agent is a foreign version gets the
// complete file set and a reload, never ops composed against a guess.
func (r *deployRequest) manifest(decision *deployplan.Decision, ops []api.Op, prev fence, full bool) *api.Manifest {
	manifest := &api.Manifest{
		PlanID:             r.planID,
		PlanSchemaVersion:  r.plan.SchemaVersion,
		Token:              r.token,
		ExpectedPrevPlanID: prev.planID,
		ExpectedPrevToken:  prev.token,
		ValidatedPlanID:    r.validatedPlanID,
		Files:              decision.Files,
		Ops:                ops,
		InPlaceOps:         decision.InPlace,
		Mode:               decision.Mode,
	}
	if len(manifest.InPlaceOps) > 0 {
		manifest.ExpectedWorkerOpsPlanID = prev.workerOps
	}
	if full {
		manifest.Ops, manifest.InPlaceOps, manifest.ExpectedWorkerOpsPlanID = nil, nil, ""
		manifest.Mode = api.ModeReload
	}
	return manifest
}

// parts carries the content of every file the agent does not already hold at
// the manifest's digest. haproxy.cfg always travels whole: it is the file the
// reload reads, and the renderer's exact bytes are what the pod must run.
func (r *deployRequest) parts(files []api.File, held map[string]api.FileAt) (map[string]io.Reader, error) {
	parts := make(map[string]io.Reader, len(files))
	for i := range files {
		file := &files[i]
		if at, ok := held[file.Path]; ok && at.Digest == file.Digest && file.Kind != api.FileKindConfig {
			continue
		}
		content, ok := r.contents[file.Digest]
		if !ok {
			return nil, fmt.Errorf("render carries no content for %s (digest %s)", file.Path, file.Digest)
		}
		parts[file.Path] = strings.NewReader(content)
	}
	return parts, nil
}

// decisionFor diffs the render against what this pod applied, reusing the
// answer across pods that report the same baseline and capabilities.
func (r *deployRequest) decisionFor(state *api.State, plans *planCache) deployplan.Decision {
	caps := deployplan.CapsFor(state.HAProxy.Version, state.AgentOps)
	baseline := deployplan.Baseline{
		Applied:               plans.Baseline(state),
		Running:               plans.Plan(state.RunningPlanID),
		WorkerOps:             plans.Plan(state.WorkerOpsPlanID),
		Inventory:             state.Inventory,
		Caps:                  caps,
		PendingServerDeletes:  len(state.PendingDeletes.Servers),
		PendingBackendDeletes: len(state.PendingDeletes.Backends),
		ReloadPending:         state.ReloadPendingAt != "",
	}
	return r.diffs.get(&diffKey{
		applied:       baselineID(baseline.Applied),
		running:       state.RunningPlanID,
		workerOps:     state.WorkerOpsPlanID,
		caps:          state.HAProxy.Version + "\x00" + strings.Join(state.AgentOps, ","),
		inventory:     state.Inventory.Generation,
		reloadPending: baseline.ReloadPending,
	}, func() deployplan.Decision {
		return deployplan.Diff(r.plan, &baseline)
	})
}

func baselineID(plan *renderplan.Plan) string {
	if plan == nil {
		return ""
	}
	return plan.ID
}
