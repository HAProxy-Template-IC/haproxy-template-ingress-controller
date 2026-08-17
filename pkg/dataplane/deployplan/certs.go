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
	"maps"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// diffCerts applies rule 6. A certificate the running worker does not hold is
// created before anything can reference it, because add ssl crt-list resolves
// against the runtime store rather than the filesystem.
func (b *builder) diffCerts() {
	for i := range b.next.Files {
		f := &b.next.Files[i]
		old, existed := b.prevFiles[f.Path]
		if existed && old.Digest == f.Digest {
			continue
		}
		switch f.Kind {
		case renderplan.FileKindCert:
			b.storeFile(f.Path, api.OpCertSet, api.OpCertNew, b.inventory.Certs)
		case renderplan.FileKindCA:
			b.storeFile(f.Path, api.OpCASet, api.OpCANew, b.inventory.CAFiles)
		case renderplan.FileKindCRTList:
			b.diffCRTList(f.Path, existed)
		}
	}
	for i := range b.prev.Files {
		f := &b.prev.Files[i]
		if _, kept := b.nextFiles[f.Path]; !kept && f.Kind == renderplan.FileKindCRTList {
			b.failf("crt-list %s removed, which only a reload takes out of the config", f.Path)
		}
	}
}

func (b *builder) storeFile(path, set, create string, loaded []string) {
	if !safeToken(path) {
		b.failf("file %s is not a safe runtime token", path)
		return
	}
	kind := create
	if slices.Contains(loaded, path) {
		kind = set
	}
	b.push(groupCert, api.Op{Kind: kind, Path: path})
}

// diffCRTList turns the entry lists of two renders into crt-list ops. A list
// the config gains or loses is a core change, and one the render did not
// describe cannot be reached entry by entry.
func (b *builder) diffCRTList(path string, existed bool) {
	if !existed {
		b.failf("crt-list %s added, which only a reload puts into the config", path)
		return
	}
	prev, hadEntries := b.prev.CRTLists[path]
	next, hasEntries := b.next.CRTLists[path]
	switch {
	case !hadEntries || !hasEntries:
		b.failf("crt-list %s changed but the render declared no entries for it", path)
	case !slices.Contains(b.inventory.CRTLists, path):
		b.notef("crt-list %s is not loaded at runtime, its file is written only", path)
	default:
		b.crtListOps(path, prev.Entries, next.Entries)
	}
}

func (b *builder) crtListOps(path string, prev, next []renderplan.CRTListEntry) {
	before, after := crtListIndex(prev), crtListIndex(next)
	for _, cert := range slices.Sorted(maps.Keys(after)) {
		entry := after[cert]
		if !safeToken(cert) {
			b.failf("crt-list %s: certificate %s is not a safe runtime token", path, cert)
			return
		}
		old, existed := before[cert]
		switch {
		case !existed:
			b.push(groupCRTList, crtListAdd(path, entry))
		case sameCRTListEntry(old, entry):
		default:
			// Options and SNI filters are only replaceable as a whole entry.
			b.push(groupCRTList, crtListDel(path, cert), crtListAdd(path, entry))
		}
	}
	for _, cert := range slices.Sorted(maps.Keys(before)) {
		if _, kept := after[cert]; !kept {
			b.push(groupCRTList, crtListDel(path, cert))
		}
	}
}

func crtListAdd(path string, entry *renderplan.CRTListEntry) api.Op {
	return api.Op{
		Kind:       api.OpCRTListAdd,
		Path:       path,
		Cert:       entry.Cert,
		Options:    apiKeywords(entry.Options),
		SNIFilters: slices.Clone(entry.SNIFilters),
	}
}

func crtListDel(path, cert string) api.Op {
	return api.Op{Kind: api.OpCRTListDel, Path: path, Cert: cert}
}

func crtListIndex(entries []renderplan.CRTListEntry) map[string]*renderplan.CRTListEntry {
	index := make(map[string]*renderplan.CRTListEntry, len(entries))
	for i := range entries {
		index[entries[i].Cert] = &entries[i]
	}
	return index
}

func sameCRTListEntry(prev, next *renderplan.CRTListEntry) bool {
	return slices.EqualFunc(prev.Options, next.Options, sameKeyword) &&
		slices.Equal(prev.SNIFilters, next.SNIFilters)
}
