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

package planblob_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/planblob"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func planFixture() *renderplan.Plan {
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections:      []renderplan.Section{{Kind: renderplan.SectionKindCore, Name: "core#0", TextDigest: "core-1"}},
		Backends: map[string]renderplan.Backend{"be": {
			Name:    "be",
			Shape:   renderplan.ShapeDynamic,
			Servers: []renderplan.Server{{Name: "s1", Address: "10.0.0.1", Port: 8080}},
		}},
		Maps: map[string]renderplan.Map{"maps/host.map": {
			Path:    "maps/host.map",
			Entries: []renderplan.Entry{{Key: "one.example.com", Value: "be"}},
		}},
		Files: []renderplan.File{{Path: "haproxy.cfg", Kind: renderplan.FileKindConfig, Digest: "cfg-1"}},
	}
	plan.ComputeID()
	return plan
}

func TestRoundTrip(t *testing.T) {
	plan := planFixture()

	blob, err := planblob.Encode(plan)
	require.NoError(t, err)
	assert.NotEmpty(t, blob)

	decoded, err := planblob.Decode(blob)
	require.NoError(t, err)
	assert.Equal(t, plan, decoded, "a pod hands back what the controller sent, down to the plan id")
}

func TestEncodeSnapshotMatchesEncode(t *testing.T) {
	config := "global\n"
	plan := planFixture()
	plan.Sections[0].Text, plan.Sections[0].TextKnown = config, true
	plan.Sections[0].TextDigest, plan.Sections[0].Length = renderplan.DigestString(config), len(config)
	backend := plan.Backends["be"]
	backend.ContentKnown = true
	plan.Backends["be"] = backend
	plan.Files[0].Content, plan.Files[0].ContentKnown = config, true
	plan.Files[0].Digest, plan.Files[0].Size = renderplan.DigestString(config), int64(len(config))
	plan.ComputeID()
	snapshot, err := renderplan.NewSnapshot(renderplan.NewAuthority(), plan, nil)
	require.NoError(t, err)

	fromSnapshot, err := planblob.EncodeSnapshot(snapshot)
	require.NoError(t, err)
	fromPlan, err := planblob.Encode(plan)
	require.NoError(t, err)
	assert.Equal(t, fromPlan, fromSnapshot, "the snapshot streams the bytes the plan encodes to")

	decoded, err := planblob.Decode(fromSnapshot)
	require.NoError(t, err)
	assert.Equal(t, plan.ID, decoded.ID)

	_, err = planblob.EncodeSnapshot(nil)
	require.Error(t, err)
}

func TestEncodeRefusesNoPlan(t *testing.T) {
	_, err := planblob.Encode(nil)
	require.Error(t, err)
}

func TestDecodeRejectsGarbage(t *testing.T) {
	_, err := planblob.Decode([]byte("not a zstd frame"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decompressing plan blob")
}

// fleetPlan is the fleet size the plan budgets for: 3,000 backends with one
// server each and 25 maps of 100 lines, with exact content so a snapshot can
// be sealed from it.
func fleetPlan() *renderplan.Plan {
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Backends:      map[string]renderplan.Backend{},
		Profiles:      map[string]renderplan.Profile{},
		Maps:          map[string]renderplan.Map{},
	}
	config := "global\n"
	for i := range 3000 {
		name := fmt.Sprintf("be-%04d", i)
		text := fmt.Sprintf("backend %s\n    server SRV_1 10.0.0.1:8080\n", name)
		config += text
		plan.Sections = append(plan.Sections, renderplan.Section{
			Kind: renderplan.SectionKindBackend, Name: name, TextDigest: renderplan.DigestString(text),
			Length: len(text), Text: text, TextKnown: true,
		})
		plan.Backends[name] = renderplan.Backend{
			Name: name, Shape: "dynamic", GUID: "guid-" + name, Balance: "roundrobin",
			Servers:    []renderplan.Server{{Name: "SRV_1", Address: "10.0.0.1", Port: 8080, GUID: "srv-" + name}},
			BodyDigest: "0123456789abcdef", CommentsDigest: "0123456789abcdef",
			RecordDigest: "0123456789abcdef", TextDigest: renderplan.DigestString(text),
			Body: []string{"server SRV_1 10.0.0.1:8080"}, ContentKnown: true,
		}
	}
	plan.Files = []renderplan.File{{
		Path: renderplan.ConfigFilePath, Kind: renderplan.FileKindConfig, ReloadOnChange: true,
		Digest: renderplan.DigestString(config), Size: int64(len(config)), Content: config, ContentKnown: true,
	}}
	for i := range 25 {
		entries := make([]renderplan.Entry, 0, 100)
		for j := range 100 {
			entries = append(entries, renderplan.Entry{
				Key: fmt.Sprintf("host-%d-%d.example.com", i, j), Value: fmt.Sprintf("be-%04d", j),
			})
		}
		path := fmt.Sprintf("maps/route-%02d.map", i)
		plan.Maps[path] = renderplan.Map{Path: path, Ordered: true, Entries: entries}
	}
	plan.ComputeID()
	return plan
}

// BenchmarkEncodeFleetPlan prices the blob encoded from a plan.
func BenchmarkEncodeFleetPlan(b *testing.B) {
	plan := fleetPlan()
	blob, err := planblob.Encode(plan)
	require.NoError(b, err)
	b.ReportMetric(float64(len(blob)), "blob-B")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := planblob.Encode(plan); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEncodeFleetPlanSnapshot prices the same blob streamed from a
// sealed snapshot, the way the deployer encodes it.
func BenchmarkEncodeFleetPlanSnapshot(b *testing.B) {
	plan := fleetPlan()
	snapshot, err := renderplan.NewSnapshot(renderplan.NewAuthority(), plan, nil)
	require.NoError(b, err)
	fromPlan, err := planblob.Encode(plan)
	require.NoError(b, err)
	fromSnapshot, err := planblob.EncodeSnapshot(snapshot)
	require.NoError(b, err)
	require.Equal(b, fromPlan, fromSnapshot)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := planblob.EncodeSnapshot(snapshot); err != nil {
			b.Fatal(err)
		}
	}
}
