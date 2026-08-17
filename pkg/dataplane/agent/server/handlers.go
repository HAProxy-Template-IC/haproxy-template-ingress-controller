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
	"encoding/json"
	"net/http"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/cli"
)

// handleState answers the controller's view of this pod. With verify=1 the
// digests are a fresh observation of the tree rather than the last one.
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	state, err := s.stateResponse(r.URL.Query().Get("verify") == "1")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, api.ApplyError{Stage: "state", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) stateResponse(verify bool) (api.State, error) {
	if verify {
		if err := s.refreshTree(); err != nil {
			return api.State{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := api.State{
		APIVersion:        api.Version,
		AgentVersion:      s.cfg.AgentVersion,
		PlanSchemaVersion: s.state.PlanSchemaVersion,
		AgentOps:          cli.Kinds(),
		HAProxy:           s.worker,
		Generation:        s.state.Generation,
		AppliedPlanID:     s.state.AppliedPlanID,
		RunningPlanID:     s.state.RunningPlanID,
		WorkerOpsPlanID:   s.state.WorkerOpsPlanID,
		AppliedToken:      s.state.AppliedToken,
		LKGPlanID:         s.state.LKGPlanID,
		Files:             s.tree,
		Inventory:         s.inventory,
		PendingDeletes:    s.deferrals.Pending(),
		LastApply:         s.state.LastApply,
	}
	if !s.state.ReloadPendingAt.IsZero() {
		out.ReloadPendingAt = s.state.ReloadPendingAt.UTC().Format(time.RFC3339)
	}
	return out, nil
}

// refreshTree re-hashes the ownership set so the reported digests are an
// observation, which is what drift prevention compares against.
func (s *Server) refreshTree() error {
	s.mu.Lock()
	paths := append([]string(nil), s.state.ManifestPaths...)
	s.mu.Unlock()

	tree, err := s.store.HashTree(paths)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tree = tree
	s.state.TreeDigest = treeDigest(tree)
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	raw, err := json.Marshal(body)
	if err != nil {
		writeText(w, http.StatusInternalServerError, "the agent could not encode its answer")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(raw, '\n'))
}

func writeText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body + "\n"))
}
