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

package deployplan_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func TestInventoryOf(t *testing.T) {
	inventory := deployplan.InventoryOf(&renderplan.Plan{Files: []renderplan.File{
		{Path: "haproxy.cfg", Kind: renderplan.FileKindConfig},
		{Path: "maps/host.map", Kind: renderplan.FileKindMap},
		{Path: "maps/path.map", Kind: renderplan.FileKindMap},
		{Path: "ssl/tls.pem", Kind: renderplan.FileKindCert},
		{Path: "ssl/ca.pem", Kind: renderplan.FileKindCA},
		{Path: "ssl/list.txt", Kind: renderplan.FileKindCRTList},
		{Path: "files/503.http", Kind: renderplan.FileKindGeneral},
	}})

	assert.Equal(t, api.Inventory{
		Maps:     []string{"maps/host.map", "maps/path.map"},
		Certs:    []string{"ssl/tls.pem"},
		CAFiles:  []string{"ssl/ca.pem"},
		CRTLists: []string{"ssl/list.txt"},
	}, inventory, "the config and general files are not things a worker loads by name")
}

func TestInventoryOfNoPlan(t *testing.T) {
	assert.Equal(t, api.Inventory{}, deployplan.InventoryOf(nil))
}
