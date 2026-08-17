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
	"context"
	"errors"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

// pacerTick is how often the agent checks whether a scheduled reload is due.
// A scheduled reload is never cancelled, so the tick only decides its latency.
const pacerTick = 100 * time.Millisecond

// workerSettleTimeout bounds the wait for the new worker to answer after a
// reload. Past it the apply reports what it knows rather than blocking.
const workerSettleTimeout = 30 * time.Second

// reload either reloads now or schedules one, depending on the pacing window.
func (r *applyRun) reload(reason string) error {
	r.server.logger.Info("reloading", "plan_id", r.manifest.PlanID, "reason", reason)
	if due, open := r.server.pacingWindow(); open {
		return r.schedule(due)
	}
	if err := r.performReload(); err != nil {
		return r.abort("reload", err)
	}
	r.result.Mode = api.ResultReload
	r.server.setPhase(phaseReloaded, r.manifest.PlanID)
	return nil
}

// schedule defers the reload to the end of the pacing window. The controller
// polls /v1/state to learn when it happened.
func (r *applyRun) schedule(due time.Time) error {
	r.server.schedulePendingReload(due, r.manifest.PlanID)
	r.result.Mode = api.ResultScheduled
	r.result.Reload = &api.ReloadInfo{ScheduledAt: due.UTC().Format(time.RFC3339)}
	r.server.setPhase(phaseScheduled, r.manifest.PlanID)
	return nil
}

// performReload asks the master to re-exec and waits until the new worker
// answers, because an op sent to the outgoing worker would be lost.
func (r *applyRun) performReload() error {
	start := time.Now()
	logs, err := r.server.runtime.Reload()
	info := &api.ReloadInfo{Performed: true, OK: err == nil, Output: logs, TookMs: time.Since(start).Milliseconds()}
	r.result.Reload = info
	if err != nil {
		r.server.metrics.reloads.WithLabelValues("failed").Inc()
		r.deterministic = true
		return errors.New(logs)
	}
	r.server.metrics.reloads.WithLabelValues("ok").Inc()
	worker, settleErr := r.server.awaitNewWorker()
	info.WorkerPID = worker.WorkerPID
	info.TookMs = time.Since(start).Milliseconds()
	if settleErr != nil {
		return settleErr
	}
	r.server.recordReload(r.manifest.PlanID)
	return nil
}

// awaitNewWorker blocks until the worker socket answers with a pid different
// from the one the agent recorded before the reload.
func (s *Server) awaitNewWorker() (api.HAProxyInfo, error) {
	previous := s.workerIdentity()
	deadline := time.Now().Add(workerSettleTimeout)
	for {
		info, err := s.runtime.Info()
		if err == nil && info.WorkerPID != previous.WorkerPID {
			s.adoptWorker(info)
			return info, nil
		}
		if time.Now().After(deadline) {
			return previous, errors.New("the new worker did not answer show info after the reload")
		}
		time.Sleep(pacerTick)
	}
}

// pacer fires reloads whose window has passed. It is the only input to the
// state machine besides the apply handler.
func (s *Server) pacer(ctx context.Context) error {
	ticker := time.NewTicker(pacerTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.reloadWake:
		case <-ticker.C:
		}
		s.firePendingReload()
	}
}

// firePendingReload performs the reload an earlier apply scheduled. A failure
// here restores the last known good set, exactly like a synchronous one.
func (s *Server) firePendingReload() {
	s.apply.Lock()
	defer s.apply.Unlock()
	planID, due := s.pendingReload()
	if planID == "" || time.Now().Before(due) {
		return
	}
	run := &applyRun{
		server:   s,
		manifest: &api.Manifest{PlanID: planID, Mode: api.ModeReload},
		result:   api.ApplyResult{PlanID: planID, OK: true, Mode: api.ResultReload, At: time.Now().UTC().Format(time.RFC3339)},
	}
	s.clearPendingReload()
	if err := run.performReload(); err != nil {
		s.logger.Error("the scheduled reload failed", "plan_id", planID, "error", err)
		_ = run.abort("scheduled_reload", err)
	}
	s.mu.Lock()
	s.applyResultLocked(&run.result)
	s.state.LastApply = &run.result
	if !run.result.OK {
		s.state.AppliedPlanID = ""
	}
	if err := s.states.save(s.state); err != nil {
		s.logger.Error("could not persist the agent state", "error", err)
	}
	s.mu.Unlock()
	s.metrics.applies.WithLabelValues(run.result.Mode).Inc()
}

// readBack compares the running state with the desired one after a runtime
// apply. A lost or truncated command must not latch, so a divergence reloads.
func (s *Server) readBack(run *applyRun) {
	if run.result.Mode != api.ResultRuntime || !run.result.OK {
		return
	}
	diverged := false
	for _, backend := range dedupe(run.touchedBackends) {
		if _, err := s.runtime.ServerNames(backend); err != nil {
			diverged = true
			s.logger.Warn("read-back could not read a backend", "backend", backend, "error", err)
		}
	}
	for _, path := range dedupe(run.touchedMaps) {
		if s.mapDiverged(path) {
			diverged = true
		}
	}
	if !diverged {
		return
	}
	s.metrics.divergence.Inc()
	s.apply.Lock()
	defer s.apply.Unlock()
	due, open := s.pacingWindow()
	if open {
		s.schedulePendingReload(due, s.snapshot().AppliedPlanID)
		return
	}
	s.selfReload()
}

// mapDiverged reports whether the map file on disk and the map the worker
// holds disagree on their key sets.
func (s *Server) mapDiverged(path string) bool {
	running, err := s.runtime.MapEntries(path)
	if err != nil {
		s.logger.Warn("read-back could not read a map", "map", path, "error", err)
		return true
	}
	desired, err := s.readMapFile(path)
	if err != nil {
		s.logger.Warn("read-back could not read a map file", "map", path, "error", err)
		return true
	}
	if len(running) != len(desired) {
		return true
	}
	for key := range desired {
		if _, present := running[key]; !present {
			return true
		}
	}
	return false
}

// selfReload reloads outside an apply, after a read-back found a divergence.
func (s *Server) selfReload() {
	planID := s.snapshot().AppliedPlanID
	run := &applyRun{
		server:   s,
		manifest: &api.Manifest{PlanID: planID, Mode: api.ModeReload},
		result:   api.ApplyResult{PlanID: planID, OK: true, Mode: api.ResultReload},
	}
	if err := run.performReload(); err != nil {
		s.logger.Error("the divergence reload failed", "error", err)
	}
}

func dedupe(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := values[:0]
	for _, v := range values {
		if _, duplicate := seen[v]; duplicate {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
