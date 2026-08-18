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

package server

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/files"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// nackCooldown is how long the agent refuses to redo work for a manifest
// HAProxy itself rejected. Anything shorter turns a bad render into a loop.
const nackCooldown = 60 * time.Second

// setPhase records how far the in-flight apply got, so a restart knows whether
// the tree can be a mix of two plans.
func (s *Server) setPhase(p phase, planID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Phase = p
	s.state.InFlightPlanID = planID
	if err := s.states.save(s.state); err != nil {
		s.logger.Error("could not persist the apply phase", "phase", p, "error", err)
	}
}

// journal hands out the live journal. Only the apply path mutates it, and it
// holds the apply lock for the whole transaction, so no second writer exists.
func (s *Server) journal() *files.Journal {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &s.state.Journal
}

func (s *Server) ownedPaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.state.ManifestPaths...)
}

func (s *Server) baselineUnknown() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.AppliedPlanID == ""
}

func (s *Server) reloadPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.state.ReloadPendingAt.IsZero()
}

func (s *Server) pendingReload() (planID string, due time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.PendingReloadPlanID, s.state.ReloadPendingAt
}

// pacingWindow reports when the next reload may run and whether that is later
// than now, which is what --reload-interval-min buys.
func (s *Server) pacingWindow() (due time.Time, open bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	due = s.lastReload.Add(s.cfg.ReloadIntervalMin)
	return due, time.Now().Before(due)
}

func (s *Server) schedulePendingReload(due time.Time, planID string) {
	s.mu.Lock()
	s.state.ReloadPendingAt = due
	s.state.PendingReloadPlanID = planID
	err := s.states.save(s.state)
	s.mu.Unlock()
	if err != nil {
		s.logger.Error("could not persist the scheduled reload", "error", err)
	}
	select {
	case s.reloadWake <- struct{}{}:
	default:
	}
}

// coalesceIntoPendingReload points the scheduled reload at the newest plan.
// The reload itself is never cancelled or moved.
func (s *Server) coalesceIntoPendingReload(planID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.PendingReloadPlanID = planID
}

func (s *Server) clearPendingReload() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.ReloadPendingAt = time.Time{}
	s.state.PendingReloadPlanID = ""
}

// recordReload is what a completed reload means: the worker is running the
// plan whose files were on disk when it re-executed, its own binary accepted
// them, and the journal's job is done. An empty planID means the agent cannot
// name that set — after a crash mid-apply — so nothing is promoted.
func (s *Server) recordReload(planID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastReload = time.Now()
	s.state.RunningPlanID = planID
	s.state.WorkerOpsPlanID = planID
	if planID == "" {
		return
	}
	s.state.LKGPlanID = planID
	if err := s.store.ClearJournal(&s.state.Journal); err != nil {
		s.logger.Error("could not clear the backup journal", "error", err)
	}
}

// lkgPlanID is the plan whose file set a rollback puts back.
func (s *Server) lkgPlanID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.LKGPlanID
}

func (s *Server) recordWorkerOps(planID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.WorkerOpsPlanID = planID
}

// invalidateBaseline makes the next apply a full state plus a reload. It is
// the answer to anything that leaves the running worker unexplained.
func (s *Server) invalidateBaseline() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.AppliedPlanID = ""
	s.state.WorkerOpsPlanID = ""
	s.baselineInvalidations++
}

// invalidationCount is what an apply captures at its start, so its commit can
// tell whether the baseline was invalidated underneath it.
func (s *Server) invalidationCount() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.baselineInvalidations
}

func (s *Server) workerOpsBaselineMatches(expected string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return expected != "" && expected == s.state.WorkerOpsPlanID
}

// adoptWorker records the worker the agent is now talking to and refreshes the
// inventory, which only a reload or a foreign worker can change.
func (s *Server) adoptWorker(info api.HAProxyInfo) {
	inventory, err := s.runtime.Inventory(0)
	if err != nil {
		s.logger.Warn("could not refresh the runtime inventory", "error", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.worker = info
	s.state.ExpectedWorker = info
	if err == nil {
		inventory.Generation = s.inventory.Generation + 1
		s.inventory = inventory
	}
}

// foldCreated records the runtime stores a completed batch created. Without
// it the controller composes `cert_new` again on the next rotation, which
// HAProxy refuses because the store is already there; the generation advances
// so the delta rides the ACK.
func (s *Server) foldCreated(run *applyRun) {
	if len(run.createdCerts) == 0 && len(run.createdCAs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, path := range run.createdCerts {
		s.inventory.Certs, changed = withPath(s.inventory.Certs, path, changed)
	}
	for _, path := range run.createdCAs {
		s.inventory.CAFiles, changed = withPath(s.inventory.CAFiles, path, changed)
	}
	if changed {
		s.inventory.Generation++
	}
}

// withPath adds a path to an inventory listing. It copies rather than appends
// in place, because the last ACK handed the caller that same slice.
func withPath(list []string, path string, changed bool) ([]string, bool) {
	if slices.Contains(list, path) {
		return list, changed
	}
	out := make([]string, len(list), len(list)+1)
	copy(out, list)
	return append(out, path), true
}

// checkWorker compares the worker the agent is about to talk to with the one
// it recorded. A foreign worker means the HAProxy container restarted, so the
// runtime state the agent believes in is gone.
func (s *Server) checkWorker() error {
	info, err := s.runtime.Info()
	if err != nil {
		return err
	}
	s.mu.Lock()
	expected := s.state.ExpectedWorker
	s.mu.Unlock()
	if expected.WorkerPID != 0 && expected.WorkerPID == info.WorkerPID {
		return nil
	}
	s.logger.Warn("the HAProxy worker changed identity",
		"expected_pid", expected.WorkerPID, "observed_pid", info.WorkerPID)
	s.adoptWorker(info)
	s.invalidateBaseline()
	return fmt.Errorf("worker pid changed from %d to %d", expected.WorkerPID, info.WorkerPID)
}

// observeTree updates the digests the agent reports without hashing anything:
// every staged part was verified against its manifest digest before it landed.
func (s *Server) observeTree(m *api.Manifest, staged map[string]*files.Staged, deleted []string) {
	declared := make(map[string]api.File, len(m.Files))
	for _, f := range m.Files {
		declared[f.Path] = f
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tree == nil {
		s.tree = map[string]api.FileAt{}
	}
	for path := range staged {
		s.tree[path] = api.FileAt{Digest: declared[path].Digest, Size: declared[path].Size}
	}
	for _, path := range deleted {
		delete(s.tree, path)
	}
}

// promoteLKG advances the rollback baseline when the controller reports that
// its own haproxy -c passed on the plan this pod already applied.
func (s *Server) promoteLKG(m *api.Manifest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.ValidatedPlanID == "" || m.ValidatedPlanID != s.state.AppliedPlanID {
		return nil
	}
	if s.state.LKGPlanID == s.state.AppliedPlanID {
		return nil
	}
	if err := s.store.ClearJournal(&s.state.Journal); err != nil {
		return err
	}
	s.state.LKGPlanID = s.state.AppliedPlanID
	s.logger.Info("promoted the last known good plan", "lkg_plan_id", s.state.LKGPlanID)
	return s.states.save(s.state)
}

// restoreJournal puts the last known good file set back and re-observes the
// tree, because the restored digests are not the ones the manifest declared.
func (s *Server) restoreJournal() error {
	s.mu.Lock()
	err := s.store.Restore(&s.state.Journal, s.cfg.ConfigFile)
	paths := lkgPaths(s.state.ManifestPaths, &s.state.Journal)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	tree, hashErr := s.store.HashTree(paths)
	if hashErr != nil {
		return hashErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tree = tree
	s.state.ManifestPaths = paths
	s.state.TreeDigest = treeDigest(tree)
	// The tree is the last known good set again, so the backups have nothing
	// left to protect; the next apply starts a fresh journal from here.
	return s.store.ClearJournal(&s.state.Journal)
}

// lkgPaths is the ownership set a restored journal leaves behind: what the
// current manifest owns, minus the paths it created, plus the ones it deleted.
func lkgPaths(current []string, j *files.Journal) []string {
	set := make(map[string]struct{}, len(current))
	for _, path := range current {
		set[path] = struct{}{}
	}
	for _, e := range j.Entries {
		switch e.Kind {
		case files.KindCreated:
			delete(set, e.Path)
		case files.KindDeleted:
			set[e.Path] = struct{}{}
		}
	}
	paths := make([]string, 0, len(set))
	for path := range set {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// cachedNACK answers a manifest the agent already knows HAProxy rejects,
// without redoing the work. Only deterministic rejections get here.
func (s *Server) cachedNACK(digest string) *api.ApplyResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.state.NACK
	if record == nil || record.ManifestDigest != digest {
		return nil
	}
	if time.Now().After(record.Until) {
		s.state.NACK = nil
		return nil
	}
	s.logger.Info("refusing a manifest that is still known bad", "reason", record.Reason)
	return record.Result
}

func (s *Server) rememberNACK(digest, reason string, result *api.ApplyResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.NACK = &nackRecord{
		ManifestDigest: digest,
		Reason:         reason,
		Until:          time.Now().Add(nackCooldown),
		Result:         result,
	}
}

// recoverFromCrash reloads what is on disk when the agent died mid-apply. The
// tree may be a mix of two plans, and only HAProxy's own parse can tell.
func (s *Server) recoverFromCrash() error {
	s.mu.Lock()
	interrupted := s.state.Phase != phaseIdle && s.state.Phase != phaseCommitted
	planID := s.state.InFlightPlanID
	s.mu.Unlock()
	if !interrupted {
		return nil
	}
	s.logger.Warn("an apply was interrupted; reloading what is on disk", "plan_id", planID)
	s.invalidateBaseline()
	run := &applyRun{
		server:   s,
		manifest: &api.Manifest{PlanID: planID, Mode: api.ModeReload},
		result:   api.ApplyResult{PlanID: planID, OK: true, Mode: api.ResultReload},
	}
	err := run.performReload("")
	s.mu.Lock()
	s.state.Phase = phaseIdle
	s.state.InFlightPlanID = ""
	if err != nil {
		s.state.RunningPlanID = ""
	}
	saveErr := s.states.save(s.state)
	s.mu.Unlock()
	if err != nil {
		s.logger.Error("the recovery reload failed", "error", err)
	}
	return saveErr
}

// readMapFile reads the desired entries of a map file straight off the disk,
// through the same parser the render composed the plan's entries with, so the
// two cannot disagree on what a line means.
func (s *Server) readMapFile(path string) (map[string][]string, error) {
	abs, err := s.store.Abs(path)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(filepath.Clean(abs))
	if err != nil {
		return nil, err
	}
	parsed := renderplan.ParseMapEntries(string(content))
	if len(parsed) > api.MaxMapEntries {
		return nil, fmt.Errorf("map file %s has more than %d entries", path, api.MaxMapEntries)
	}
	entries := make(map[string][]string, len(parsed))
	for _, e := range parsed {
		entries[e.Key] = append(entries[e.Key], e.Value)
	}
	return entries, nil
}
