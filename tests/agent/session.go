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

//go:build agentdocker

package agent

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// session is the controller's side of one pod: the desired file set plus the
// baseline the last ACK reported, so every manifest is fenced the way the
// deployer fences it.
type session struct {
	env            *env
	epoch          uint64
	seq            uint64
	applied        string
	appliedProof   string
	token          api.Token
	workerOps      string
	workerOpsProof string
	lkg            string
	files          map[string]string
}

// newSession starts from the full rendered set: config, both maps, the default
// certificate and its crt-list, and one general file on its own mount.
func newSession(e *env) *session {
	defaultCert := makeCertificate(e.t, "default.test", 1001)
	s := &session{
		env:   e,
		epoch: 1,
		files: map[string]string{
			configPath:      renderedConfig,
			hostMapPath:     hostMapContent,
			noteMapPath:     noteMapContent,
			defaultCertPath: defaultCert.pem,
			crtListPath:     defaultCertFile + "\n",
			generalFilePath: generalFileContent,
		},
	}
	return s
}

func (s *session) set(path, content string) { s.files[path] = content }

func (s *session) remove(path string) { delete(s.files, path) }

// next builds the manifest for the current desired set: a fresh plan id and
// render seq, fenced against the baseline the last ACK reported.
func (s *session) next(mode string) *api.Manifest {
	s.seq++
	// IdentityVersion and the plan proofs are what the deployer sends; without
	// them the agent normalises the manifest as legacy, forcing a reload and
	// dropping every op.
	m := &api.Manifest{
		IdentityVersion:            api.ExactIdentityVersion,
		PlanID:                     planID(s.seq),
		PlanSchemaVersion:          1,
		Token:                      api.Token{LeaderEpoch: s.epoch, RenderSeq: s.seq},
		ExpectedPrevPlanID:         s.applied,
		ExpectedPrevPlanProof:      s.appliedProof,
		ExpectedPrevToken:          s.token,
		ExpectedWorkerOpsPlanID:    s.workerOps,
		ExpectedWorkerOpsPlanProof: s.workerOpsProof,
		Mode:                       mode,
	}
	if mode == api.ModeRevertLKG {
		// A revert names the plan being reverted FROM, which the agent checks
		// is one it holds and is not already running.
		m.PlanID, m.PlanProof = s.applied, s.appliedProof
	}
	for _, path := range slices.Sorted(maps.Keys(s.files)) {
		content := s.files[path]
		m.Files = append(m.Files, api.File{
			Path:           path,
			Digest:         renderplan.DigestString(content),
			Proof:          renderplan.DigestString(content),
			Size:           int64(len(content)),
			Kind:           fileKind(path),
			ReloadOnChange: reloadOnChange(path),
		})
	}
	return m
}

func planID(seq uint64) string {
	return fmt.Sprintf("plan-%d", seq)
}

// allParts sends every file's content. The agent keeps what it already holds,
// so this only costs bytes; a test that wants the missing-parts path passes nil.
func (s *session) allParts() map[string]io.Reader {
	parts := map[string]io.Reader{}
	for path, content := range s.files {
		parts[path] = strings.NewReader(content)
	}
	return parts
}

// apply sends the manifest and records the new baseline. It fails the test on a
// transport error; a NACK comes back as a result for the caller to assert on.
// apply sends one manifest and returns its final outcome: an apply the agent
// only scheduled (a reload inside the pacing window) is followed until the
// pacer ran it, the way the deployer polls /v1/state at scheduled_at.
func (s *session) apply(m *api.Manifest, parts map[string]io.Reader) *api.ApplyResult {
	s.env.t.Helper()
	result, elapsed := s.timedApply(m, parts)
	s.env.t.Logf("apply %s mode=%s took %s", m.PlanID, m.Mode, elapsed.Round(time.Millisecond))
	if result.OK && result.Mode == api.ResultScheduled {
		result = s.awaitScheduled(m.PlanID)
	}
	s.absorb(result)
	return result
}

// awaitScheduled polls the agent until the reload it scheduled for planID has
// run and reports that run's outcome.
func (s *session) awaitScheduled(planID string) *api.ApplyResult {
	s.env.t.Helper()
	var final *api.ApplyResult
	waitFor(s.env.t, "the scheduled reload of "+planID, convergeBudget, func() error {
		state, err := s.env.client.State(context.Background(), false)
		if err != nil {
			return err
		}
		if state.ReloadPendingAt != "" || state.LastApply == nil || state.LastApply.PlanID != planID ||
			state.LastApply.Mode == api.ResultScheduled {
			return fmt.Errorf("reload of %s still pending", planID)
		}
		final = state.LastApply
		return nil
	})
	s.env.t.Logf("scheduled reload of %s ran: ok=%t mode=%s", planID, final.OK, final.Mode)
	return final
}

func (s *session) timedApply(m *api.Manifest, parts map[string]io.Reader) (*api.ApplyResult, time.Duration) {
	s.env.t.Helper()
	start := time.Now()
	result, err := s.env.client.Apply(context.Background(), m, parts, nil)
	elapsed := time.Since(start)
	require.NoError(s.env.t, err)
	return result, elapsed
}

// applyExpectingRefusal returns the transport-level error a 409 produces.
func (s *session) applyExpectingRefusal(t *testing.T, m *api.Manifest, parts map[string]io.Reader) error {
	t.Helper()
	_, err := s.env.client.Apply(context.Background(), m, parts, nil)
	require.Error(t, err, "the agent accepted an apply it should have refused")
	return err
}

// absorb takes the baseline the agent reports — after a NACK that is an empty
// applied plan, which the next manifest must expect.
func (s *session) absorb(result *api.ApplyResult) {
	if result == nil {
		return
	}
	s.applied = result.AppliedPlanID
	s.appliedProof = result.AppliedPlanProof
	s.token = result.AppliedToken
	s.workerOps = result.WorkerOpsPlanID
	s.workerOpsProof = result.WorkerOpsPlanProof
	s.lkg = result.LKGPlanID
}

func fileKind(path string) string {
	switch {
	case strings.HasPrefix(path, "maps/"):
		return api.FileKindMap
	case path == crtListPath:
		return api.FileKindCRTList
	case strings.HasPrefix(path, "ssl/"):
		return api.FileKindCert
	case strings.HasPrefix(path, "general/"):
		return api.FileKindGeneral
	default:
		return api.FileKindConfig
	}
}

// reloadOnChange marks the files HAProxy can only pick up by reloading: the
// configuration and the general files it reads at parse time.
func reloadOnChange(path string) bool {
	return path == configPath || strings.HasPrefix(path, "general/")
}
