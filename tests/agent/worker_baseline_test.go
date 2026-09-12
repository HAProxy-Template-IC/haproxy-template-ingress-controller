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

//go:build agentdocker

package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
)

func TestNewlyLoadedMapRejectsPreReloadFileOnlyApply(t *testing.T) {
	e := newEnvWithReloadInterval(t, 3*time.Second)
	s := newSession(e)
	line := "    http-request return status 200 content-type text/plain hdr x-note \"%[req.hdr(host),lower,map(" +
		noteMapPath + ",none)]\" string \"noted\" if { path " + notePath + " }\n"
	require.Contains(t, renderedConfig, line)
	s.set(configPath, strings.Replace(renderedConfig, line, "", 1))
	first := s.apply(s.next(api.ModeReload), s.allParts())
	require.True(t, first.OK, "%+v", first.Error)
	require.NotNil(t, first.Inventory)
	require.NotContains(t, first.Inventory.Maps, noteMapPath)

	s.set(configPath, renderedConfig)
	second := s.next(api.ModeReload)
	scheduled, _ := s.timedApply(second, s.allParts())
	require.True(t, scheduled.OK, "%+v", scheduled.Error)
	require.Equal(t, api.ResultScheduled, scheduled.Mode)
	s.absorb(scheduled)
	s.set(noteMapPath, strings.Replace(noteMapContent, "first value", "after-reload", 1))
	stale := s.next(api.ModeAuto)
	reloaded := s.awaitScheduled(second.PlanID)
	require.True(t, reloaded.OK, "%+v", reloaded.Error)
	require.NotNil(t, reloaded.Inventory)
	require.Contains(t, reloaded.Inventory.Maps, noteMapPath)
	require.Equal(t, scheduled.AppliedPlanProof, reloaded.AppliedPlanProof)
	require.NotEqual(t, scheduled.WorkerOpsPlanProof, reloaded.WorkerOpsPlanProof)

	err := s.applyExpectingRefusal(t, stale, s.allParts())
	var conflict *client.ConflictError
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, "worker_ops_mismatch", conflict.Conflict.Reason)
	require.Equal(t, noteMapContent, e.read(noteMapPath))
	_, headers, _ := e.requestWithHost("a.example.com", notePath)
	require.Equal(t, "first value", headers.Get("x-note"))

	s.absorb(reloaded)
	retry := s.next(api.ModeAuto)
	retry.Ops = []api.Op{{Kind: api.OpMapSet, Path: noteMapPath, Key: "a.example.com", Value: "after-reload"}}
	result := s.apply(retry, s.allParts())
	require.True(t, result.OK, "%+v", result.Error)
	require.Equal(t, api.ResultRuntime, result.Mode)
	require.True(t, reloaded.HAProxy.SameWorker(result.HAProxy))
	require.Equal(t, "after-reload", mapEntries(e.worker("show map " + noteMapPath))["a.example.com"])
	require.Equal(t, s.files[noteMapPath], e.read(noteMapPath))
	_, headers, _ = e.requestWithHost("a.example.com", notePath)
	require.Equal(t, "after-reload", headers.Get("x-note"))
}
