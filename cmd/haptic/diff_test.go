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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/dataplanetest"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/planblob"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func TestParseDiffSource(t *testing.T) {
	tests := []struct {
		name      string
		values    []string
		wantFiles []string
		wantPod   *podRef
		wantErr   string
	}{
		{name: "nothing named is the default pod", values: nil},
		{name: "one file", values: []string{"a.yaml"}, wantFiles: []string{"a.yaml"}},
		{name: "several files", values: []string{"a.yaml", "b.yaml"}, wantFiles: []string{"a.yaml", "b.yaml"}},
		{
			name:    "a pod",
			values:  []string{"pod://haptic/haproxy-0"},
			wantPod: &podRef{namespace: "haptic", name: "haproxy-0"},
		},
		{
			name:    "a pod without a namespace is not a reference",
			values:  []string{"pod://haproxy-0"},
			wantErr: "not a pod reference",
		},
		{
			name:    "files and a pod are not one side",
			values:  []string{"a.yaml", "pod://haptic/haproxy-0"},
			wantErr: "names both files and a pod",
		},
		{
			name:    "two pods are not one side",
			values:  []string{"pod://haptic/haproxy-0", "pod://haptic/haproxy-1"},
			wantErr: "names both files and a pod",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source, err := parseDiffSource("--from", tt.values)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantFiles, source.files)
			assert.Equal(t, tt.wantPod, source.pod)
		})
	}
}

// diffPlan builds the smallest plan every rule in the verdict table touches:
// one core section, one recorded backend, one map, and the two files behind
// them. mutate turns it into the "to" side.
func diffPlan(mutate func(*renderplan.Plan)) *renderplan.Plan {
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections: []renderplan.Section{
			{Kind: renderplan.SectionKindCore, Name: "core#0", TextDigest: "core-1", Length: 32},
			{Kind: renderplan.SectionKindBackend, Name: "be", TextDigest: "text-1", Length: 16},
		},
		Backends: map[string]renderplan.Backend{"be": {
			Name:         "be",
			Shape:        renderplan.ShapeStructural,
			Servers:      []renderplan.Server{{Name: "s1", Address: "10.0.0.1", Port: 8080}},
			BodyDigest:   "body-1",
			RecordDigest: "record-1",
			TextDigest:   "text-1",
		}},
		Maps: map[string]renderplan.Map{"maps/host.map": {
			Path:    "maps/host.map",
			Entries: []renderplan.Entry{{Key: "one.example.com", Value: "be"}},
		}},
		Files: []renderplan.File{
			{Path: "haproxy.cfg", Kind: renderplan.FileKindConfig, ReloadOnChange: true, Digest: "cfg-1"},
			{Path: "maps/host.map", Kind: renderplan.FileKindMap, Digest: "map-1"},
		},
	}
	if mutate != nil {
		mutate(plan)
	}
	for i := range plan.Sections {
		plan.Sections[i].Text = plan.Sections[i].TextDigest
		plan.Sections[i].TextKnown = true
	}
	for name := range plan.Backends {
		backend := plan.Backends[name]
		backend.Body = []string{backend.BodyDigest}
		backend.Comments = []string{backend.CommentsDigest}
		backend.ContentKnown = true
		plan.Backends[name] = backend
	}
	for i := range plan.Files {
		plan.Files[i].Content = plan.Files[i].Digest
		plan.Files[i].ContentKnown = true
	}
	plan.ComputeID()
	return plan
}

func TestDiffVerdicts(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*renderplan.Plan)
		agentOps    []string
		wantVerdict deployplan.Verdict
		wantOps     []string
		wantReason  string
	}{
		{
			name:        "an unchanged render writes the same files",
			wantVerdict: deployplan.VerdictFileOnly,
		},
		{
			name: "a map entry runs at runtime",
			mutate: func(p *renderplan.Plan) {
				p.Maps["maps/host.map"] = renderplan.Map{
					Path:    "maps/host.map",
					Entries: []renderplan.Entry{{Key: "one.example.com", Value: "other"}},
				}
				p.Files[1].Digest = "map-2"
			},
			wantVerdict: deployplan.VerdictRuntime,
			wantOps:     []string{api.OpMapSet},
		},
		{
			name: "a backend body reloads",
			mutate: func(p *renderplan.Plan) {
				backend := p.Backends["be"]
				backend.BodyDigest, backend.RecordDigest, backend.TextDigest = "body-2", "record-2", "text-2"
				p.Backends["be"] = backend
				p.Sections[1].TextDigest = "text-2"
				p.Files[0].Digest = "cfg-2"
			},
			wantVerdict: deployplan.VerdictReload,
			wantReason:  "backend be: body changed",
		},
		{
			name: "a core section reloads",
			mutate: func(p *renderplan.Plan) {
				p.Sections[0].TextDigest = "core-2"
				p.Files[0].Digest = "cfg-2"
			},
			wantVerdict: deployplan.VerdictReload,
			wantReason:  "core section core#0 changed",
		},
		{
			name: "an agent that does not execute the op reloads instead",
			mutate: func(p *renderplan.Plan) {
				p.Maps["maps/host.map"] = renderplan.Map{
					Path:    "maps/host.map",
					Entries: []renderplan.Entry{{Key: "one.example.com", Value: "other"}},
				}
				p.Files[1].Digest = "map-2"
			},
			agentOps:    []string{api.OpServerAdd},
			wantVerdict: deployplan.VerdictReload,
			wantReason:  "the agent does not execute map_set",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore := setDiffFlags(t)
			defer restore()
			diffAgentOps = tt.agentOps

			from := &diffSide{plan: diffPlan(nil)}
			decision := deployplan.Diff(diffPlan(tt.mutate), baselineOf(from))

			assert.Equal(t, tt.wantVerdict, decision.Verdict)
			kinds := make([]string, 0, len(decision.Ops))
			for i := range decision.Ops {
				kinds = append(kinds, decision.Ops[i].Kind)
			}
			if tt.wantOps != nil {
				assert.Equal(t, tt.wantOps, kinds)
			}
			if tt.wantReason != "" {
				assert.Contains(t, decision.Reasons, tt.wantReason)
			}
		})
	}
}

func TestBaselineOfARenderedSide(t *testing.T) {
	restore := setDiffFlags(t)
	defer restore()

	plan := diffPlan(nil)
	baseline := baselineOf(&diffSide{plan: plan})

	assert.Same(t, plan, baseline.Applied)
	assert.Same(t, plan, baseline.Running, "a rendered baseline is a pod that reloaded it")
	assert.Same(t, plan, baseline.WorkerOps)
	assert.Equal(t, []string{"maps/host.map"}, baseline.Inventory.Maps,
		"without a pod, what the worker loaded is what the plan declares")
	assert.False(t, baseline.ReloadPending)
	assert.True(t, baseline.Caps.DynamicBackends, "the default judges the change against "+defaultDiffHAProxyVersion)
}

func TestBaselineOfAPodSide(t *testing.T) {
	restore := setDiffFlags(t)
	defer restore()

	plan := diffPlan(nil)
	state := &api.State{
		AppliedPlanID:   plan.ID,
		RunningPlanID:   "an-older-plan",
		WorkerOpsPlanID: plan.ID,
		HAProxy:         api.HAProxyInfo{Version: "3.0.11"},
		Inventory:       api.Inventory{Maps: []string{"maps/other.map"}},
		ReloadPendingAt: "2026-08-18T12:00:00Z",
		PendingDeletes:  api.PendingDeletes{Servers: []string{"be/s2"}, Backends: []string{"be_old"}},
	}
	baseline := baselineOf(&diffSide{plan: plan, state: state})

	assert.Same(t, plan, baseline.Applied)
	assert.Nil(t, baseline.Running, "the worker runs a plan this diff does not have")
	assert.Same(t, plan, baseline.WorkerOps)
	assert.Equal(t, []string{"maps/other.map"}, baseline.Inventory.Maps,
		"a live pod's inventory is an observation, never the plan's file list")
	assert.True(t, baseline.ReloadPending)
	assert.Equal(t, 1, baseline.PendingServerDeletes)
	assert.Equal(t, 1, baseline.PendingBackendDeletes)
	assert.False(t, baseline.Caps.DynamicBackends, "3.0 has no add backend")
}

func TestAppliedPlanOfRejectsAMismatchedBlob(t *testing.T) {
	tests := []struct {
		name    string
		state   *api.State
		wantErr string
	}{
		{
			name:    "no blob at all",
			state:   &api.State{AppliedPlanID: "p1"},
			wantErr: "reports no plan blob",
		},
		{
			name:    "not a blob",
			state:   &api.State{AppliedPlanID: "p1", AppliedPlan: []byte("not zstd")},
			wantErr: "decoding the pod's plan blob",
		},
		{
			name:    "a blob for another plan",
			state:   &api.State{AppliedPlanID: "another-plan", AppliedPlan: encodedPlan(t, diffPlan(nil))},
			wantErr: "but it reports another-plan applied",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := appliedPlanOf(tt.state)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestPrintDiffHuman(t *testing.T) {
	restore := setDiffFlags(t)
	defer restore()

	ops := make([]api.Op, 0, 25)
	for i := range 25 {
		ops = append(ops, api.Op{Kind: api.OpMapSet, Path: "/tmp/render-1/maps/host.map", Key: "host-" + string(rune('a'+i))})
	}
	ops = append(ops, api.Op{Kind: api.OpServerAdd, Backend: "be", Server: "s1", Address: "10.0.0.1", Port: 8080})

	var out bytes.Buffer
	require.NoError(t, printDiff(&out, &diffReport{
		decision: &deployplan.Decision{
			Verdict: deployplan.VerdictRuntime,
			Ops:     ops,
			Reasons: []string{"file /tmp/render-1/files/503.http was removed, which no runtime op undoes"},
		},
		renderRoot: "/tmp/render-1",
	}))

	lines := strings.Split(out.String(), "\n")
	assert.Equal(t, "runtime", lines[0], "the verdict is the first line, so one word answers the question")
	assert.Contains(t, out.String(), "ops: 26 — map_set 25, server_add 1")
	assert.Contains(t, out.String(), "  map_set maps/host.map host-a",
		"the render's temporary directory is not a path the pod knows")
	assert.Contains(t, out.String(), "file files/503.http was removed")
	assert.Contains(t, out.String(), "… 6 more, pass --all to list every op")
	assert.NotContains(t, out.String(), "server_add be/s1 10.0.0.1:8080", "the 26th op is past the cap")

	diffAll = true
	out.Reset()
	require.NoError(t, printDiff(&out, &diffReport{
		decision: &deployplan.Decision{Verdict: deployplan.VerdictRuntime, Ops: ops},
	}))
	assert.Contains(t, out.String(), "server_add be/s1 10.0.0.1:8080")
	assert.NotContains(t, out.String(), "more, pass --all")
}

func TestPrintDiffSaysWhenNothingChanges(t *testing.T) {
	restore := setDiffFlags(t)
	defer restore()

	var out bytes.Buffer
	require.NoError(t, printDiff(&out, &diffReport{
		decision:  &deployplan.Decision{Verdict: deployplan.VerdictFileOnly, Mode: api.ModeAuto},
		unchanged: true,
	}))
	assert.Contains(t, out.String(), "both sides declare the same plan")
	assert.Contains(t, out.String(), "ops: none")
}

func TestPrintDiffJSON(t *testing.T) {
	restore := setDiffFlags(t)
	defer restore()
	diffOutputFormat = "json"

	var out bytes.Buffer
	require.NoError(t, printDiff(&out, &diffReport{decision: &deployplan.Decision{
		Verdict: deployplan.VerdictReload,
		Mode:    api.ModeReload,
		Reasons: []string{"core section core#0 changed"},
	}}))

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &decoded))
	assert.Equal(t, "reload", decoded["verdict"])
	assert.Equal(t, "reload", decoded["mode"])
	assert.Equal(t, []any{"core section core#0 changed"}, decoded["reasons"])
}

func TestPrintDiffRejectsAnUnknownFormat(t *testing.T) {
	restore := setDiffFlags(t)
	defer restore()
	diffOutputFormat = "yaml"

	err := printDiff(&bytes.Buffer{}, &diffReport{decision: &deployplan.Decision{Verdict: deployplan.VerdictReload}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "use human or json")
}

// TestDiffRendersTwoConfigs is the whole command minus the printing: two
// configuration files in, one verdict out. Both renders have to resolve their
// map and certificate paths against the same directories — those paths are
// inside haproxy.cfg, so a per-render directory would report every section as
// rewritten and every change as a reload.
func TestDiffRendersTwoConfigs(t *testing.T) {
	tests := []struct {
		name        string
		to          string
		wantVerdict deployplan.Verdict
		wantOps     []string
		wantReason  string
	}{
		{
			name:        "the same configuration twice changes nothing",
			to:          "base.yaml",
			wantVerdict: deployplan.VerdictFileOnly,
		},
		{
			name:        "a map entry runs at runtime",
			to:          "map-entry-changed.yaml",
			wantVerdict: deployplan.VerdictRuntime,
			wantOps:     []string{api.OpMapAdd, api.OpMapDel},
		},
		{
			name:        "a backend body reloads",
			to:          "backend-body-changed.yaml",
			wantVerdict: deployplan.VerdictReload,
			wantReason:  "backend be_two: body changed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restoreHAProxy := dataplanetest.InstallFakeHAProxy()
			t.Cleanup(restoreHAProxy)
			restore := setDiffFlags(t)
			defer restore()

			ctx := context.Background()
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
			env := &diffEnv{}
			t.Cleanup(env.close)

			// The target first, as the command does: it owns the directories.
			to, err := resolveDiffSide(ctx, &diffSource{files: []string{diffTestdata(tt.to)}}, env, logger)
			require.NoError(t, err)
			from, err := resolveDiffSide(ctx, &diffSource{files: []string{diffTestdata("base.yaml")}}, env, logger)
			require.NoError(t, err)

			decision := deployplan.Diff(to.plan, baselineOf(from))
			assert.Equal(t, tt.wantVerdict, decision.Verdict, "reasons: %v", decision.Reasons)
			if tt.wantOps != nil {
				kinds := make([]string, 0, len(decision.Ops))
				for i := range decision.Ops {
					kinds = append(kinds, decision.Ops[i].Kind)
				}
				assert.Equal(t, tt.wantOps, kinds)
			}
			if tt.wantReason != "" {
				assert.Contains(t, decision.Reasons, tt.wantReason)
			}
		})
	}
}

func diffTestdata(name string) string {
	return filepath.Join("testdata", "diff", name)
}

func encodedPlan(t *testing.T, plan *renderplan.Plan) []byte {
	t.Helper()
	blob, err := planblob.Encode(plan)
	require.NoError(t, err)
	return blob
}

// setDiffFlags puts the command's package-level flags back to their defaults
// for one test and restores whatever they held.
func setDiffFlags(t *testing.T) func() {
	t.Helper()
	previous := struct {
		output   string
		all      bool
		version  string
		agentOps []string
		test     string
		schema   string
	}{diffOutputFormat, diffAll, diffHAProxyVersion, diffAgentOps, diffTestName, diffSchemaDir}

	diffOutputFormat, diffAll, diffHAProxyVersion, diffAgentOps, diffTestName, diffSchemaDir =
		"human", false, "", nil, "", ""

	return func() {
		diffOutputFormat, diffAll, diffHAProxyVersion, diffAgentOps, diffTestName, diffSchemaDir =
			previous.output, previous.all, previous.version, previous.agentOps, previous.test, previous.schema
	}
}
