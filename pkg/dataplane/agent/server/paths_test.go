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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

// TestOneBaseRelativePathEverywhere pins the convention the whole system rests
// on: HAProxy names maps and certificates by the literal string in the config,
// which is base-relative, so the manifest path, the op path, the runtime ident
// and the inventory entry are all the same string. Only disk I/O prefixes
// --base-dir.
func TestOneBaseRelativePathEverywhere(t *testing.T) {
	h := newHarness(t)
	pem := "-----BEGIN PRIVATE KEY-----\nx\n-----BEGIN CERTIFICATE-----\ny\n"
	files := []file{
		{Path: configPath, Content: "global\n", Reload: true},
		{Path: "maps/host.map", Content: "example.com be-a\n"},
		{Path: "ssl/tls.pem", Content: pem, Kind: api.FileKindCert},
	}
	m := buildManifest("plan-1", files)
	m.Mode = api.ModeReload
	first := h.apply(&m, files)
	require.True(t, first.OK, "%+v", first.Error)

	next := buildManifest("plan-2", files)
	next.ExpectedPrevPlanID = first.AppliedPlanID
	next.ExpectedPrevToken = first.AppliedToken
	next.Ops = []api.Op{
		{Kind: api.OpMapAdd, Path: "maps/host.map", Key: "b.example.com", Value: "be-b"},
		{Kind: api.OpCertNew, Path: "ssl/tls.pem"},
	}
	result := h.apply(&next, files)
	require.True(t, result.OK, "%+v", result.Error)

	for _, command := range h.model.Sent() {
		assert.False(t, strings.Contains(command, h.baseDir),
			"a runtime command must carry the base-relative path, not the mount point: %q", command)
	}
	assert.Contains(t, h.model.Sent(), "add map maps/host.map")
	assert.Contains(t, h.model.Sent(), "new ssl cert ssl/tls.pem")

	// The inventory refreshes on a reload, which is when the running worker's
	// idea of what it has loaded can change.
	refresh := buildManifest("plan-3", files)
	refresh.Mode = api.ModeReload
	refresh.ExpectedPrevPlanID = result.AppliedPlanID
	refresh.ExpectedPrevToken = result.AppliedToken
	require.True(t, h.apply(&refresh, files).OK)

	state := h.state(false)
	require.Contains(t, state.Files, "maps/host.map")
	require.Contains(t, state.Files, "ssl/tls.pem")
	assert.Equal(t, []string{"maps/host.map"}, state.Inventory.Maps)
	assert.Equal(t, []string{"ssl/tls.pem"}, state.Inventory.Certs)
}
