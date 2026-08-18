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

package deployplan

import (
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// inPlaceKinds are the ops a pod may run while a reload is pending: each one
// changes an object the running worker already has and survives the reload,
// because the file set the reload loads carries the same state.
var inPlaceKinds = map[string]bool{
	api.OpServerSetAddr:   true,
	api.OpServerSetWeight: true,
	api.OpServerSetState:  true,
	api.OpServerAdd:       true,
	api.OpServerEnable:    true,
	api.OpServerDisable:   true,
	api.OpMapSet:          true,
	api.OpMapDel:          true,
	api.OpCertSet:         true,
}

// inPlaceOps composes the subset that runs while this pod waits for its
// reload, against the plan the worker actually has. A change it cannot express
// is dropped rather than reported: the pending reload applies all of it.
func (b *builder) inPlaceOps() []api.Op {
	if !b.baseline.ReloadPending {
		return nil
	}
	worker := firstPlan(b.baseline.WorkerOps, b.baseline.Running, b.baseline.Applied)
	if worker == nil {
		return nil
	}
	// The ops that create runtime-store objects never run while a reload is
	// pending, so the worker holds nothing this diff composed.
	b.created = nil
	ops := b.inPlaceServerOps(worker)
	ops = append(ops, b.inPlaceMapOps(worker)...)
	ops = append(ops, b.inPlaceCertOps(worker)...)
	kept := ops[:0]
	for i := range ops {
		if inPlaceKinds[ops[i].Kind] && b.caps.executes(ops[i].Kind) {
			kept = append(kept, ops[i])
		}
	}
	if len(kept) > api.MaxOpsPerApply {
		kept = kept[:api.MaxOpsPerApply]
	}
	return kept
}

func (b *builder) inPlaceServerOps(worker *renderplan.Plan) []api.Op {
	var ops []api.Op
	for _, name := range backendSections(b.next) {
		running, has := worker.Backends[name]
		if !has {
			continue
		}
		next := b.next.Backends[name]
		ops = append(ops, b.serverOpsAgainstWorker(&running, &next)...)
	}
	return ops
}

// serverOpsAgainstWorker judges an add by the backend the worker runs — that
// is the configuration HAProxy checks the command against — and takes the
// server's own values from the render.
func (b *builder) serverOpsAgainstWorker(running, next *renderplan.Backend) []api.Op {
	var ops []api.Op
	current := serverIndex(running.Servers)
	for i := range next.Servers {
		srv := &next.Servers[i]
		if old, existed := current[srv.Name]; existed {
			composed, _ := b.updateServer(next, old, srv)
			ops = append(ops, composed...)
			continue
		}
		composed, _ := b.addServer(running, srv)
		ops = append(ops, composed...)
	}
	desired := serverIndex(next.Servers)
	for i := range running.Servers {
		srv := &running.Servers[i]
		if _, kept := desired[srv.Name]; !kept {
			ops = append(ops, api.Op{Kind: api.OpServerDisable, Backend: next.Name, Server: srv.Name})
		}
	}
	return ops
}

// inPlaceMapOps keeps only the map ops that stand alone: an in-place value
// change and the deletion of a key the render dropped. The del half of a
// replacement would unmap a key whose re-add is not allowed here.
func (b *builder) inPlaceMapOps(worker *renderplan.Plan) []api.Op {
	var ops []api.Op
	for _, name := range sortedMapNames(b.next.Maps) {
		next := b.next.Maps[name]
		path := next.Path
		if path == "" {
			path = name
		}
		if !slices.Contains(b.inventory.Maps, path) || !api.SafeToken(path) {
			continue
		}
		prev := worker.Maps[name]
		delta := unorderedMapOps(path, prev.Entries, next.Entries)
		if next.Ordered {
			delta = orderedMapOps(path, prev.Entries, next.Entries)
		}
		if delta.whole {
			continue
		}
		for i := range delta.upserts {
			if delta.upserts[i].Kind == api.OpMapSet {
				ops = append(ops, delta.upserts[i])
			}
		}
		ops = append(ops, delta.deletes...)
	}
	return ops
}

func (b *builder) inPlaceCertOps(worker *renderplan.Plan) []api.Op {
	var ops []api.Op
	running := fileIndex(worker.Files)
	for i := range b.next.Files {
		f := &b.next.Files[i]
		old, existed := running[f.Path]
		if f.Kind != renderplan.FileKindCert || !existed || old.Digest == f.Digest {
			continue
		}
		if slices.Contains(b.inventory.Certs, f.Path) {
			ops = append(ops, api.Op{Kind: api.OpCertSet, Path: f.Path})
		}
	}
	return ops
}

func firstPlan(plans ...*renderplan.Plan) *renderplan.Plan {
	for _, p := range plans {
		if p != nil {
			return p
		}
	}
	return nil
}
