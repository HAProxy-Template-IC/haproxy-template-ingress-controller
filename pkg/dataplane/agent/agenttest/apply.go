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

package agenttest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/http"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// fixedTimestamp is what the fake stamps on every result: a test asserting a
// recorded apply must not depend on wall-clock time.
const fixedTimestamp = "2026-01-01T00:00:00Z"

type applyRequest struct {
	manifest     api.Manifest
	parts        map[string][]byte
	plan         []byte
	appliedProof string
	workerProof  string
}

// outcome is one apply's answer: exactly one of result, conflict, missing.
type outcome struct {
	status   int
	result   *api.ApplyResult
	conflict *api.Conflict
	missing  []string
}

func (a *Agent) handleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	req, err := parseApply(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	a.mu.Lock()
	if a.failOnce {
		a.failOnce = false
		a.applies = append(a.applies, RecordedApply{
			Manifest: req.manifest, Parts: req.parts, Plan: req.plan,
			Status: http.StatusInternalServerError,
		})
		a.mu.Unlock()
		http.Error(w, "the agent hit an internal error", http.StatusInternalServerError)
		return
	}
	out := a.apply(req)
	a.applies = append(a.applies, RecordedApply{
		Manifest: req.manifest,
		Parts:    req.parts,
		Plan:     req.plan,
		Status:   out.status,
		Result:   out.result,
		Conflict: out.conflict,
		Missing:  out.missing,
	})
	a.mu.Unlock()

	switch {
	case out.conflict != nil:
		writeJSON(w, out.status, out.conflict)
	case out.missing != nil:
		writeJSON(w, out.status, api.Missing{Missing: out.missing})
	default:
		writeJSON(w, out.status, out.result)
	}
}

func parseApply(r *http.Request) (*applyRequest, error) {
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return nil, fmt.Errorf("content type: %w", err)
	}
	reader := multipart.NewReader(io.LimitReader(r.Body, api.MaxApplyBodyBytes), params["boundary"])
	req := &applyRequest{parts: map[string][]byte{}}
	sawManifest := false
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read part: %w", err)
		}
		body, err := io.ReadAll(part)
		if err != nil {
			return nil, fmt.Errorf("read part %q: %w", part.FormName(), err)
		}
		switch name := part.FormName(); name {
		case api.PartManifest:
			if err := json.Unmarshal(body, &req.manifest); err != nil {
				return nil, fmt.Errorf("decode manifest: %w", err)
			}
			sawManifest = true
		case api.PartPlan:
			req.plan = body
		default:
			if !sawManifest {
				return nil, fmt.Errorf("part %q arrived before the manifest", name)
			}
			req.parts[name] = body
		}
	}
	if !sawManifest {
		return nil, errors.New("apply has no manifest part")
	}
	return req, nil
}

// apply runs the contract's stages in order. Callers hold a.mu.
func (a *Agent) apply(req *applyRequest) outcome {
	m := &req.manifest
	normalizeLegacyManifest(m)
	if conflict := a.fence(m); conflict != nil {
		return outcome{status: http.StatusConflict, conflict: conflict}
	}
	if missing := a.missingParts(req); len(missing) > 0 {
		return outcome{status: http.StatusConflict, missing: missing}
	}
	if path := a.mismatchedPart(req); path != "" {
		return a.nack(m, "verify", fmt.Sprintf("part %q does not match its manifest digest", path))
	}
	if m.Mode != api.ModeRevertLKG {
		a.proofGeneration++
		req.appliedProof = fmt.Sprintf("a:%d", a.proofGeneration)
		if len(m.InPlaceOps) > 0 {
			a.proofGeneration++
			req.workerProof = fmt.Sprintf("a:%d", a.proofGeneration)
		}
	}
	a.promoteLKG(m)
	out := a.runMode(req)
	a.commitPlanBlob(req)
	return out
}

func normalizeLegacyManifest(m *api.Manifest) {
	if m.IdentityVersion == api.ExactIdentityVersion {
		return
	}
	m.Mode = api.ModeReload
	m.Ops = nil
	m.InPlaceOps = nil
	m.ExpectedPrevPlanProof = ""
	m.ExpectedWorkerOpsPlanID = ""
	m.ExpectedWorkerOpsPlanProof = ""
	m.WorkerOpsPlanID = ""
	m.WorkerOpsPlanProof = ""
	m.ValidatedPlanID = ""
	m.ValidatedPlanProof = ""
}

// runMode is the apply itself: a revert lands the last known good set, anything
// else the manifest's own.
func (a *Agent) runMode(req *applyRequest) outcome {
	if req.manifest.Mode == api.ModeRevertLKG {
		return a.revertLKG(&req.manifest)
	}
	return a.transact(req)
}

// commitPlanBlob keeps the plan of the apply that just landed, and only while
// it is the plan the pod applied: an apply that moves the applied plan on
// without carrying one leaves the pod with no baseline to hand back, which is
// what the real agent's PlanBlobPlanID does.
func (a *Agent) commitPlanBlob(req *applyRequest) {
	if len(req.plan) == 0 || !samePlanRef(
		a.state.AppliedPlanID,
		a.state.AppliedPlanProof,
		req.manifest.PlanID,
		req.appliedProof,
	) {
		return
	}
	a.appliedPlan = req.plan
	a.planBlobPlanID = req.manifest.PlanID
	a.planBlobPlanProof = req.appliedProof
}

// fence is the write gate, and the only three reasons an apply is answered with
// a 409, the worker-ops baseline included when the in-place batch is going to
// run: nothing is written, the caller re-diffs against the worker as it is.
func (a *Agent) fence(m *api.Manifest) *api.Conflict {
	if reason := a.conflictOnce; reason != "" {
		a.conflictOnce = ""
		return a.conflict(reason)
	}
	switch {
	case m.Token.LeaderEpoch < a.state.AppliedToken.LeaderEpoch:
		return a.conflict("stale_epoch")
	case m.Mode == api.ModeRevertLKG:
		if !a.carriesRefusedPlan(m.PlanID, m.PlanProof) {
			return a.conflict("revert_target_mismatch")
		}
	case m.ExpectedPrevPlanID != a.state.AppliedPlanID:
		if a.state.AppliedPlanID == "" {
			return a.conflict("unknown_baseline")
		}
		return a.conflict("prev_mismatch")
	case m.ExpectedPrevToken != a.state.AppliedToken:
		return a.conflict("prev_mismatch")
	case m.IdentityVersion == api.ExactIdentityVersion && m.Mode != api.ModeReload &&
		!samePlanRef(m.ExpectedPrevPlanID, m.ExpectedPrevPlanProof, a.state.AppliedPlanID, a.state.AppliedPlanProof):
		return a.conflict("prev_mismatch")
	case m.IdentityVersion == api.ExactIdentityVersion && m.Mode == api.ModeReload &&
		a.state.AppliedPlanProof != "" && m.ExpectedPrevPlanProof != a.state.AppliedPlanProof:
		return a.conflict("prev_mismatch")
	case a.inPlaceWillRun(m) && !samePlanRef(
		m.ExpectedWorkerOpsPlanID,
		m.ExpectedWorkerOpsPlanProof,
		a.state.WorkerOpsPlanID,
		a.state.WorkerOpsPlanProof,
	):
		return a.conflict("worker_ops_mismatch")
	}
	return nil
}

func (a *Agent) conflict(reason string) *api.Conflict {
	return &api.Conflict{
		AppliedPlanID:      a.state.AppliedPlanID,
		AppliedPlanProof:   a.state.AppliedPlanProof,
		AppliedToken:       a.state.AppliedToken,
		RunningPlanID:      a.state.RunningPlanID,
		RunningPlanProof:   a.state.RunningPlanProof,
		WorkerOpsPlanID:    a.state.WorkerOpsPlanID,
		WorkerOpsPlanProof: a.state.WorkerOpsPlanProof,
		LKGPlanID:          a.state.LKGPlanID,
		LKGPlanProof:       a.state.LKGPlanProof,
		Reason:             reason,
	}
}

func (a *Agent) carriesRefusedPlan(id, proof string) bool {
	if samePlanRef(id, proof, a.state.RunningPlanID, a.state.RunningPlanProof) {
		return false
	}
	return samePlanRef(id, proof, a.state.AppliedPlanID, a.state.AppliedPlanProof) ||
		samePlanRef(id, proof, a.state.WorkerOpsPlanID, a.state.WorkerOpsPlanProof)
}

func samePlanRef(leftID, leftProof, rightID, rightProof string) bool {
	return leftProof != "" && rightProof != "" && leftID == rightID && leftProof == rightProof
}

// missingParts names the files whose content the agent does not hold. It is a
// per-path question, not a content-addressed one: a new path whose bytes match
// an existing file is still missing, because the agent stores files by path.
func (a *Agent) missingParts(req *applyRequest) []string {
	if forced := a.missingOnce; len(forced) > 0 {
		a.missingOnce = nil
		return forced
	}
	var missing []string
	for _, f := range req.manifest.Files {
		if _, sent := req.parts[f.Path]; sent {
			continue
		}
		if at, held := a.state.Files[f.Path]; held && f.Proof != "" && at.Proof == f.Proof &&
			at.Digest == f.Digest && at.Size == f.Size {
			continue
		}
		missing = append(missing, f.Path)
	}
	slices.Sort(missing)
	return missing
}

// mismatchedPart names the first part whose content is not what the manifest
// declares. Manifest digests are renderplan.Digest of the content.
func (a *Agent) mismatchedPart(req *applyRequest) string {
	for _, f := range req.manifest.Files {
		content, sent := req.parts[f.Path]
		if !sent {
			continue
		}
		if renderplan.Digest(content) != f.Digest {
			return f.Path
		}
	}
	return ""
}

// promoteLKG runs before the transaction: a plan the controller has validated
// and the agent has applied becomes the rollback baseline.
func (a *Agent) promoteLKG(m *api.Manifest) {
	if !samePlanRef(m.ValidatedPlanID, m.ValidatedPlanProof, a.state.AppliedPlanID, a.state.AppliedPlanProof) {
		return
	}
	a.state.LKGPlanID = a.state.AppliedPlanID
	a.state.LKGPlanProof = a.state.AppliedPlanProof
	a.lkgFiles = maps.Clone(a.state.Files)
}

// transact writes the files and then decides, in the order the real agent does:
// a reload already waiting takes precedence over the mode the manifest asks
// for, because the worker it would target is on its way out.
func (a *Agent) transact(req *applyRequest) outcome {
	m := &req.manifest
	changed := a.storeFiles(req)
	switch {
	case a.reloadPending:
		return a.scheduled(req)
	case m.Mode == api.ModeReload, a.state.AppliedPlanID == "":
		// An unknown baseline reloads regardless of the ops, as the real agent does.
		return a.reload(req)
	}
	if kind := a.firstRejected(m.Ops); kind != "" {
		return a.rejectOps(m, kind)
	}
	mode := api.ResultNoop
	switch {
	case len(m.Ops) > 0:
		mode = api.ResultRuntime
	case changed:
		mode = api.ResultFileOnly
	}
	a.advance(m, req.appliedProof)
	// Every op ran on the worker, so it holds the applied plan.
	a.state.WorkerOpsPlanID = m.PlanID
	a.state.WorkerOpsPlanProof = req.appliedProof
	return a.ack(m, mode, m.Ops, nil)
}

// scheduled coalesces the apply into the reload already waiting: the files land
// and only the in-place ops run. They advance the worker-ops baseline only when
// they actually executed, and anything that leaves the worker unexplained
// invalidates the pod instead of forcing a second reload.
func (a *Agent) scheduled(req *applyRequest) outcome {
	m := &req.manifest
	reload := &api.ReloadInfo{ScheduledAt: a.state.ReloadPendingAt}
	if len(m.InPlaceOps) == 0 {
		a.advance(m, req.appliedProof)
		return a.ack(m, api.ResultScheduled, nil, reload)
	}
	if !samePlanRef(
		m.ExpectedWorkerOpsPlanID,
		m.ExpectedWorkerOpsPlanProof,
		a.state.WorkerOpsPlanID,
		a.state.WorkerOpsPlanProof,
	) {
		return a.invalidate(m, reload, "in-place ops were composed against a different worker baseline")
	}
	if kind := a.firstRejected(m.InPlaceOps); kind != "" {
		return a.invalidate(m, reload, kind+": command rejected by HAProxy")
	}
	a.advance(m, req.appliedProof)
	a.state.WorkerOpsPlanID = m.WorkerOpsPlanID
	a.state.WorkerOpsPlanProof = req.workerProof
	return a.ack(m, api.ResultScheduled, m.InPlaceOps, reload)
}

// inPlaceWillRun mirrors the real agent's activate: the in-place batch runs
// while a reload is pending. The fake never paces, so that is the only case.
func (a *Agent) inPlaceWillRun(m *api.Manifest) bool {
	return len(m.InPlaceOps) > 0 && a.reloadPending
}

// invalidate answers an in-place batch the worker did not take: an ACK that
// names the stage and clears the baseline, so the next apply is full state plus
// a reload. The applied token stays where it was, because this plan is not it.
func (a *Agent) invalidate(m *api.Manifest, reload *api.ReloadInfo, message string) outcome {
	a.state.Generation++
	a.state.AppliedPlanID = ""
	a.state.AppliedPlanProof = ""
	a.state.WorkerOpsPlanID = ""
	a.state.WorkerOpsPlanProof = ""
	out := a.ack(m, api.ResultScheduled, nil, reload)
	out.result.Error = &api.ApplyError{Stage: "in_place", Message: message}
	return out
}

func (a *Agent) reload(req *applyRequest) outcome {
	m := &req.manifest
	a.performReload()
	a.advance(m, req.appliedProof)
	a.state.RunningPlanID = m.PlanID
	a.state.RunningPlanProof = req.appliedProof
	a.state.WorkerOpsPlanID = m.PlanID
	a.state.WorkerOpsPlanProof = req.appliedProof
	a.state.LKGPlanID = m.PlanID
	a.state.LKGPlanProof = req.appliedProof
	a.lkgFiles = maps.Clone(a.state.Files)
	return a.ack(m, api.ResultReload, nil, &api.ReloadInfo{
		Performed: true,
		OK:        true,
		WorkerPID: a.state.HAProxy.WorkerPID,
	})
}

func (a *Agent) revertLKG(m *api.Manifest) outcome {
	a.state.Files = maps.Clone(a.lkgFiles)
	if a.state.Files == nil {
		a.state.Files = map[string]api.FileAt{}
	}
	a.performReload()
	a.state.Generation++
	a.state.AppliedToken = m.Token
	a.state.AppliedPlanID = a.state.LKGPlanID
	a.state.AppliedPlanProof = a.state.LKGPlanProof
	a.state.RunningPlanID = a.state.LKGPlanID
	a.state.RunningPlanProof = a.state.LKGPlanProof
	a.state.WorkerOpsPlanID = a.state.LKGPlanID
	a.state.WorkerOpsPlanProof = a.state.LKGPlanProof
	return a.ack(m, api.ResultReload, nil, &api.ReloadInfo{
		Performed: true,
		OK:        true,
		WorkerPID: a.state.HAProxy.WorkerPID,
	})
}

// rejectOps answers the way HAProxy refusing a runtime command does: a NACK
// that also invalidates the baseline, so the controller's next apply is full
// state plus a reload.
func (a *Agent) rejectOps(m *api.Manifest, kind string) outcome {
	a.state.AppliedPlanID = ""
	a.state.AppliedPlanProof = ""
	return a.nack(m, "ops", fmt.Sprintf("%s: command rejected by HAProxy", kind))
}

func (a *Agent) firstRejected(ops []api.Op) string {
	for i := range ops {
		if _, rejected := a.rejectedOps[ops[i].Kind]; rejected {
			return ops[i].Kind
		}
	}
	return ""
}

// advance commits the applied baseline. Generation is strictly +1 per
// successful apply, which a test can assert. The worker-ops baseline is not
// part of it: only a reload or an executed in-place batch moves that.
func (a *Agent) advance(m *api.Manifest, proof string) {
	a.state.Generation++
	a.state.AppliedPlanID = m.PlanID
	a.state.AppliedPlanProof = proof
	a.state.AppliedToken = m.Token
}

// storeFiles replaces the held set with the manifest's — the manifest is the
// complete desired state, so absence is deletion. It reports whether anything
// about the set changed.
func (a *Agent) storeFiles(req *applyRequest) bool {
	next := make(map[string]api.FileAt, len(req.manifest.Files))
	for _, f := range req.manifest.Files {
		next[f.Path] = api.FileAt{Digest: f.Digest, Proof: f.Proof, Size: f.Size}
		// Kinds accumulate rather than replace, so a revert to the LKG set
		// still classifies paths this manifest happens not to carry.
		a.kinds[f.Path] = f.Kind
	}
	changed := !maps.Equal(a.state.Files, next)
	a.state.Files = next
	return changed
}

// performReload models the worker re-exec: a new pid, a refreshed inventory,
// and any pending reload consumed.
func (a *Agent) performReload() {
	a.state.HAProxy.WorkerPID++
	a.reloadPending = false
	a.state.ReloadPendingAt = ""
	// CRL files carry over: no file kind identifies them, so a manifest cannot
	// reconstruct what WithInventory seeded.
	inventory := api.Inventory{
		Generation: a.state.Inventory.Generation + 1,
		CRLFiles:   a.state.Inventory.CRLFiles,
	}
	for _, path := range slices.Sorted(maps.Keys(a.state.Files)) {
		switch a.kinds[path] {
		case api.FileKindMap:
			inventory.Maps = append(inventory.Maps, path)
		case api.FileKindCert:
			inventory.Certs = append(inventory.Certs, path)
		case api.FileKindCA:
			inventory.CAFiles = append(inventory.CAFiles, path)
		case api.FileKindCRTList:
			inventory.CRTLists = append(inventory.CRTLists, path)
		}
	}
	a.state.Inventory = inventory
}

func (a *Agent) ack(m *api.Manifest, mode string, executed []api.Op, reload *api.ReloadInfo) outcome {
	result := &api.ApplyResult{
		PlanID:             m.PlanID,
		OK:                 true,
		Mode:               mode,
		AppliedPlanID:      a.state.AppliedPlanID,
		AppliedPlanProof:   a.state.AppliedPlanProof,
		RunningPlanID:      a.state.RunningPlanID,
		RunningPlanProof:   a.state.RunningPlanProof,
		WorkerOpsPlanID:    a.state.WorkerOpsPlanID,
		WorkerOpsPlanProof: a.state.WorkerOpsPlanProof,
		AppliedToken:       a.state.AppliedToken,
		LKGPlanID:          a.state.LKGPlanID,
		LKGPlanProof:       a.state.LKGPlanProof,
		OpResults:          opResults(executed),
		Reload:             reload,
		HAProxy:            a.state.HAProxy,
		At:                 fixedTimestamp,
	}
	if reload != nil && reload.Performed {
		inventory := a.state.Inventory
		result.Inventory = &inventory
	}
	a.state.LastApply = result
	return outcome{status: http.StatusOK, result: result}
}

func (a *Agent) nack(m *api.Manifest, stage, message string) outcome {
	result := &api.ApplyResult{
		PlanID:             m.PlanID,
		OK:                 false,
		Mode:               api.ResultRejected,
		AppliedPlanID:      a.state.AppliedPlanID,
		AppliedPlanProof:   a.state.AppliedPlanProof,
		RunningPlanID:      a.state.RunningPlanID,
		RunningPlanProof:   a.state.RunningPlanProof,
		WorkerOpsPlanID:    a.state.WorkerOpsPlanID,
		WorkerOpsPlanProof: a.state.WorkerOpsPlanProof,
		AppliedToken:       a.state.AppliedToken,
		LKGPlanID:          a.state.LKGPlanID,
		LKGPlanProof:       a.state.LKGPlanProof,
		Error:              &api.ApplyError{Stage: stage, Message: message},
		HAProxy:            a.state.HAProxy,
		At:                 fixedTimestamp,
	}
	a.state.LastApply = result
	return outcome{status: http.StatusOK, result: result}
}

func opResults(ops []api.Op) []api.OpResult {
	if len(ops) == 0 {
		return nil
	}
	results := make([]api.OpResult, 0, len(ops))
	for i := range ops {
		results = append(results, api.OpResult{Kind: ops[i].Kind, OK: true})
	}
	return results
}
