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

package deployplan

import (
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// InventoryOf projects what a worker that reloaded this plan would report as
// loaded. It answers "what would this pod be", which is what an offline
// comparison of two renders — `haptic diff` between two configurations, the
// playground's verdict — has instead of a pod. A live diff always uses the
// inventory the pod itself reported: a file the render declares is not proof
// the worker loaded it.
func InventoryOf(p *renderplan.Plan) api.Inventory {
	inventory := api.Inventory{}
	if p == nil {
		return inventory
	}
	for i := range p.Files {
		f := &p.Files[i]
		switch f.Kind {
		case api.FileKindMap:
			inventory.Maps = append(inventory.Maps, f.Path)
		case api.FileKindCert:
			inventory.Certs = append(inventory.Certs, f.Path)
		case api.FileKindCA:
			inventory.CAFiles = append(inventory.CAFiles, f.Path)
		case api.FileKindCRTList:
			inventory.CRTLists = append(inventory.CRTLists, f.Path)
		}
	}
	return inventory
}
