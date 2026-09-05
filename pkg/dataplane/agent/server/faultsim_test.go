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

package server_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/haproxytest"
)

// simulationSteps is how many applies each seed drives. Every step is one
// apply plus its verification, so the whole matrix stays inside a unit test's
// time budget.
const simulationSteps = 24

// TestFaultSimulation drives random manifest sequences against the HAProxy
// model with faults injected, and asserts after every step that the tree is
// either the desired set or the last known good one — never a mix — that the
// generation only moves forward, and that no invariant fired.
func TestFaultSimulation(t *testing.T) {
	for seed := int64(1); seed <= 6; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			newSimulation(t, seed).run()
		})
	}
}

// simulation is the model the test compares the agent against: what the last
// apply asked for, and what the tree looked like when it last succeeded.
type simulation struct {
	t          *testing.T
	h          *harness
	rng        *rand.Rand
	desired    map[string]string
	onDisk     map[string]string
	applied    string
	token      api.Token
	generation uint64
	step       int
	// allowed collects the invariants an injected fault is supposed to record;
	// anything else is a real violation.
	allowed map[string]bool
}

func newSimulation(t *testing.T, seed int64) *simulation {
	t.Helper()
	return &simulation{
		t:       t,
		h:       newHarness(t),
		rng:     rand.New(rand.NewSource(seed)),
		desired: map[string]string{},
		onDisk:  map[string]string{},
		allowed: map[string]bool{},
	}
}

func (s *simulation) run() {
	for s.step = 1; s.step <= simulationSteps; s.step++ {
		s.next()
		s.verify()
	}
}

// next performs one step: a fresh desired set, a fault drawn from the table,
// and the apply that has to survive it.
func (s *simulation) next() {
	injected := faults[s.rng.Intn(len(faults))]
	files, m := s.compose()
	injected.arm(s, &m, files)
	if injected.expects != "" {
		s.allowed[injected.expects] = true
	}
	if m.Mode == api.ModeRevertLKG {
		// A revert names the set it puts back, so it carries no files.
		m.Files, files = nil, nil
	}
	status, raw := s.h.post(&m, files)

	switch status {
	case http.StatusOK:
		s.record(&m, raw)
	case http.StatusConflict, http.StatusBadRequest:
		s.desired = copyOf(s.onDisk)
	default:
		s.t.Fatalf("step %d (%s): unexpected status %d: %s", s.step, injected.name, status, raw)
	}
	injected.disarm(s)
}

func (s *simulation) record(m *api.Manifest, raw []byte) {
	result := api.ApplyResult{}
	require.NoError(s.t, json.Unmarshal(raw, &result))
	if result.OK && m.Mode == api.ModeRevertLKG {
		// The revert put the last known good set back, which the model does
		// not track; continue from what the pod now reports and holds.
		s.generation++
		s.reconcileDisk()
		s.token = m.Token
		require.Equal(s.t, result.LKGPlanID, result.AppliedPlanID,
			"step %d: a revert applied the last known good plan", s.step)
		return
	}
	if result.OK {
		s.onDisk = copyOf(s.desired)
		s.applied = result.AppliedPlanID
		s.token = m.Token
		s.generation++
		return
	}
	// A rejected apply restored the last known good set. That is the tree
	// before this step only while every earlier success advanced the LKG; a
	// runtime-only or file-only apply does not, so read the LKG back from disk.
	s.reconcileDisk()
	require.Equal(s.t, "", s.applied, "step %d: a NACK must leave the baseline unknown", s.step)
}

// compose builds the next desired set and the manifest that asks for it.
func (s *simulation) compose() ([]file, api.Manifest) {
	s.desired[configPath] = fmt.Sprintf("global\n  maxconn %d\n", 1000+s.step)
	switch s.rng.Intn(3) {
	case 0:
		s.desired[fmt.Sprintf("maps/m%d.map", s.rng.Intn(3))] = fmt.Sprintf("key%d value%d\n", s.step, s.step)
	case 1:
		for path := range s.desired {
			if path != configPath {
				delete(s.desired, path)
				break
			}
		}
	}
	files := filesOf(s.desired)
	m := buildManifest(fmt.Sprintf("plan-%d-%d", s.step, s.rng.Intn(1000)), files)
	m.ExpectedPrevPlanID = s.applied
	m.ExpectedPrevToken = s.token
	m.Token = api.Token{LeaderEpoch: 1, RenderSeq: uint64(s.step)}
	if s.applied == "" || s.rng.Intn(2) == 0 {
		m.Mode = api.ModeReload
	}
	return files, m
}

// verify is the invariant set the whole design rests on.
func (s *simulation) verify() {
	state := s.h.state(true)
	assert.Equal(s.t, flatten(s.onDisk), flatten(s.h.tree()),
		"step %d: the tree is neither the desired set nor the last known good one", s.step)
	assert.Equal(s.t, s.generation, state.Generation, "step %d: generation", s.step)
	for _, violation := range s.h.violations() {
		name, _, _ := strings.Cut(violation, "=")
		assert.True(s.t, s.allowed[name], "step %d: unexpected invariant %s", s.step, violation)
	}
	if s.applied != "" {
		assert.Equal(s.t, s.applied, state.AppliedPlanID, "step %d: applied plan", s.step)
	}
}

// fault is one injected failure: arm changes the world before the apply,
// disarm puts it back so the next step starts from a known place.
type fault struct {
	name string
	// expects names the invariant this fault is designed to record, if any.
	expects string
	arm     func(s *simulation, m *api.Manifest, files []file)
	disarm  func(s *simulation)
}

var faults = []fault{
	{
		name:   "none",
		arm:    func(*simulation, *api.Manifest, []file) {},
		disarm: func(*simulation) {},
	},
	{
		name: "reload fails",
		arm: func(s *simulation, _ *api.Manifest, _ []file) {
			s.h.model.With(func(m *haproxytest.Model) { m.ReloadFails = true })
		},
		disarm: func(s *simulation) {
			s.h.model.With(func(m *haproxytest.Model) { m.ReloadFails = false })
		},
	},
	{
		name: "an op is rejected mid-batch",
		arm: func(s *simulation, m *api.Manifest, _ []file) {
			m.Ops = []api.Op{{Kind: api.OpMapAdd, Path: "maps/m0.map", Key: "k", Value: "v"}}
			s.h.model.With(func(model *haproxytest.Model) {
				model.Reject = func(command string) (string, bool) {
					return "No such map file.", strings.HasPrefix(command, "add map")
				}
			})
		},
		disarm: func(s *simulation) {
			s.h.model.With(func(model *haproxytest.Model) { model.Reject = nil })
		},
	},
	{
		name:    "an op kind this agent does not know",
		expects: "ops_executable",
		arm: func(_ *simulation, m *api.Manifest, _ []file) {
			m.Ops = []api.Op{{Kind: "backend_teleport", Backend: "be-a"}}
		},
		disarm: func(*simulation) {},
	},
	{
		name: "a part does not match its digest",
		arm: func(_ *simulation, _ *api.Manifest, files []file) {
			files[0].Content += "corrupted in flight\n"
		},
		disarm: func(*simulation) {},
	},
	{
		name: "a former leader is still dispatching",
		arm: func(_ *simulation, m *api.Manifest, _ []file) {
			m.Token = api.Token{LeaderEpoch: 0, RenderSeq: 1}
		},
		disarm: func(*simulation) {},
	},
	{
		name: "the baseline the ops were composed against is gone",
		arm: func(_ *simulation, m *api.Manifest, _ []file) {
			m.ExpectedPrevPlanID = "plan-from-another-life"
		},
		disarm: func(*simulation) {},
	},
	{
		name: "the disk refuses the write",
		arm: func(s *simulation, _ *api.Manifest, files []file) {
			for _, f := range files {
				if f.Path == configPath {
					continue
				}
				blocked := filepath.Join(s.h.baseDir, f.Path)
				require.NoError(s.t, os.RemoveAll(blocked))
				require.NoError(s.t, os.MkdirAll(blocked, 0o755))
				return
			}
		},
		disarm: func(s *simulation) {
			for path := range s.desired {
				require.NoError(s.t, removeIfDir(filepath.Join(s.h.baseDir, path)))
			}
			s.reconcileDisk()
		},
	},
	{
		name: "the HAProxy container restarted under the agent",
		arm: func(s *simulation, _ *api.Manifest, _ []file) {
			s.h.model.With(func(m *haproxytest.Model) { m.Pid += 3 })
		},
		// A file-only apply never talks to the worker, so the agent may
		// notice the foreign worker only on the next verify — and then it
		// rightly forgets the baseline. Take the agent's word for it.
		disarm: func(s *simulation) { s.applied = s.h.state(true).AppliedPlanID },
	},
	{
		name: "the agent restarted between applies",
		arm: func(s *simulation, _ *api.Manifest, _ []file) {
			s.h = s.h.restart()
		},
		disarm: func(*simulation) {},
	},
	{
		name: "the controller reverts to the last known good set",
		arm: func(s *simulation, m *api.Manifest, _ []file) {
			m.Mode = api.ModeRevertLKG
			if s.applied != "" {
				m.PlanID = s.applied
			}
		},
		disarm: func(*simulation) {},
	},
}

// reconcileDisk re-reads the tree after a fault that could have left a path in
// a shape the model does not track, so the next step compares like for like.
func (s *simulation) reconcileDisk() {
	s.onDisk = copyOf(s.h.tree())
	s.desired = copyOf(s.onDisk)
	state := s.h.state(false)
	s.applied = state.AppliedPlanID
}

func filesOf(set map[string]string) []file {
	paths := make([]string, 0, len(set))
	for path := range set {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	out := make([]file, 0, len(paths))
	for _, path := range paths {
		out = append(out, file{Path: path, Content: set[path], Reload: path == configPath})
	}
	return out
}

func copyOf(set map[string]string) map[string]string {
	out := make(map[string]string, len(set))
	for k, v := range set {
		out[k] = v
	}
	return out
}

// flatten renders a file set so a mismatch reads as a diff of paths and
// contents rather than a map dump.
func flatten(set map[string]string) []string {
	out := make([]string, 0, len(set))
	for path, content := range set {
		out = append(out, path+"="+content)
	}
	sort.Strings(out)
	return out
}

// removeIfDir drops the directory the disk fault put where a file belongs, so
// the next step starts from a tree the agent can write again.
func removeIfDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return nil
	}
	return os.RemoveAll(path)
}
