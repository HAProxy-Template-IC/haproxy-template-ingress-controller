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
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

// loggedBackendSource is a BackendSource over a map that logs the names its
// owner changed, the way the registry's prepared snapshot does. A text-only
// change is deliberately not logged: the transition has to find those
// through the section change.
type loggedBackendSource struct {
	backends map[string]Backend
	log      *loggedBackendStep
}

type loggedBackendStep struct {
	parent *loggedBackendStep
	name   string
}

func (s *loggedBackendSource) Token() any { return s.log }

func (s *loggedBackendSource) ChangedSince(token any) ([]string, bool) {
	base, ok := token.(*loggedBackendStep)
	if !ok || base == nil {
		return nil, false
	}
	var names []string
	for step := s.log; step != nil; step = step.parent {
		if step == base {
			return names, true
		}
		names = append(names, step.name)
	}
	return nil, false
}

func (s *loggedBackendSource) Get(name string) (Backend, bool) {
	backend, exists := s.backends[name]
	return backend, exists
}

func (s *loggedBackendSource) Len() int { return len(s.backends) }

func (s *loggedBackendSource) Walk(visit func(string, Backend) error) error {
	for name := range s.backends {
		if err := visit(name, s.backends[name]); err != nil {
			return err
		}
	}
	return nil
}

// backendDocumentState is one render's worth of backend sections: the plan,
// the document and the source are all derived from it.
type backendDocumentState struct {
	names    []string
	texts    map[string]string
	balances map[string]string
	next     int
}

func (s *backendDocumentState) clone() *backendDocumentState {
	next := &backendDocumentState{
		names:    slices.Clone(s.names),
		texts:    make(map[string]string, len(s.texts)),
		balances: make(map[string]string, len(s.balances)),
		next:     s.next,
	}
	for name, text := range s.texts {
		next.texts[name] = text
	}
	for name, balance := range s.balances {
		next.balances[name] = balance
	}
	return next
}

func (s *backendDocumentState) backend(name string) Backend {
	text := s.texts[name]
	body := strings.Split(strings.TrimSuffix(text, "\n"), "\n")[1:]
	return Backend{
		Name: name, Balance: s.balances[name],
		Body: body, BodyDigest: DigestString(strings.Join(body, "\n")),
		TextDigest: DigestString(text), ContentKnown: true,
	}
}

func (s *backendDocumentState) plan(tb testing.TB) (*Plan, map[string]Backend, rendercontent.Document) {
	tb.Helper()
	plan := &Plan{
		SchemaVersion: SchemaVersion,
		Profiles:      map[string]Profile{},
		Maps:          map[string]Map{},
		Files: []File{{
			Path: ConfigFilePath, Kind: FileKindConfig, ReloadOnChange: true,
		}},
	}
	backends := make(map[string]Backend, len(s.names))
	var documentBuilder rendercontent.DocumentBuilder
	size := 0
	for _, name := range s.names {
		text := s.texts[name]
		plan.Sections = append(plan.Sections, Section{
			Kind: SectionKindBackend, Name: name, Text: text, TextKnown: true,
			TextDigest: DigestString(text), Length: len(text),
		})
		backends[name] = s.backend(name)
		var partBuilder rendercontent.DocumentBuilder
		_, err := partBuilder.WriteString(text)
		require.NoError(tb, err)
		part, err := partBuilder.Build(nil)
		require.NoError(tb, err)
		require.NoError(tb, documentBuilder.AppendDocument(part))
		size += len(text)
	}
	document, err := documentBuilder.Build(nil)
	require.NoError(tb, err)
	plan.Files[0].Size = int64(size)
	return plan, backends, document
}

func newBackendDocumentState(count int) *backendDocumentState {
	state := &backendDocumentState{texts: map[string]string{}, balances: map[string]string{}}
	for index := range count {
		name := fmt.Sprintf("be%02d", index)
		state.names = append(state.names, name)
		state.texts[name] = fmt.Sprintf("backend %s\n  server s1 10.0.0.%d:80\n", name, index+1)
		state.balances[name] = "roundrobin"
	}
	state.next = count
	return state
}

// TestBackendSourceTransitionMatchesTheFullComparison drives random
// histories through both entry points: the map form compares every backend,
// the source form only the logged names plus the changed sections. Record
// changes, text changes, additions and removals must land identically.
func TestBackendSourceTransitionMatchesTheFullComparison(t *testing.T) {
	random := rand.New(rand.NewPCG(7, 11))
	for round := range 40 {
		history := newBackendHistory(t, 6)
		for step := range 12 {
			logged := history.state.mutate(random, step, round)
			history.advance(t, logged)
			history.requireSameTransition(t, fmt.Sprintf("round %d step %d", round, step))
		}
	}
}

// backendHistory keeps the sparse and the full lineage side by side.
type backendHistory struct {
	authority *Authority
	state     *backendDocumentState
	source    *loggedBackendSource
	sparse    *Snapshot
	full      *Snapshot
	sparseDlt *Delta
	fullDlt   *Delta
}

func newBackendHistory(tb testing.TB, count int) *backendHistory {
	tb.Helper()
	history := &backendHistory{authority: NewAuthority(), state: newBackendDocumentState(count)}
	plan, backends, document := history.state.plan(tb)
	history.source = &loggedBackendSource{backends: backends, log: &loggedBackendStep{}}
	var err error
	history.sparse, _, err = ReconcileSnapshotWithBackendSource(history.authority, nil, plan, history.source, document)
	require.NoError(tb, err)
	fullPlan := *plan
	fullPlan.Backends = backends
	history.full, _, err = ReconcileSnapshotWithConfigDocument(history.authority, nil, &fullPlan, document)
	require.NoError(tb, err)
	return history
}

// mutate applies one random step to the state and returns the names the
// source logs for it; a text-only change logs nothing.
func (s *backendDocumentState) mutate(random *rand.Rand, step, round int) []string {
	switch random.IntN(5) {
	case 0:
		name := s.names[random.IntN(len(s.names))]
		s.balances[name] = fmt.Sprintf("leastconn%d", step)
		return []string{name}
	case 1:
		name := s.names[random.IntN(len(s.names))]
		s.texts[name] = fmt.Sprintf("backend %s\n  server s1 10.0.0.%d:80\n  # r%d\n", name, step, round)
		return nil
	case 2:
		name := fmt.Sprintf("be%02d", s.next)
		s.next++
		at := random.IntN(len(s.names) + 1)
		s.names = slices.Insert(s.names, at, name)
		s.texts[name] = fmt.Sprintf("backend %s\n  server s1 10.0.1.%d:80\n", name, step)
		s.balances[name] = "roundrobin"
		return []string{name}
	case 3:
		if len(s.names) < 3 {
			return nil
		}
		at := random.IntN(len(s.names))
		name := s.names[at]
		s.names = slices.Delete(s.names, at, at+1)
		delete(s.texts, name)
		delete(s.balances, name)
		return []string{name}
	default:
		return nil
	}
}

func (h *backendHistory) advance(tb testing.TB, logged []string) {
	tb.Helper()
	plan, backends, document := h.state.plan(tb)
	log := h.source.log
	for _, name := range logged {
		log = &loggedBackendStep{parent: log, name: name}
	}
	h.source = &loggedBackendSource{backends: backends, log: log}
	var err error
	h.sparse, h.sparseDlt, err = ReconcileSnapshotWithBackendSource(h.authority, h.sparse, plan, h.source, document)
	require.NoError(tb, err)
	fullPlan := *plan
	fullPlan.Backends = backends
	h.full, h.fullDlt, err = ReconcileSnapshotWithConfigDocument(h.authority, h.full, &fullPlan, document)
	require.NoError(tb, err)
}

func (h *backendHistory) requireSameTransition(tb testing.TB, at string) {
	tb.Helper()
	sparseID, err := h.sparse.ID()
	require.NoError(tb, err)
	fullID, err := h.full.ID()
	require.NoError(tb, err)
	require.Equal(tb, fullID, sparseID, at)
	require.Equal(tb, changedBackendNames(tb, h.fullDlt), changedBackendNames(tb, h.sparseDlt), at)
	sparseLegacy, err := h.sparse.LegacyCopy()
	require.NoError(tb, err)
	fullLegacy, err := h.full.LegacyCopy()
	require.NoError(tb, err)
	require.True(tb, ExactlyEqual(fullLegacy, sparseLegacy), at)
}

func changedBackendNames(tb testing.TB, delta *Delta) []string {
	tb.Helper()
	changes, err := delta.Changes()
	require.NoError(tb, err)
	names := make([]string, 0, len(changes.Backends))
	for _, change := range changes.Backends {
		names = append(names, change.Name)
	}
	slices.Sort(names)
	return names
}

// TestBackendSourceTransitionFallsBackWithoutALineage compares every backend
// when the previous snapshot came from a map or from another source.
func TestBackendSourceTransitionFallsBackWithoutALineage(t *testing.T) {
	authority := NewAuthority()
	state := newBackendDocumentState(4)
	plan, backends, document := state.plan(t)
	fullPlan := *plan
	fullPlan.Backends = backends
	fromMap, _, err := ReconcileSnapshotWithConfigDocument(authority, nil, &fullPlan, document)
	require.NoError(t, err)
	require.Nil(t, fromMap.source)

	next := state.clone()
	next.balances["be01"] = "leastconn"
	plan, backends, document = next.plan(t)
	// An unrelated lineage: its log does not reach the previous snapshot's
	// token, and its log would not have named be01 anyway.
	source := &loggedBackendSource{backends: backends, log: &loggedBackendStep{}}
	snapshot, delta, err := ReconcileSnapshotWithBackendSource(authority, fromMap, plan, source, document)
	require.NoError(t, err)
	require.Equal(t, []string{"be01"}, changedBackendNames(t, delta))
	require.Same(t, source.log, snapshot.source)

	other := &loggedBackendSource{backends: backends, log: &loggedBackendStep{}}
	again, delta, err := ReconcileSnapshotWithBackendSource(authority, fromMap, plan, other, document)
	require.NoError(t, err)
	require.Equal(t, []string{"be01"}, changedBackendNames(t, delta))
	require.Same(t, other.log, again.source)
}

// TestBackendSourceTransitionRejectsAPlanThatStillCarriesBackends pins that
// the two forms cannot be mixed.
func TestBackendSourceTransitionRejectsAPlanThatStillCarriesBackends(t *testing.T) {
	authority := NewAuthority()
	state := newBackendDocumentState(2)
	plan, backends, document := state.plan(t)
	plan.Backends = backends
	source := &loggedBackendSource{backends: backends, log: &loggedBackendStep{}}
	_, _, err := ReconcileSnapshotWithBackendSource(authority, nil, plan, source, document)
	require.ErrorIs(t, err, errInexactSnapshotPlan)
}

type sliceTokenBackendSource struct{ mapBackendSource }

func (s sliceTokenBackendSource) Token() any { return []string{"not comparable"} }

// TestBackendSourceTransitionRejectsATokenSnapshotsCannotCompare pins the
// Token contract: a snapshot compares tokens with ==, so a source whose token
// is not comparable is refused before anything is sealed.
func TestBackendSourceTransitionRejectsATokenSnapshotsCannotCompare(t *testing.T) {
	authority := NewAuthority()
	state := newBackendDocumentState(2)
	plan, backends, document := state.plan(t)
	source := sliceTokenBackendSource{mapBackendSource(backends)}
	_, _, err := ReconcileSnapshotWithBackendSource(authority, nil, plan, source, document)
	require.ErrorIs(t, err, errBackendSourceTokenNotComparable)
}
