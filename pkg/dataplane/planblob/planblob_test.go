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
