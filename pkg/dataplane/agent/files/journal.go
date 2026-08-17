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

package files

// EntryKind is how a path looked when the last-known-good set still held it.
type EntryKind string

// The three shapes a restore has to undo.
const (
	KindModified EntryKind = "modified" // Backup holds the LKG content.
	KindCreated  EntryKind = "created"  // The path did not exist; restore unlinks it.
	KindDeleted  EntryKind = "deleted"  // Backup holds the content the apply unlinked.
)

// Entry is one path's backup record. Backup is an absolute path inside the
// path's own mount, empty for KindCreated.
type Entry struct {
	Path   string    `json:"path"`
	Kind   EntryKind `json:"kind"`
	Backup string    `json:"backup,omitempty"`
}

// Journal holds the last-known-good version of every path changed since the
// LKG plan. The first entry per path wins: later applies must not overwrite a
// backup with an already-diverged version.
type Journal struct {
	Entries []Entry `json:"entries,omitempty"`
}

// Has reports whether the LKG version of rel is already backed up.
func (j *Journal) Has(rel string) bool {
	for _, e := range j.Entries {
		if e.Path == rel {
			return true
		}
	}
	return false
}

// Empty reports whether the on-disk set is the LKG set.
func (j *Journal) Empty() bool { return len(j.Entries) == 0 }

func (j *Journal) add(e Entry) {
	if j.Has(e.Path) {
		return
	}
	j.Entries = append(j.Entries, e)
}
