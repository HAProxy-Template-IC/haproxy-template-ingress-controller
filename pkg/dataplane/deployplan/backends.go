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

// diffBackends applies rules 2 to 4 in the order the render emitted the
// sections, which is deterministic without sorting anything.
func (b *builder) diffBackends() {
	for _, name := range backendSections(b.next) {
		next, described := b.next.Backends[name]
		if !described {
			b.failf("backend %s: section without a record", name)
			continue
		}
		prev, existed := b.prev.Backends[name]
		if !existed {
			b.sectionChange = true
			b.backendAdded(&next)
			continue
		}
		b.sectionChange = b.sectionChange || prev.TextDigest != next.TextDigest
		b.backendChanged(&prev, &next)
	}
	for _, name := range backendSections(b.prev) {
		if _, kept := b.next.Backends[name]; kept {
			continue
		}
		if prev, described := b.prev.Backends[name]; described {
			b.sectionChange = true
			b.backendRemoved(&prev)
		}
	}
}

// backendAdded composes rule 2: create, populate, then publish.
func (b *builder) backendAdded(be *renderplan.Backend) {
	switch {
	case be.Shape != renderplan.ShapeDynamic:
		b.failf("backend %s added: %s", be.Name, shapeReason(be))
	case !b.caps.DynamicBackends:
		b.failf("backend %s added: this HAProxy has no add backend", be.Name)
	case !api.SafeToken(be.Name) || !api.SafeToken(be.Profile) || (be.GUID != "" && !api.SafeToken(be.GUID)):
		b.failf("backend %s added: the name, profile or guid is not a safe runtime token", be.Name)
	case !b.profileInRunningConfig(be.Profile):
		b.failf("backend %s added: profile %q is not in the running config", be.Name, be.Profile)
	default:
		b.publishNewBackend(be)
	}
}

// profileInRunningConfig reports whether add backend can inherit this profile:
// "from" is mandatory and the named defaults must already be parsed.
func (b *builder) profileInRunningConfig(name string) bool {
	if name == "" {
		return false
	}
	_, ok := b.prev.Profiles[name]
	return ok
}

func (b *builder) publishNewBackend(be *renderplan.Backend) {
	adds := make([]api.Op, 0, 2*len(be.Servers))
	for i := range be.Servers {
		ops, reason := b.addServer(be, &be.Servers[i])
		if reason != "" {
			b.failf("backend %s added: %s", be.Name, reason)
			return
		}
		adds = append(adds, ops...)
	}
	b.push(groupBackendAdd, api.Op{
		Kind:    api.OpBackendAdd,
		Backend: be.Name,
		Profile: be.Profile,
		Mode:    be.Mode,
		GUID:    be.GUID,
	})
	b.push(groupBackendAdd, adds...)
	b.push(groupBackendPublish, api.Op{Kind: api.OpBackendPublish, Backend: be.Name})
}

// backendRemoved composes rule 3: unpublish, drain every server, then delete.
func (b *builder) backendRemoved(be *renderplan.Backend) {
	switch {
	case be.Shape != renderplan.ShapeDynamic:
		b.failf("backend %s removed: %s", be.Name, shapeReason(be))
	case !b.caps.DynamicBackends:
		b.failf("backend %s removed: this HAProxy has no del backend", be.Name)
	case b.pendingBackendDeletes >= api.MaxPendingBackendDeletes:
		b.failf("backend %s removed: %d backend deletes already pending", be.Name, b.pendingBackendDeletes)
	default:
		b.deleteBackend(be)
	}
}

func (b *builder) deleteBackend(be *renderplan.Backend) {
	drains := make([]api.Op, 0, 3*len(be.Servers))
	for i := range be.Servers {
		ops, reason := b.removeServer(be.Name, &be.Servers[i])
		if reason != "" {
			b.failf("backend %s removed: %s", be.Name, reason)
			return
		}
		drains = append(drains, ops...)
	}
	b.push(groupBackendUnpublish, api.Op{Kind: api.OpBackendUnpublish, Backend: be.Name})
	b.push(groupServerDel, drains...)
	b.push(groupBackendDel,
		api.Op{Kind: api.OpBackendWaitRemovable, Backend: be.Name, TimeoutMs: removableTimeoutMs},
		api.Op{Kind: api.OpBackendDel, Backend: be.Name},
	)
	b.deletedByOps[be.Name] = true
	b.pendingBackendDeletes++
}

// backendChanged applies the section guard for one backend and then rule 4.
func (b *builder) backendChanged(prev, next *renderplan.Backend) {
	switch {
	case prev.BodyDigest != next.BodyDigest:
		b.failf("backend %s: body changed", next.Name)
	case unexplainedText(prev, next):
		b.failf("backend %s: unexplained text change", next.Name)
	case next.RecordDigest != "" && prev.RecordDigest == next.RecordDigest:
		// An equal record digest is an equal record: no attribute and no
		// server moved, so nothing is left to compose for this backend.
	default:
		if attr := changedAttribute(prev, next); attr != "" {
			b.failf("backend %s: %s changed, which HAProxy cannot alter at runtime", next.Name, attr)
			return
		}
		b.diffServers(prev, next)
	}
}

// unexplainedText reports a section whose text moved without any record,
// comment or body change accounting for it — the generator's derivation check.
func unexplainedText(prev, next *renderplan.Backend) bool {
	return prev.TextDigest != next.TextDigest &&
		prev.RecordDigest == next.RecordDigest &&
		prev.CommentsDigest == next.CommentsDigest
}

// changedAttribute names the first backend attribute that only a reload can
// change, or "" when none did.
func changedAttribute(prev, next *renderplan.Backend) string {
	switch {
	case prev.Profile != next.Profile:
		return "profile"
	case prev.Mode != next.Mode:
		return "mode"
	case prev.GUID != next.GUID:
		return "guid"
	case prev.Balance != next.Balance:
		return "balance"
	case prev.HashType != next.HashType:
		return "hash-type"
	case prev.Shape != next.Shape:
		return "shape"
	case !slices.EqualFunc(prev.DefaultServer, next.DefaultServer, sameKeyword):
		return "default-server"
	}
	return ""
}

// diffServers composes rule 4's per-server ops.
func (b *builder) diffServers(prev, next *renderplan.Backend) {
	previous := serverIndex(prev.Servers)
	for i := range next.Servers {
		srv := &next.Servers[i]
		ops, reason := []api.Op(nil), ""
		if old, existed := previous[srv.Name]; existed {
			ops, reason = b.updateServer(next, old, srv)
		} else {
			ops, reason = b.addServer(next, srv)
		}
		b.push(groupServerUpdate, b.serverOps(ops, reason, next.Name, srv.Name)...)
	}
	current := serverIndex(next.Servers)
	for i := range prev.Servers {
		srv := &prev.Servers[i]
		if _, kept := current[srv.Name]; !kept {
			ops, reason := b.removeServer(prev.Name, srv)
			b.push(groupServerDel, b.serverOps(ops, reason, prev.Name, srv.Name)...)
		}
	}
}

// serverOps records a composer's refusal against the server it names.
func (b *builder) serverOps(ops []api.Op, reason, backend, server string) []api.Op {
	if reason != "" {
		b.failf("backend %s/server %s: %s", backend, server, reason)
		return nil
	}
	return ops
}

func shapeReason(be *renderplan.Backend) string {
	if be.ShapeReason != "" {
		return "structural shape, " + be.ShapeReason
	}
	return "structural shape"
}
