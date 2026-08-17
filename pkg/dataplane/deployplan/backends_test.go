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

package deployplan_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// TestDiffBackendAdded covers rule 2.
func TestDiffBackendAdded(t *testing.T) {
	sslServer := srv("SRV_1", "10.0.0.1", 8443)
	sslServer.Extra = []renderplan.KeywordArg{{Name: "ssl"}, {Name: "ca-file", Args: []string{"ca/bundle.pem"}}}

	unknownKeyword := srv("SRV_1", "10.0.0.1", 8080)
	unknownKeyword.Extra = []renderplan.KeywordArg{{Name: "resolvers", Args: []string{"dns"}}}

	noProfile := dynBackend("be-new", srv("SRV_1", "10.0.0.1", 8080))
	noProfile.Profile = "not-rendered-yet"

	staticLB := dynBackend("be-new", srv("SRV_1", "10.0.0.1", 8080))
	staticLB.Balance = "static-rr"

	tests := []struct {
		name      string
		added     *renderplan.Backend
		baseline  func(*renderplan.Plan) *deployplan.Baseline
		inventory api.Inventory
		verdict   deployplan.Verdict
		reason    string
	}{
		{
			name:    "dynamic backend on 3.4",
			added:   dynBackend("be-new", srv("SRV_1", "10.0.0.1", 8080)),
			verdict: deployplan.VerdictRuntime,
		},
		{
			name:     "no add backend below 3.4",
			added:    dynBackend("be-new", srv("SRV_1", "10.0.0.1", 8080)),
			baseline: on33,
			verdict:  deployplan.VerdictReload,
			reason:   "be-new added: this HAProxy has no add backend",
		},
		{
			name:    "structural shape",
			added:   structuralBackend("be-new", srv("SRV_1", "10.0.0.1", 8080)),
			verdict: deployplan.VerdictReload,
			reason:  "be-new added: structural shape, stick-table in the body",
		},
		{
			name:    "profile is not in the running config",
			added:   noProfile,
			verdict: deployplan.VerdictReload,
			reason:  `be-new added: profile "not-rendered-yet" is not in the running config`,
		},
		{
			name:    "server keyword outside the add server set",
			added:   dynBackend("be-new", unknownKeyword),
			verdict: deployplan.VerdictReload,
			reason:  "keyword resolvers cannot be set on a dynamic server",
		},
		{
			name:    "ca-file the running worker has not loaded",
			added:   dynBackend("be-new", sslServer),
			verdict: deployplan.VerdictReload,
			reason:  "keyword ca-file cannot be set on a dynamic server",
		},
		{
			name:      "ca-file in the runtime inventory",
			added:     dynBackend("be-new", sslServer),
			inventory: api.Inventory{CAFiles: []string{"ca/bundle.pem"}},
			verdict:   deployplan.VerdictRuntime,
		},
		{
			name:    "balance that refuses dynamic servers",
			added:   staticLB,
			verdict: deployplan.VerdictReload,
			reason:  `balance "static-rr" takes no dynamic server`,
		},
	}

	prev := basePlan()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := on34
			if tt.baseline != nil {
				pod = tt.baseline
			}
			base := pod(prev)
			base.Inventory = tt.inventory

			got := deployplan.Diff(basePlan(withBackend(tt.added)), base)

			require.Equal(t, tt.verdict, got.Verdict, got.Reasons)
			if tt.reason != "" {
				reasonsContain(t, got.Reasons, tt.reason)
			}
		})
	}
}

func TestDiffBackendAddedOpSequence(t *testing.T) {
	prev := basePlan()
	added := dynBackend("be-new", srv("SRV_1", "10.0.0.1", 8080), srv("SRV_2", "10.0.0.2", 8080))
	added.GUID = "be-new-guid"

	got := deployplan.Diff(basePlan(withBackend(added)), on34(prev))

	require.Equal(t, deployplan.VerdictRuntime, got.Verdict, got.Reasons)
	assert.Equal(t, []string{
		api.OpBackendAdd,
		api.OpServerAdd, api.OpServerEnable,
		api.OpServerAdd, api.OpServerEnable,
		api.OpBackendPublish,
	}, kinds(got.Ops))
	assert.Equal(t, api.Op{
		Kind: api.OpBackendAdd, Backend: "be-new", Profile: testProfile, Mode: "http", GUID: "be-new-guid",
	}, got.Ops[0])
	assert.Equal(t, 1, got.Chunks)
	assert.Equal(t, api.ModeAuto, got.Mode)
}

// TestDiffBackendRemoved covers rule 3.
func TestDiffBackendRemoved(t *testing.T) {
	prev := basePlan(withBackend(dynBackend("be-old", srv("SRV_1", "10.0.0.1", 8080))))

	got := deployplan.Diff(basePlan(), on34(prev))

	require.Equal(t, deployplan.VerdictRuntime, got.Verdict, got.Reasons)
	assert.Equal(t, []string{
		api.OpBackendUnpublish,
		api.OpServerDisable, api.OpServerWaitRemovable, api.OpServerDel,
		api.OpBackendWaitRemovable, api.OpBackendDel,
	}, kinds(got.Ops))
	assert.Equal(t, 2000, got.Ops[2].TimeoutMs)
}

func TestDiffBackendRemovedRefusals(t *testing.T) {
	tests := []struct {
		name     string
		removed  *renderplan.Backend
		baseline func(*renderplan.Plan) *deployplan.Baseline
		pending  int
		reason   string
	}{
		{
			name:     "no del backend below 3.4",
			removed:  dynBackend("be-old", srv("SRV_1", "10.0.0.1", 8080)),
			baseline: on33,
			reason:   "be-old removed: this HAProxy has no del backend",
		},
		{
			name:     "structural backends are deleted by a reload",
			removed:  structuralBackend("be-old", srv("SRV_1", "10.0.0.1", 8080)),
			baseline: on34,
			reason:   "be-old removed: structural shape, stick-table in the body",
		},
		{
			name:     "too many backend deletes already pending",
			removed:  dynBackend("be-old", srv("SRV_1", "10.0.0.1", 8080)),
			baseline: on34,
			pending:  api.MaxPendingBackendDeletes,
			reason:   "be-old removed: 100 backend deletes already pending",
		},
		{
			name:     "too many server deletes already pending",
			removed:  dynBackend("be-old", srv("SRV_1", "10.0.0.1", 8080)),
			baseline: on34,
			pending:  -1,
			reason:   "1000 server deletes already pending",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := tt.baseline(basePlan(withBackend(tt.removed)))
			base.PendingBackendDeletes = max(tt.pending, 0)
			if tt.pending < 0 {
				base.PendingServerDeletes = api.MaxPendingServerDeletes
			}

			got := deployplan.Diff(basePlan(), base)

			require.Equal(t, deployplan.VerdictReload, got.Verdict)
			assert.Empty(t, got.Ops)
			reasonsContain(t, got.Reasons, tt.reason)
		})
	}
}

// TestDiffBackendAttributeChanges covers the attributes rule 4 sends to a
// reload because HAProxy cannot alter them at runtime.
func TestDiffBackendAttributeChanges(t *testing.T) {
	tests := []struct {
		name   string
		change func(*renderplan.Backend)
		reason string
	}{
		{"profile", func(be *renderplan.Backend) { be.Profile = "haptic-be-2" }, "be-a: profile changed"},
		{"mode", func(be *renderplan.Backend) { be.Mode = "tcp" }, "be-a: mode changed"},
		{"guid", func(be *renderplan.Backend) { be.GUID = "other" }, "be-a: guid changed"},
		{"balance", func(be *renderplan.Backend) { be.Balance = "leastconn" }, "be-a: balance changed"},
		{"hash type", func(be *renderplan.Backend) { be.HashType = "consistent" }, "be-a: hash-type changed"},
		{"shape", func(be *renderplan.Backend) { be.Shape = renderplan.ShapeStructural }, "be-a: shape changed"},
		{
			"default server",
			func(be *renderplan.Backend) {
				be.DefaultServer = []renderplan.KeywordArg{{Name: "maxconn", Args: []string{"100"}}}
			},
			"be-a: default-server changed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prev := basePlan(withProfile("haptic-be-2", "body-2"), withBackend(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080))))
			changed := dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080))
			tt.change(changed)

			got := deployplan.Diff(basePlan(withProfile("haptic-be-2", "body-2"), withBackend(changed)), on34(prev))

			require.Equal(t, deployplan.VerdictReload, got.Verdict)
			assert.Empty(t, got.Ops)
			reasonsContain(t, got.Reasons, tt.reason)
		})
	}
}
