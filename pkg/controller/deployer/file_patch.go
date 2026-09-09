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

package deployer

import (
	"slices"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

// filePatch is a file delivered as the bytes that differ from the one the
// pod holds.
type filePatch struct {
	patch api.FilePatch
	data  string
}

// patchMemo shares one deployment's splices across its pods: every pod that
// acknowledged the same content gets the same patch.
type patchMemo struct {
	mu      sync.Mutex
	patches map[patchKey]filePatch
	missing map[patchKey]bool
}

type patchKey struct {
	path       string
	baseDigest string
}

func newPatchMemo() *patchMemo {
	return &patchMemo{patches: map[patchKey]filePatch{}, missing: map[patchKey]bool{}}
}

// get returns the splice of next into base, computing it on first sight.
func (m *patchMemo) get(path string, base *contentProof, next string) (filePatch, bool) {
	key := patchKey{path: path, baseDigest: base.digest}
	m.mu.Lock()
	defer m.mu.Unlock()
	if patch, known := m.patches[key]; known {
		return patch, true
	}
	if m.missing[key] {
		return filePatch{}, false
	}
	patch, ok := contentPatch(base, next)
	if ok {
		m.patches[key] = patch
	} else {
		m.missing[key] = true
	}
	return patch, ok
}

// contentPatch is the splice that turns the content the pod holds into next:
// the run between the common prefix and the common suffix. A splice of more
// than half the file is not worth the agent's read of the base.
func contentPatch(base *contentProof, next string) (filePatch, bool) {
	prev := base.content
	prefix := commonPrefix(prev, next)
	suffix := commonSuffix(prev[prefix:], next[prefix:])
	data := next[prefix : len(next)-suffix]
	if len(data)*2 > len(next) {
		return filePatch{}, false
	}
	return filePatch{
		patch: api.FilePatch{
			BaseDigest: base.digest, BaseSize: int64(len(prev)),
			Offset: int64(prefix), Length: int64(len(prev) - prefix - suffix),
		},
		data: data,
	}, true
}

// compareChunk is compared as one string, which the runtime does at memory
// speed; the byte loop only finishes the last chunk.
const compareChunk = 4096

func commonPrefix(a, b string) int {
	n := min(len(a), len(b))
	i := 0
	for i+compareChunk <= n && a[i:i+compareChunk] == b[i:i+compareChunk] {
		i += compareChunk
	}
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

func commonSuffix(a, b string) int {
	n := min(len(a), len(b))
	i := 0
	for i+compareChunk <= n && a[len(a)-i-compareChunk:len(a)-i] == b[len(b)-i-compareChunk:len(b)-i] {
		i += compareChunk
	}
	for i < n && a[len(a)-1-i] == b[len(b)-1-i] {
		i++
	}
	return i
}

func acceptsFilePatches(state *api.State) bool {
	return state != nil && slices.Contains(state.Features, api.FeatureFilePatch)
}
