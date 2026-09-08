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

package renderplan

import (
	"errors"
	"reflect"
	"slices"
)

// BackendSource hands a document transition the plan's backends by name, so
// a transition from a snapshot built from an earlier state of the same source
// compares only the names that changed in between instead of every backend.
type BackendSource interface {
	// Token identifies the source's state; the snapshot built from it keeps
	// the token and offers it to the next transition. It must be comparable
	// (a pointer, or nil), since snapshots compare tokens with ==.
	Token() any
	// ChangedSince lists every name whose entry may differ from the state
	// token identifies. ok is false when the source cannot tell, and the
	// transition then compares every backend.
	ChangedSince(token any) (names []string, ok bool)
	// Get returns the backend by name; ContentKnown is true on every backend
	// the source returns.
	Get(name string) (Backend, bool)
	Len() int
	// Walk visits every backend in an unspecified order.
	Walk(func(name string, backend Backend) error) error
}

var (
	errBackendSourceContentUnknown     = errors.New("renderplan: backend source returned a backend without known content")
	errBackendSourceTokenNotComparable = errors.New("renderplan: backend source token is not comparable")
)

// backendSourceToken validates the token contract: snapshots compare tokens
// with ==, so a token that cannot be compared is refused.
func backendSourceToken(source BackendSource) (any, error) {
	token := source.Token()
	if token != nil && !reflect.TypeOf(token).Comparable() {
		return nil, errBackendSourceTokenNotComparable
	}
	return token, nil
}

// mapBackendSource is the full-map form: it has no token, so every transition
// compares every backend.
type mapBackendSource map[string]Backend

func (s mapBackendSource) Token() any                        { return nil }
func (s mapBackendSource) ChangedSince(any) ([]string, bool) { return nil, false }
func (s mapBackendSource) Len() int                          { return len(s) }

func (s mapBackendSource) Get(name string) (Backend, bool) {
	backend, exists := s[name]
	return backend, exists
}

func (s mapBackendSource) Walk(visit func(string, Backend) error) error {
	for name := range s {
		if err := visit(name, s[name]); err != nil {
			return err
		}
	}
	return nil
}

func backendSourceMap(source BackendSource) (map[string]Backend, error) {
	if m, ok := source.(mapBackendSource); ok {
		return m, nil
	}
	backends := make(map[string]Backend, source.Len())
	err := source.Walk(func(name string, backend Backend) error {
		if !backend.ContentKnown {
			return errBackendSourceContentUnknown
		}
		backends[name] = backend
		return nil
	})
	if err != nil {
		return nil, err
	}
	return backends, nil
}

// reconcileBackendSource compares the base collection with source over the
// names the source reports changed since the state base was built from,
// plus the backends whose sections changed, which is where a text digest
// moves without the record moving. Without a usable token it compares
// every backend.
func reconcileBackendSource(
	authority *Authority,
	base *snapshotCollection[Backend],
	baseToken any,
	source BackendSource,
	sections []*sealedSequenceChange[Section],
) ([]*sealedMapChange[Backend], error) {
	if source == nil {
		if base.present {
			return nil, ErrDocumentTransitionRequiresRebuild
		}
		return nil, nil
	}
	if !base.present {
		return nil, ErrDocumentTransitionRequiresRebuild
	}
	changed, ok := source.ChangedSince(baseToken)
	if !ok || baseToken == nil {
		backends, err := backendSourceMap(source)
		if err != nil {
			return nil, err
		}
		return reconcileMapCollection(
			authority, base, backendSnapshotCollection, backends, ownBackend, exactBackend,
		)
	}
	changes := make([]*sealedMapChange[Backend], 0)
	for _, name := range backendCandidates(changed, sections) {
		change, moved, err := backendSourceChange(authority, base, source, name)
		if err != nil {
			return nil, err
		}
		if moved {
			changes = append(changes, change)
		}
	}
	return changes, nil
}

func backendCandidates(changed []string, sections []*sealedSequenceChange[Section]) []string {
	names := slices.Clone(changed)
	for _, change := range sections {
		for _, entry := range []*snapshotEntry[Section]{change.before, change.after} {
			if entry != nil && entry.value.value.Kind == SectionKindBackend {
				names = append(names, entry.value.value.Name)
			}
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// backendSourceChange reports whether the source's entry differs from the
// base's, and the change when it does.
func backendSourceChange(
	authority *Authority,
	base *snapshotCollection[Backend],
	source BackendSource,
	name string,
) (*sealedMapChange[Backend], bool, error) {
	key := snapshotKey{index: -1, name: name}
	before, err := findSnapshotEntry(authority, backendSnapshotCollection, base, key)
	if err != nil && !errors.Is(err, errSnapshotEntryNotFound) {
		return nil, false, err
	}
	value, present := source.Get(name)
	if present && !value.ContentKnown {
		return nil, false, errBackendSourceContentUnknown
	}
	if before != nil && present && exactBackend(before.value.value, value) {
		return nil, false, nil
	}
	var after *snapshotEntry[Backend]
	if present {
		after = sealSnapshotEntry(authority, backendSnapshotCollection, key, ownBackend(value))
	}
	return sealMapChange(name, before, after), true, nil
}
