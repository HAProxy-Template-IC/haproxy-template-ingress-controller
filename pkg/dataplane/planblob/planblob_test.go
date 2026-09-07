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

func TestEncodeRefusesNoPlan(t *testing.T) {
	_, err := planblob.Encode(nil)
	require.Error(t, err)
}

func TestDecodeRejectsGarbage(t *testing.T) {
	_, err := planblob.Decode([]byte("not a zstd frame"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decompressing plan blob")
}

// BenchmarkEncodeFleetPlan prices the blob for the fleet size the plan
// budgets for: 3,000 backends with one server each and 25 maps of 100 lines.
func BenchmarkEncodeFleetPlan(b *testing.B) {
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Backends:      map[string]renderplan.Backend{},
		Maps:          map[string]renderplan.Map{},
	}
	for i := range 3000 {
		name := fmt.Sprintf("be-%04d", i)
		plan.Sections = append(plan.Sections, renderplan.Section{
			Kind: renderplan.SectionKindBackend, Name: name, TextDigest: "0123456789abcdef", Length: 200,
		})
		plan.Backends[name] = renderplan.Backend{
			Name: name, Shape: "dynamic", GUID: "guid-" + name, Balance: "roundrobin",
			Servers:    []renderplan.Server{{Name: "SRV_1", Address: "10.0.0.1", Port: 8080, GUID: "srv-" + name}},
			BodyDigest: "0123456789abcdef", CommentsDigest: "0123456789abcdef",
			RecordDigest: "0123456789abcdef", TextDigest: "0123456789abcdef",
		}
	}
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
