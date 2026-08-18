//go:build integration

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

package integration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rekby/fixenv"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// scheduledReloadBudget bounds the wait for a reload the agent postponed into
// its pacing window.
const scheduledReloadBudget = 60 * time.Second

// podFile is one file of the desired set with the kind the render declared for
// it. The kind decides which runtime command can carry a change, so it is data
// the test states rather than something anything infers from the bytes.
type podFile struct {
	content string
	kind    string
}

// Session is the controller's side of one HAProxy pod: the desired file set,
// the plan that describes it, and the baseline the pod's last ACK reported.
// Every apply is fenced exactly the way the deployer fences it.
type Session struct {
	t       *testing.T
	haproxy *HAProxyInstance
	client  *client.Client

	epoch    uint64
	seq      uint64
	files    map[string]podFile
	crtLists map[string][]renderplan.CRTListEntry
	// declared is every path this session ever put in a manifest, which is the
	// ownership set: the agent deletes a path it owns and no longer sees, and
	// leaves everything else — including files HAProxy itself writes — alone.
	declared map[string]struct{}

	applied   *renderplan.Plan
	appliedID string
	token     api.Token
	workerOps string
}

// NewSession starts a controller session against the test pod. Its desired set
// is empty: a test declares the configuration and the auxiliary files it wants
// and then applies them.
func NewSession(t *testing.T, env fixenv.Env) *Session {
	t.Helper()
	return &Session{
		t:        t,
		haproxy:  TestHAProxy(env),
		client:   TestAgentClient(env),
		epoch:    1,
		files:    map[string]podFile{},
		crtLists: map[string][]renderplan.CRTListEntry{},
		declared: map[string]struct{}{},
	}
}

// SetConfig declares the rendered HAProxy configuration.
func (s *Session) SetConfig(content string) {
	s.put(ConfigPath, podFile{content: content, kind: renderplan.FileKindConfig})
}

// Set declares one auxiliary file, its kind derived from its directory.
func (s *Session) Set(path, content string) {
	s.put(path, podFile{content: content, kind: kindForPath(path)})
}

func (s *Session) put(path string, file podFile) {
	s.files[path] = file
	s.declared[path] = struct{}{}
}

// SetOfKind declares one auxiliary file whose kind its directory does not
// imply — a CA bundle lives beside ordinary general files.
func (s *Session) SetOfKind(path, content, kind string) {
	s.put(path, podFile{content: content, kind: kind})
}

// SetCRTList declares a crt-list the way a generator macro does: as entries,
// plus the file they render to. Only a declared entry list lets a change to
// the list run as runtime commands.
//
// An entry's Cert is the crt-list line token, which HAProxy resolves against
// `crt-base` — so it is the bare filename, not the `ssl/<file>` path the
// certificate itself is declared under.
func (s *Session) SetCRTList(path string, entries ...renderplan.CRTListEntry) {
	s.crtLists[path] = entries
	s.put(path, podFile{content: crtListContent(entries), kind: renderplan.FileKindCRTList})
}

// crtListContent renders entries into the file HAProxy parses: the
// certificate, its ssl options in brackets, then the SNI filters.
func crtListContent(entries []renderplan.CRTListEntry) string {
	var b strings.Builder
	for i := range entries {
		entry := &entries[i]
		b.WriteString(entry.Cert)
		if len(entry.Options) > 0 {
			b.WriteString(" [")
			for j, option := range entry.Options {
				if j > 0 {
					b.WriteString(" ")
				}
				b.WriteString(strings.Join(append([]string{option.Name}, option.Args...), " "))
			}
			b.WriteString("]")
		}
		for _, filter := range entry.SNIFilters {
			b.WriteString(" " + filter)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Remove drops a file from the desired set; the next apply deletes it from the
// pod, because the manifest is the complete desired state.
func (s *Session) Remove(path string) {
	delete(s.files, path)
}

// RemoveDir drops every declared file under one directory.
func (s *Session) RemoveDir(dir string) {
	for path := range s.files {
		if strings.HasPrefix(path, dir+"/") {
			delete(s.files, path)
		}
	}
}

// Paths lists the declared manifest paths in apply order.
func (s *Session) Paths() []string {
	return slices.Sorted(maps.Keys(s.files))
}

// Dropped lists the paths this session owns but no longer declares: the agent
// must have deleted each one from the pod.
func (s *Session) Dropped() []string {
	var gone []string
	for path := range s.declared {
		if _, kept := s.files[path]; !kept {
			gone = append(gone, path)
		}
	}
	slices.Sort(gone)
	return gone
}

// Content returns a declared file's content.
func (s *Session) Content(path string) string {
	return s.files[path].content
}

// kindForPath maps a manifest path to the file kind its directory implies,
// which is how the chart lays the tree out.
func kindForPath(path string) string {
	switch {
	case path == ConfigPath:
		return renderplan.FileKindConfig
	case strings.HasPrefix(path, MapsDir+"/"):
		return renderplan.FileKindMap
	case strings.HasPrefix(path, SSLDir+"/"):
		return renderplan.FileKindCert
	default:
		return renderplan.FileKindGeneral
	}
}

// reloadOnChange marks the files HAProxy can only pick up by reloading: the
// configuration and the general files it reads while parsing it.
func reloadOnChange(kind string) bool {
	return kind == renderplan.FileKindConfig || kind == renderplan.FileKindGeneral
}

// Plan is the render this session describes. The whole configuration is one
// core section: nothing in this suite parses HAProxy syntax, so no finer
// structure can be declared, and any configuration change is a reload — the
// same verdict the controller reaches for text no section accounts for.
// Auxiliary files carry their entries, which is what makes a map or a
// certificate change reload-free.
func (s *Session) Plan() *renderplan.Plan {
	config := s.files[ConfigPath].content
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections: []renderplan.Section{{
			Kind:       renderplan.SectionKindCore,
			Name:       ConfigPath,
			TextDigest: renderplan.DigestString(config),
			Length:     len(config),
		}},
		Maps:     map[string]renderplan.Map{},
		CRTLists: map[string]renderplan.CRTList{},
	}
	for _, path := range s.Paths() {
		file := s.files[path]
		plan.Files = append(plan.Files, renderplan.File{
			Path:           path,
			Kind:           file.kind,
			ReloadOnChange: reloadOnChange(file.kind),
			Digest:         renderplan.DigestString(file.content),
			Size:           int64(len(file.content)),
		})
		switch file.kind {
		case renderplan.FileKindMap:
			plan.Maps[path] = renderplan.Map{Path: path, Entries: renderplan.ParseMapEntries(file.content)}
		case renderplan.FileKindCRTList:
			plan.CRTLists[path] = renderplan.CRTList{Path: path, Entries: s.crtLists[path]}
		}
	}
	plan.ComputeID()
	return plan
}

// State reads the pod's baseline from the agent.
func (s *Session) State(ctx context.Context) *api.State {
	s.t.Helper()
	state, err := s.client.State(ctx, false)
	require.NoError(s.t, err, "reading the agent's state")
	return state
}

// Decide is what the deployer does for this pod: diff the render against the
// baseline the pod reported and compose the apply.
func (s *Session) Decide(ctx context.Context) deployplan.Decision {
	s.t.Helper()
	state := s.State(ctx)
	baseline := &deployplan.Baseline{
		Applied:               s.applied,
		Running:               s.applied,
		WorkerOps:             s.applied,
		Inventory:             state.Inventory,
		Caps:                  deployplan.CapsFor(state.HAProxy.Version, state.AgentOps),
		PendingServerDeletes:  len(state.PendingDeletes.Servers),
		PendingBackendDeletes: len(state.PendingDeletes.Backends),
		ReloadPending:         state.ReloadPendingAt != "",
	}
	if s.appliedID == "" {
		// The pod never ACKed a plan of ours, so there is nothing to diff
		// against: the deployer sends full state and reloads.
		baseline.Applied, baseline.Running, baseline.WorkerOps = nil, nil, nil
	}
	return deployplan.Diff(s.Plan(), baseline)
}

// Apply sends one decision and returns the pod's final verdict. An apply the
// agent only scheduled — a reload inside the pacing window — is followed until
// the pacer has run it, the way the deployer polls /v1/state at scheduled_at.
func (s *Session) Apply(ctx context.Context, decision deployplan.Decision) *api.ApplyResult {
	s.t.Helper()
	plan := s.Plan()
	s.seq++
	manifest := &api.Manifest{
		PlanID:                  plan.ID,
		PlanSchemaVersion:       plan.SchemaVersion,
		Token:                   api.Token{LeaderEpoch: s.epoch, RenderSeq: s.seq},
		ExpectedPrevPlanID:      s.appliedID,
		ExpectedPrevToken:       s.token,
		ExpectedWorkerOpsPlanID: s.workerOps,
		// Every apply this suite makes reached HAProxy, so the plan the pod
		// already holds is the newest one that passed validation.
		ValidatedPlanID: s.appliedID,
		Files:           decision.Files,
		Ops:             decision.Ops,
		InPlaceOps:      decision.InPlace,
		Mode:            decision.Mode,
	}

	start := time.Now()
	result, err := s.send(ctx, manifest)
	require.NoError(s.t, err, "applying plan %s", plan.ID)
	s.t.Logf("apply %s verdict=%s mode=%s ops=%d took %s",
		plan.ID, decision.Verdict, manifest.Mode, len(decision.Ops), time.Since(start).Round(time.Millisecond))

	if result.OK && result.Mode == api.ResultScheduled {
		result = s.awaitScheduled(ctx, plan.ID)
	}
	s.absorb(plan, result)
	return result
}

// ApplyDesired decides and applies in one step, which is what almost every
// test wants.
func (s *Session) ApplyDesired(ctx context.Context) (deployplan.Decision, *api.ApplyResult) {
	s.t.Helper()
	decision := s.Decide(ctx)
	return decision, s.Apply(ctx, decision)
}

// MustApply applies the desired set and fails the test on a NACK.
func (s *Session) MustApply(ctx context.Context) deployplan.Decision {
	s.t.Helper()
	decision, result := s.ApplyDesired(ctx)
	require.True(s.t, result.OK, "apply was rejected: %s", applyError(result))
	return decision
}

// send makes the apply the deployer makes: content travels only for the files
// the agent answers that it does not hold, so an unchanged file set carries no
// bytes and the pod writes nothing.
func (s *Session) send(ctx context.Context, manifest *api.Manifest) (*api.ApplyResult, error) {
	result, err := s.client.Apply(ctx, manifest, nil, nil)
	var missing *client.MissingError
	if !errors.As(err, &missing) {
		return result, err
	}
	return s.client.Apply(ctx, manifest, s.parts(missing.Missing), nil)
}

// parts is the content of the named files.
func (s *Session) parts(paths []string) map[string]io.Reader {
	parts := make(map[string]io.Reader, len(paths))
	for _, path := range paths {
		parts[path] = strings.NewReader(s.files[path].content)
	}
	return parts
}

// awaitScheduled polls until the reload the agent scheduled for planID has run
// and reports that run's outcome.
func (s *Session) awaitScheduled(ctx context.Context, planID string) *api.ApplyResult {
	s.t.Helper()
	deadline := time.Now().Add(scheduledReloadBudget)
	for time.Now().Before(deadline) {
		state, err := s.client.State(ctx, false)
		if err == nil && state.ReloadPendingAt == "" && state.LastApply != nil &&
			state.LastApply.PlanID == planID && state.LastApply.Mode != api.ResultScheduled {
			s.t.Logf("scheduled reload of %s ran: ok=%t mode=%s",
				planID, state.LastApply.OK, state.LastApply.Mode)
			return state.LastApply
		}
		time.Sleep(200 * time.Millisecond)
	}
	s.t.Fatalf("the reload scheduled for %s never ran within %s", planID, scheduledReloadBudget)
	return nil
}

// absorb records the baseline the agent reported. After a NACK that is an
// empty applied plan, which the next manifest must expect.
func (s *Session) absorb(plan *renderplan.Plan, result *api.ApplyResult) {
	if result == nil {
		return
	}
	s.appliedID = result.AppliedPlanID
	s.token = result.AppliedToken
	s.workerOps = result.WorkerOpsPlanID
	s.applied = nil
	if result.AppliedPlanID == plan.ID {
		s.applied = plan
	}
}

// applyError renders a NACK for a failure message.
func applyError(result *api.ApplyResult) string {
	if result == nil {
		return "no result"
	}
	if result.Error != nil {
		return fmt.Sprintf("%s: %s", result.Error.Stage, result.Error.Message)
	}
	if result.Reload != nil && !result.Reload.OK {
		return "reload: " + result.Reload.Output
	}
	return "mode " + result.Mode
}
