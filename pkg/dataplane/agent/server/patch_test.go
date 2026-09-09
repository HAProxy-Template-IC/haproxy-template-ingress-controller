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

package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/haproxytest"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

const (
	patchBase    = "global\nbackend a\n  server s1 10.0.0.1:80\nbackend b\n  server s1 10.0.0.2:80\nbackend c\n  server s1 10.0.0.3:80\n"
	patchMiddle  = "backend b\n  server s1 10.0.0.2:80\n"
	patchReplace = "backend b\n  server s1 10.9.9.9:80\n  server s2 10.9.9.8:80\n"
)

// patchedConfig is a manifest for plan-2 whose config arrives as a splice of
// patchReplace into patchBase at the middle backend; the returned file list
// carries the replacement as the config's part.
func patchedConfig(t *testing.T, previous *api.ApplyResult, patch *api.FilePatch) (api.Manifest, []file) {
	t.Helper()
	base, replacement := patchBase, patchReplace
	offset := strings.Index(base, patchMiddle)
	require.NotEqual(t, -1, offset)
	next := base[:offset] + replacement + base[offset+len(patchMiddle):]
	files := baseFiles(next)
	m := buildManifest("plan-2", files)
	m.Mode = api.ModeReload
	m.ExpectedPrevPlanID = previous.AppliedPlanID
	m.ExpectedPrevToken = previous.AppliedToken
	if patch == nil {
		patch = &api.FilePatch{
			BaseDigest: renderplan.DigestString(base), BaseSize: int64(len(base)),
			Offset: int64(offset), Length: int64(len(patchMiddle)),
		}
	}
	m.Files[0].Patch = patch
	parts := []file{{Path: configPath, Content: replacement, Reload: true}, files[1]}
	return m, parts
}

func applyPatchBase(t *testing.T, h *harness) api.ApplyResult {
	t.Helper()
	files := baseFiles(patchBase)
	m := buildManifest("plan-1", files)
	m.Mode = api.ModeReload
	result := h.apply(&m, files)
	require.True(t, result.OK, "%+v", result.Error)
	return result
}

func TestPatchedConfigIsSplicedIntoTheHeldFile(t *testing.T) {
	h := newHarness(t)
	assert.Contains(t, h.state(false).Features, api.FeatureFilePatch)
	first := applyPatchBase(t, h)

	m, parts := patchedConfig(t, &first, nil)
	status, raw := h.post(&m, parts, "maps/host.map")
	require.Equal(t, http.StatusOK, status, string(raw))
	result := api.ApplyResult{}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.True(t, result.OK, "%+v", result.Error)
	assert.Equal(t, "plan-2", result.AppliedPlanID)
	want := strings.Replace(patchBase, patchMiddle, patchReplace, 1)
	assert.Equal(t, want, h.read(configPath))
	assert.Equal(t, renderplan.DigestString(want), h.state(true).Files[configPath].Digest)
}

func TestPatchWhoseBaseIsNotHeldAsksForTheWholeFile(t *testing.T) {
	h := newHarness(t)
	first := applyPatchBase(t, h)

	other := strings.Replace(patchBase, "10.0.0.1", "10.0.0.7", 1)
	m, parts := patchedConfig(t, &first, &api.FilePatch{
		BaseDigest: renderplan.DigestString(other), BaseSize: int64(len(other)),
		Offset: int64(strings.Index(patchBase, patchMiddle)), Length: int64(len(patchMiddle)),
	})
	status, raw := h.post(&m, parts, "maps/host.map")
	require.Equal(t, http.StatusConflict, status, string(raw))
	missing := api.Missing{}
	require.NoError(t, json.Unmarshal(raw, &missing))
	assert.Equal(t, []string{configPath}, missing.Missing)
	assert.Equal(t, patchBase, h.read(configPath), "nothing landed")
	assert.Equal(t, "plan-1", h.state(false).AppliedPlanID)
}

func TestPatchThatDoesNotFitItsBaseIsRefused(t *testing.T) {
	h := newHarness(t)
	first := applyPatchBase(t, h)

	m, parts := patchedConfig(t, &first, &api.FilePatch{
		BaseDigest: renderplan.DigestString(patchBase), BaseSize: int64(len(patchBase)),
		Offset: int64(len(patchBase)) - 3, Length: int64(len(patchMiddle)),
	})
	status, raw := h.post(&m, parts, "maps/host.map")
	require.Equal(t, http.StatusBadRequest, status, string(raw))
	assert.Equal(t, patchBase, h.read(configPath), "nothing landed")

	// The right offsets with the wrong bytes: the spliced file fails the
	// manifest digest exactly as a whole part would.
	m, parts = patchedConfig(t, &first, nil)
	parts[0].Content = strings.Replace(patchReplace, "10.9.9.9", "10.9.9.1", 1)
	status, raw = h.post(&m, parts, "maps/host.map")
	require.Equal(t, http.StatusBadRequest, status, string(raw))
	assert.Equal(t, patchBase, h.read(configPath), "nothing landed")
	assert.Equal(t, "plan-1", h.state(false).AppliedPlanID)
}

func TestARefusedSetIsKnownBadHoweverItArrives(t *testing.T) {
	h := newHarness(t)
	first := applyPatchBase(t, h)
	h.model.With(func(m *haproxytest.Model) { m.ReloadFails = true })

	offset := strings.Index(patchBase, patchMiddle)
	next := baseFiles(patchBase[:offset] + patchReplace + patchBase[offset+len(patchMiddle):])
	whole := buildManifest("plan-2", next)
	whole.Mode = api.ModeReload
	whole.ExpectedPrevPlanID = first.AppliedPlanID
	whole.ExpectedPrevToken = first.AppliedToken
	require.False(t, h.apply(&whole, next).OK)
	refusals := h.metric("haptic_agent_reloads_total", "failed")

	m, parts := patchedConfig(t, &first, nil)
	m.ExpectedPrevPlanID, m.ExpectedPrevToken = "", api.Token{}
	result := h.apply(&m, parts)
	assert.False(t, result.OK)
	assert.Equal(t, refusals, h.metric("haptic_agent_reloads_total", "failed"),
		"the same set as a patch must not reach HAProxy again")
	assert.Equal(t, patchBase, h.read(configPath))
}
