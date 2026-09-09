// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package deployer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/agenttest"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// renderBackendRun builds a render of one profile and eight backend
// sections; addresses maps a backend index to the server address it gets,
// every other backend serving 10.0.0.<index>.
func renderBackendRun(addresses map[int]string) (*renderplan.Plan, string, *dataplane.AuxiliaryFiles) {
	return renderBackendRunWithMap(addresses, mapEntry)
}

func renderBackendRunWithMap(addresses map[int]string, mapContent string) (*renderplan.Plan, string, *dataplane.AuxiliaryFiles) {
	const count = 8
	profileText := "defaults\n  mode http\n  timeout connect 5s\n  timeout client 30s\n  timeout server 30s\n"
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections: []renderplan.Section{{
			Kind: renderplan.SectionKindProfile, Name: "http", Text: profileText, TextKnown: true,
			TextDigest: renderplan.DigestString(profileText), Length: len(profileText),
		}},
		Backends: map[string]renderplan.Backend{},
		Profiles: map[string]renderplan.Profile{"http": {
			Name: "http", BodyDigest: renderplan.DigestString(strings.TrimPrefix(profileText, "defaults\n")),
		}},
		Maps: map[string]renderplan.Map{"maps/host.map": {
			Path: "maps/host.map", Entries: renderplan.ParseMapEntries(mapContent),
		}},
	}
	config := profileText
	for i := range count {
		name := fmt.Sprintf("be_%03d", i)
		address := fmt.Sprintf("10.0.0.%d", i)
		if custom, ok := addresses[i]; ok {
			address = custom
		}
		text := "backend " + name + "\n  server srv1 " + address + ":8080\n  # padding padding padding padding\n"
		backend := renderplan.Backend{
			Name: name, Profile: "http", Mode: "http", Shape: renderplan.ShapeDynamic,
			Servers:        []renderplan.Server{{Name: "srv1", Address: address, Port: 8080}},
			BodyDigest:     renderplan.DigestString("body"),
			CommentsDigest: renderplan.DigestString("comments"),
			TextDigest:     renderplan.DigestString(text),
			Body:           []string{"body"},
			Comments:       []string{"comments"},
			ContentKnown:   true,
		}
		backend.RecordDigest = testBackendRecordDigest(&backend)
		plan.Backends[name] = backend
		plan.Sections = append(plan.Sections, renderplan.Section{
			Kind: renderplan.SectionKindBackend, Name: name, Text: text, TextKnown: true,
			TextDigest: backend.TextDigest, Length: len(text),
		})
		config += text
	}
	plan.Files = []renderplan.File{
		{
			Path: "haproxy.cfg", Kind: renderplan.FileKindConfig, ReloadOnChange: true,
			Digest: renderplan.DigestString(config), Size: int64(len(config)), Content: config, ContentKnown: true,
		},
		{
			Path: "maps/host.map", Kind: renderplan.FileKindMap,
			Digest: renderplan.DigestString(mapContent), Size: int64(len(mapContent)), Content: mapContent, ContentKnown: true,
		},
	}
	aux := &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{{Path: "maps/host.map", Content: mapContent}},
	}
	plan.ComputeID()
	return plan, config, aux
}

func TestContentPatchSplicesTheRunBetweenTheSharedEnds(t *testing.T) {
	prev := strings.Repeat("line of a map file that does not change\n", 300)
	base := &contentProof{proof: "p", digest: renderplan.DigestString(prev), content: prev}

	middle := prev[:len(prev)/2] + "new.example.com be-new\n" + prev[len(prev)/2:]
	patch, ok := contentPatch(base, middle)
	require.True(t, ok)
	assert.Equal(t, base.digest, patch.patch.BaseDigest)
	assert.Equal(t, int64(len(prev)), patch.patch.BaseSize)
	assert.Equal(t, int64(len(prev)/2), patch.patch.Offset)
	assert.Zero(t, patch.patch.Length)
	assert.Equal(t, "new.example.com be-new\n", patch.data)

	removed := prev[:len(prev)/2] + prev[len(prev)/2+len("line of a map file that does not change\n"):]
	patch, ok = contentPatch(base, removed)
	require.True(t, ok, "a dropped line is a splice of nothing")
	assert.Empty(t, patch.data)
	assert.Equal(t, int64(len("line of a map file that does not change\n")), patch.patch.Length)
	spliced := prev[:patch.patch.Offset] + patch.data + prev[patch.patch.Offset+patch.patch.Length:]
	assert.Equal(t, removed, spliced)

	tail := prev + "appended\n"
	patch, ok = contentPatch(base, tail)
	require.True(t, ok)
	assert.Equal(t, int64(len(prev)), patch.patch.Offset)
	assert.Equal(t, "appended\n", patch.data)

	rewritten := strings.ToUpper(prev[:len(prev)*2/3]) + prev[len(prev)*2/3:]
	_, ok = contentPatch(base, rewritten)
	assert.False(t, ok, "a splice of most of the file is sent whole")

	_, ok = contentPatch(base, prev)
	assert.True(t, ok, "an unchanged file is an empty splice, which the proofs keep from being sent at all")
}

func TestCommonPrefixAndSuffixAcrossChunkBoundaries(t *testing.T) {
	for _, size := range []int{0, 1, compareChunk - 1, compareChunk, compareChunk + 1, 3*compareChunk + 7} {
		a := strings.Repeat("x", size)
		assert.Equal(t, size, commonPrefix(a, a), "prefix of %d", size)
		assert.Equal(t, size, commonSuffix(a, a), "suffix of %d", size)
		if size == 0 {
			continue
		}
		b := a[:size-1] + "y"
		assert.Equal(t, size-1, commonPrefix(a, b), "prefix with the last byte changed at %d", size)
		assert.Zero(t, commonSuffix(a, b))
		c := "y" + a[1:]
		assert.Zero(t, commonPrefix(a, c))
		assert.Equal(t, size-1, commonSuffix(a, c), "suffix with the first byte changed at %d", size)
	}
	assert.Equal(t, 3, commonPrefix("abcdef", "abcxyz"))
	assert.Equal(t, 3, commonSuffix("abcdef", "xyzdef"))
}

func TestPatchMemoComputesASpliceOncePerBase(t *testing.T) {
	memo := newPatchMemo()
	prev := strings.Repeat("entry\n", 100)
	base := &contentProof{proof: "p", digest: renderplan.DigestString(prev), content: prev}
	next := prev + "more\n"
	first, ok := memo.get("maps/host.map", base, next)
	require.True(t, ok)
	second, ok := memo.get("maps/host.map", base, next)
	require.True(t, ok)
	assert.Equal(t, first, second)
	assert.Len(t, memo.patches, 1)

	rewritten := strings.ToUpper(prev)
	_, ok = memo.get("maps/other.map", base, rewritten)
	assert.False(t, ok)
	_, ok = memo.get("maps/other.map", base, rewritten)
	assert.False(t, ok)
	assert.Len(t, memo.missing, 1)
}

// TestApply_ConfigChangeIsSentAsAPatch pins the upload: after the first apply
// the changed backend section is what crosses the wire, the agent holds the
// spliced file, and an agent without the feature or without the base gets
// the whole file.
func TestApply_ConfigChangeIsSentAsAPatch(t *testing.T) {
	agent := agenttest.New(t)
	bus := newTestBus(t)
	component := createTestDeployer(bus.EventBus)
	endpoint := agentEndpoint(agent, "haproxy-0")

	plan1, config1, aux1 := renderBackendRun(nil)
	deployTo(t, component, bus, plan1, config1, aux1, "config_validation", endpoint)
	plan2, config2, aux2 := renderBackendRun(map[int]string{3: "10.9.9.9"})
	completed := deployTo(t, component, bus, plan2, config2, aux2, "config_validation", endpoint)

	applies := agent.Applies()
	require.Len(t, applies, 2)
	require.Nil(t, applies[0].Manifest.Files[0].Patch)
	assert.Len(t, applies[0].Parts["haproxy.cfg"], len(config1))
	second := applies[1]
	require.NotNil(t, second.Manifest.Files[0].Patch, "the second apply carries a patch for the config")
	assert.Equal(t, "9.9.9", string(second.Parts["haproxy.cfg"]), "the bytes that differ from 10.0.0.3")
	assert.Equal(t, config2, string(agent.Content("haproxy.cfg")))
	assert.Equal(t, plan2.ID, agent.State().AppliedPlanID)
	require.NotNil(t, completed.Phases)
	assert.Equal(t, int64(len(second.Parts["haproxy.cfg"])), completed.Phases.UploadBytes)
	_, mapSent := second.Parts["maps/host.map"]
	assert.False(t, mapSent, "an unchanged map is held, not sent")

	// A map large enough for one added line to be a splice, not a rewrite.
	wideMap := ""
	for i := range 40 {
		wideMap += fmt.Sprintf("host%02d.example.com be_%03d\n", i, i%8)
	}
	plan2b, config2b, aux2b := renderBackendRunWithMap(map[int]string{3: "10.9.9.9"}, wideMap)
	deployTo(t, component, bus, plan2b, config2b, aux2b, "config_validation", endpoint)
	grownMap := wideMap + "new.example.com be_003\n"
	plan3, config3, aux3 := renderBackendRunWithMap(map[int]string{3: "10.9.9.9"}, grownMap)
	deployTo(t, component, bus, plan3, config3, aux3, "config_validation", endpoint)
	applies = agent.Applies()
	require.Len(t, applies, 4)
	third := applies[3]
	_, configSent := third.Parts["haproxy.cfg"]
	assert.False(t, configSent, "the unchanged config is held")
	require.NotNil(t, third.Manifest.Files[1].Patch, "the map arrives as a patch")
	assert.Equal(t, "new.example.com be_003\n", string(third.Parts["maps/host.map"]))
	assert.Equal(t, grownMap, string(agent.Content("maps/host.map")))

	plan4, config4, aux4 := renderBackendRunWithMap(map[int]string{3: "10.9.9.9", 5: "10.8.8.8"}, grownMap)
	agent.MissingOnce("haproxy.cfg")
	deployTo(t, component, bus, plan4, config4, aux4, "config_validation", endpoint)
	applies = agent.Applies()
	require.Len(t, applies, 6)
	assert.NotNil(t, applies[4].Manifest.Files[0].Patch, "the first attempt patched")
	assert.Nil(t, applies[5].Manifest.Files[0].Patch, "the retry after a missing base sends the file whole")
	assert.Len(t, applies[5].Parts["haproxy.cfg"], len(config4))
	assert.Equal(t, config4, string(agent.Content("haproxy.cfg")))
}

func TestApply_AgentWithoutFilePatchesGetsTheWholeFile(t *testing.T) {
	agent := agenttest.New(t, agenttest.WithoutFilePatches())
	bus := newTestBus(t)
	component := createTestDeployer(bus.EventBus)
	endpoint := agentEndpoint(agent, "haproxy-0")

	plan1, config1, aux1 := renderBackendRun(nil)
	deployTo(t, component, bus, plan1, config1, aux1, "config_validation", endpoint)
	plan2, config2, aux2 := renderBackendRun(map[int]string{3: "10.9.9.9"})
	deployTo(t, component, bus, plan2, config2, aux2, "config_validation", endpoint)

	applies := agent.Applies()
	require.Len(t, applies, 2)
	assert.Nil(t, applies[1].Manifest.Files[0].Patch)
	assert.Len(t, applies[1].Parts["haproxy.cfg"], len(config2))
	assert.Equal(t, config2, string(agent.Content("haproxy.cfg")))
}
