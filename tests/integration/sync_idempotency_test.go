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
	"testing"

	"github.com/rekby/fixenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
)

// TestConfigSyncIdempotency applies the same render twice and asserts the
// second apply is a noop: no reload, no write, and the configuration on disk
// still byte-identical to what the renderer produced.
//
// The configurations carry inline comments, which is where the Data Plane API
// lost idempotency: it re-encoded comments as model metadata, so the next
// reconciliation saw a difference that was not there. The agent stores the
// renderer's bytes, so the comparison is on the bytes themselves.
func TestConfigSyncIdempotency(t *testing.T) {
	testCases := []struct {
		name       string
		configFile string
	}{
		{
			name:       "server-with-comment-idempotent",
			configFile: "idempotency/server-with-comment.cfg",
		},
		{
			name:       "acl-with-comment-idempotent",
			configFile: "idempotency/acl-with-comment.cfg",
		},
		{
			name:       "http-response-rule-idempotent",
			configFile: "idempotency/http-response-rule.cfg",
		},
	}

	for _, tt := range testCases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runIdempotencyTest(t, tt.configFile)
		})
	}
}

func runIdempotencyTest(t *testing.T, configFile string) {
	env := fixenv.New(t)
	ctx := context.Background()
	session := NewSession(t, env)

	config := LoadTestConfig(t, configFile)
	session.SetConfig(config)
	require.Equal(t, deployplan.VerdictReload, session.MustApply(ctx).Verdict,
		"a pod with no baseline gets full state and a reload")

	onDisk, err := session.haproxy.ReadFile(ctx, ConfigPath)
	require.NoError(t, err, "reading the applied configuration")
	require.Equal(t, config, onDisk, "the pod must hold the renderer's exact bytes")

	before, err := session.haproxy.WorkerPID(ctx)
	require.NoError(t, err)

	decision, result := session.ApplyDesired(ctx)
	require.True(t, result.OK, "the repeated apply was rejected: %s", applyError(result))
	assert.Equal(t, deployplan.VerdictFileOnly, decision.Verdict, "an unchanged render must not reload")
	assert.Empty(t, decision.Ops, "an unchanged render must compose no runtime commands")
	assert.Equal(t, api.ResultNoop, result.Mode, "an unchanged render must write nothing")

	after, err := session.haproxy.WorkerPID(ctx)
	require.NoError(t, err)
	assert.Equal(t, before, after, "an unchanged render must not replace the worker")
}
