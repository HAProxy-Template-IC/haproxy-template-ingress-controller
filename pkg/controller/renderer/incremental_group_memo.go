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

package renderer

import (
	"errors"
	"fmt"
	"sync"

	iradix "github.com/hashicorp/go-immutable-radix/v2"

	"gitlab.com/haproxy-haptic/haptic/pkg/persistenttree"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type incrementalGroupMemoAuthority struct {
	seal *incrementalGroupMemoAuthority
}

type incrementalGroupMemoGeneration struct {
	seal *incrementalGroupMemoGeneration
}

type incrementalPublishedValuesMemo struct {
	authority   *incrementalGroupMemoAuthority
	key         incrementalPublishedValuesMemoKey
	values      []any
	certificate *templating.IncrementalImmutableCertificate
	seal        *incrementalPublishedValuesMemo
}

type incrementalPublishedValuesMemoKey struct {
	root *persistenttree.Node[incrementalIndexedPublication]
	cell string
}

type incrementalRankedFragmentsMemoKey struct {
	root *persistenttree.Node[incrementalIndexedPublication]
	cell string
}

type incrementalRankedFragmentsMemo struct {
	authority *incrementalGroupMemoAuthority
	key       incrementalRankedFragmentsMemoKey
	payload   *incrementalRankedFragmentsMemoPayload
	seal      *incrementalRankedFragmentsMemo
}

type incrementalRankedFragmentsMemoPayload struct {
	fragment rendercontent.TextFragment
	text     string
	seal     *incrementalRankedFragmentsMemoPayload
}

type incrementalGroupMemoState struct {
	mu sync.Mutex

	authority  *incrementalGroupMemoAuthority
	generation *incrementalGroupMemoGeneration
	seal       *incrementalGroupMemoState

	published *iradix.Tree[*incrementalPublishedValuesMemo]
	ranked    *iradix.Tree[*incrementalRankedFragmentsMemo]
	status    *incrementalStatusPatchProjectionMemo
	// decoded keeps each cell's last decoded winners by key. A cell whose
	// winner set moved loses its published entry, but a winner whose encoded
	// value did not change decodes to the same value, so the next read
	// decodes only the winners that changed instead of the whole cell.
	decoded *iradix.Tree[*incrementalDecodedWinners]
}

// incrementalDecodedWinners is one cell's decoded winners by winner key.
type incrementalDecodedWinners struct {
	values map[string]incrementalDecodedWinner
}

type incrementalDecodedWinner struct {
	encoded string
	value   any
}

type incrementalGroupMemo struct {
	authority  *incrementalGroupMemoAuthority
	generation *incrementalGroupMemoGeneration
	state      *incrementalGroupMemoState
	seal       *incrementalGroupMemo
}

var (
	incrementalEmptyPublishedValues            = []any{}
	incrementalEmptyPublishedValuesCertificate = templating.CertifyIncrementalImmutableInputs(
		incrementalEmptyPublishedValues,
	)
)

func newIncrementalGroupMemo() *incrementalGroupMemo {
	authority := &incrementalGroupMemoAuthority{}
	authority.seal = authority
	generation := &incrementalGroupMemoGeneration{}
	generation.seal = generation
	state := &incrementalGroupMemoState{
		authority:  authority,
		generation: generation,
		published:  iradix.New[*incrementalPublishedValuesMemo](),
		ranked:     iradix.New[*incrementalRankedFragmentsMemo](),
		decoded:    iradix.New[*incrementalDecodedWinners](),
	}
	state.seal = state
	memo := &incrementalGroupMemo{
		authority:  authority,
		generation: generation,
		state:      state,
	}
	memo.seal = memo
	return memo
}

func (m *incrementalGroupMemo) valid() bool {
	if m == nil || m.seal != m ||
		m.authority == nil || m.authority.seal != m.authority ||
		m.generation == nil || m.generation.seal != m.generation ||
		m.state == nil || m.state.seal != m.state ||
		m.state.authority != m.authority || m.state.generation != m.generation {
		return false
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	return m.state.published != nil && m.state.ranked != nil
}

func (m *incrementalGroupMemo) fork() (*incrementalGroupMemo, error) {
	if !m.valid() {
		return nil, errors.New("incremental group memo is unavailable")
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if m.state.published == nil || m.state.ranked == nil {
		return nil, errors.New("incremental group memo has invalid provenance")
	}
	generation := &incrementalGroupMemoGeneration{}
	generation.seal = generation
	state := &incrementalGroupMemoState{
		authority:  m.authority,
		generation: generation,
		published:  m.state.published,
		ranked:     m.state.ranked,
		status:     m.state.status,
		decoded:    m.state.decoded,
	}
	state.seal = state
	forked := &incrementalGroupMemo{
		authority: m.authority, generation: generation, state: state,
	}
	forked.seal = forked
	return forked, nil
}

func (m *incrementalGroupMemo) invalidateCell(cell string) error {
	if !m.valid() {
		return errors.New("incremental group memo is unavailable")
	}
	if cell == "" {
		return nil
	}
	key := incrementalOrderedTuple(cell)
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if m.state.published == nil || m.state.ranked == nil {
		return errors.New("incremental group memo has invalid provenance")
	}
	published := m.state.published.Txn()
	published.Delete(key)
	ranked := m.state.ranked.Txn()
	ranked.Delete(key)
	m.state.published = published.Commit()
	m.state.ranked = ranked.Commit()
	return nil
}

// decodedWinners returns the cell's last decoded winners, or nil.
func (m *incrementalGroupMemo) decodedWinners(cell string) map[string]incrementalDecodedWinner {
	if !m.valid() || cell == "" {
		return nil
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if m.state.decoded == nil {
		return nil
	}
	entry, exists := m.state.decoded.Root().Get(incrementalOrderedTuple(cell))
	if !exists || entry == nil {
		return nil
	}
	return entry.values
}

// storeDecodedWinners replaces the cell's decoded winners with the current set.
func (m *incrementalGroupMemo) storeDecodedWinners(cell string, values map[string]incrementalDecodedWinner) {
	if !m.valid() || cell == "" {
		return
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if m.state.decoded == nil {
		m.state.decoded = iradix.New[*incrementalDecodedWinners]()
	}
	txn := m.state.decoded.Txn()
	txn.Insert(incrementalOrderedTuple(cell), &incrementalDecodedWinners{values: values})
	m.state.decoded = txn.Commit()
}

func (m *incrementalGroupMemo) publishedValues(
	key incrementalPublishedValuesMemoKey,
) (*incrementalPublishedValuesMemo, bool, error) {
	if !m.valid() || key.root == nil || key.cell == "" {
		return nil, false, errors.New("incremental publication memo is unavailable")
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if m.state.published == nil {
		return nil, false, errors.New("incremental publication memo has invalid provenance")
	}
	entry, exists := m.state.published.Root().Get(incrementalOrderedTuple(key.cell))
	if !exists {
		return nil, false, nil
	}
	if !validIncrementalPublishedValuesMemo(entry, m.authority, key) {
		return nil, false, errors.New("incremental publication memo has invalid provenance")
	}
	return entry, true, nil
}

func validIncrementalPublishedValuesMemo(
	entry *incrementalPublishedValuesMemo,
	authority *incrementalGroupMemoAuthority,
	key incrementalPublishedValuesMemoKey,
) bool {
	return entry != nil && entry.seal == entry && entry.authority == authority && entry.key == key &&
		entry.certificate != nil && entry.certificate.Guards(entry.values)
}

func (m *incrementalGroupMemo) storePublishedValues(
	entry *incrementalPublishedValuesMemo,
) (*incrementalPublishedValuesMemo, error) {
	key := incrementalPublishedValuesMemoKey{}
	if entry != nil {
		key = entry.key
	}
	if !m.valid() || !validIncrementalPublishedValuesMemo(entry, m.authority, key) {
		return nil, errors.New("incremental publication memo value has invalid provenance")
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if m.state.published == nil {
		return nil, errors.New("incremental publication memo has invalid provenance")
	}
	cellKey := incrementalOrderedTuple(entry.key.cell)
	if existing, exists := m.state.published.Root().Get(cellKey); exists {
		if !validIncrementalPublishedValuesMemo(existing, m.authority, entry.key) {
			return nil, errors.New("incremental publication memo has invalid provenance")
		}
		return existing, nil
	}
	txn := m.state.published.Txn()
	txn.Insert(cellKey, entry)
	m.state.published = txn.Commit()
	return entry, nil
}

func (m *incrementalGroupMemo) rankedFragments(
	key incrementalRankedFragmentsMemoKey,
) (*incrementalRankedFragmentsMemo, bool, error) {
	if !m.valid() || key.root == nil || key.cell == "" {
		return nil, false, errors.New("incremental ranked-fragment memo is unavailable")
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if m.state.ranked == nil {
		return nil, false, errors.New("incremental ranked-fragment memo has invalid provenance")
	}
	entry, exists := m.state.ranked.Root().Get(incrementalOrderedTuple(key.cell))
	if !exists {
		return nil, false, nil
	}
	if !validIncrementalRankedFragmentsMemo(entry, m.authority, key) {
		return nil, false, errors.New("incremental ranked-fragment memo has invalid provenance")
	}
	return entry, true, nil
}

func validIncrementalRankedFragmentsMemo(
	entry *incrementalRankedFragmentsMemo,
	authority *incrementalGroupMemoAuthority,
	key incrementalRankedFragmentsMemoKey,
) bool {
	return entry != nil && entry.seal == entry && entry.authority == authority && entry.key == key &&
		entry.payload != nil && entry.payload.seal == entry.payload &&
		entry.payload.fragment.ValidateAuthentication() == nil
}

func (m *incrementalGroupMemo) storeRankedFragments(
	entry *incrementalRankedFragmentsMemo,
) (*incrementalRankedFragmentsMemo, error) {
	key := incrementalRankedFragmentsMemoKey{}
	if entry != nil {
		key = entry.key
	}
	if !m.valid() || !validIncrementalRankedFragmentsMemo(entry, m.authority, key) {
		return nil, errors.New("incremental ranked-fragment memo value has invalid provenance")
	}
	m.state.mu.Lock()
	defer m.state.mu.Unlock()
	if m.state.ranked == nil {
		return nil, errors.New("incremental ranked-fragment memo has invalid provenance")
	}
	cellKey := incrementalOrderedTuple(entry.key.cell)
	if existing, exists := m.state.ranked.Root().Get(cellKey); exists {
		if !validIncrementalRankedFragmentsMemo(existing, m.authority, entry.key) {
			return nil, errors.New("incremental ranked-fragment memo has invalid provenance")
		}
		return existing, nil
	}
	txn := m.state.ranked.Txn()
	txn.Insert(cellKey, entry)
	m.state.ranked = txn.Commit()
	return entry, nil
}

func (i *incrementalGroupIndex) certifiedPublishedValues(
	cell string,
) ([]any, *templating.IncrementalImmutableCertificate, error) {
	if err := i.validateAuthentication(); err != nil {
		return nil, nil, err
	}
	projection, exists := i.publicationWinnersByLocation.Root().Get(incrementalOrderedTuple(cell))
	if !exists {
		return incrementalEmptyPublishedValues, incrementalEmptyPublishedValuesCertificate, nil
	}
	if projection == nil || projection.Len() == 0 {
		return nil, nil, errors.New("incremental publication winner projection has an empty cell")
	}
	root := projection.Root()
	key := incrementalPublishedValuesMemoKey{root: root, cell: cell}
	if cached, found, err := i.memo.publishedValues(key); err != nil {
		return nil, nil, err
	} else if found {
		return cached.values, cached.certificate, nil
	}
	values := make([]any, 0, projection.Len())
	previous := i.memo.decodedWinners(cell)
	decoded := make(map[string]incrementalDecodedWinner, projection.Len())
	var decodeErr error
	projection.Root().Walk(func(_ string, winner incrementalIndexedPublication) bool {
		if winner.cell != cell {
			decodeErr = errors.New("incremental publication winner projection has a mismatched cell")
			return true
		}
		if known, exists := previous[winner.key]; exists && known.encoded == winner.value {
			values = append(values, known.value)
			decoded[winner.key] = known
			return false
		}
		value, err := decodeResourceValue([]byte(winner.value))
		if err != nil {
			decodeErr = fmt.Errorf("decoding incremental publication %q/%q: %w", cell, winner.key, err)
			return true
		}
		values = append(values, value)
		decoded[winner.key] = incrementalDecodedWinner{encoded: winner.value, value: value}
		return false
	})
	if decodeErr != nil {
		return nil, nil, decodeErr
	}
	i.memo.storeDecodedWinners(cell, decoded)
	entry := &incrementalPublishedValuesMemo{
		authority: i.memo.authority,
		key:       key,
		values:    values,
		certificate: templating.CertifyIncrementalImmutableInputs(
			values,
		),
	}
	entry.seal = entry
	stored, err := i.memo.storePublishedValues(entry)
	if err != nil {
		return nil, nil, err
	}
	return stored.values, stored.certificate, nil
}

func (i *incrementalGroupIndex) rankedFragments(cell, delimiter string) (string, error) {
	fragment, projection, err := i.rankedTextFragmentWithProjection(cell, "")
	if err != nil {
		return "", err
	}
	if projection == nil {
		return "", nil
	}
	key := incrementalRankedFragmentsMemoKey{root: projection.Root(), cell: cell}
	if cached, found, err := i.memo.rankedFragments(key); err != nil {
		return "", err
	} else if found {
		same, err := cached.payload.fragment.SameRoot(fragment)
		if err != nil {
			return "", fmt.Errorf("incremental ranked-fragment memo has invalid provenance: %w", err)
		}
		if !same {
			return "", errors.New("incremental ranked-fragment memo has invalid provenance")
		}
		return materializeRankedTextFragment(cached.payload.fragment, cached.payload.text, delimiter)
	}
	text, err := fragment.String()
	if err != nil {
		return "", err
	}
	payload := &incrementalRankedFragmentsMemoPayload{fragment: fragment, text: text}
	payload.seal = payload
	entry := &incrementalRankedFragmentsMemo{authority: i.memo.authority, key: key, payload: payload}
	entry.seal = entry
	stored, err := i.memo.storeRankedFragments(entry)
	if err != nil {
		return "", err
	}
	return materializeRankedTextFragment(stored.payload.fragment, stored.payload.text, delimiter)
}

func materializeRankedTextFragment(
	fragment rendercontent.TextFragment,
	withoutDelimiter string,
	delimiter string,
) (string, error) {
	if delimiter == "" {
		return withoutDelimiter, nil
	}
	withDelimiter, err := fragment.WithDelimiter(delimiter)
	if err != nil {
		return "", err
	}
	return withDelimiter.String()
}

func (i *incrementalGroupIndex) rankedTextFragment(
	cell, delimiter string,
) (rendercontent.TextFragment, error) {
	fragment, _, err := i.rankedTextFragmentWithProjection(cell, delimiter)
	return fragment, err
}

func (i *incrementalGroupIndex) rankedTextFragmentWithProjection(
	cell, delimiter string,
) (rendercontent.TextFragment, *persistenttree.Tree[incrementalIndexedPublication], error) {
	if err := i.validateAuthentication(); err != nil {
		return rendercontent.TextFragment{}, nil, err
	}
	cellKey := incrementalOrderedTuple(cell)
	projection, exists := i.publicationWinnersByRank.Root().Get(cellKey)
	if !exists {
		if _, retained := i.rankedText.Root().Get(cellKey); retained {
			return rendercontent.TextFragment{}, nil, errors.New("incremental ranked text index has a cell without winners")
		}
		return rendercontent.EmptyTextFragment(), nil, nil
	}
	if projection == nil || projection.Len() == 0 {
		return rendercontent.TextFragment{}, nil, errors.New("incremental ranked publication projection has an empty cell")
	}
	state, exists := i.rankedText.Root().Get(cellKey)
	if !exists {
		return rendercontent.TextFragment{}, nil, errors.New("incremental ranked text index is missing a cell")
	}
	if err := validateIncrementalRankedTextCell(state, projection, true); err != nil {
		return rendercontent.TextFragment{}, nil, fmt.Errorf("incremental ranked-fragment memo has invalid provenance: %w", err)
	}
	if state.unrankedCount != 0 {
		return rendercontent.TextFragment{}, nil, unrankedIncrementalFragmentError(cell, projection)
	}
	if state.nonStringCount != 0 {
		return rendercontent.TextFragment{}, nil, nonStringIncrementalFragmentError(cell, projection)
	}
	fragment, err := state.fragment.WithDelimiter(delimiter)
	if err != nil {
		return rendercontent.TextFragment{}, nil, err
	}
	return fragment, projection, nil
}

func unrankedIncrementalFragmentError(
	cell string,
	projection *persistenttree.Tree[incrementalIndexedPublication],
) error {
	var unranked *incrementalIndexedPublication
	projection.Root().Walk(func(_ string, winner incrementalIndexedPublication) bool {
		if winner.rank != "" {
			return false
		}
		candidate := winner
		unranked = &candidate
		return true
	})
	if unranked == nil {
		return errors.New("incremental ranked text index has an invalid unranked count")
	}
	return fmt.Errorf("incremental ranked fragment %q/%q has no rank", cell, unranked.key)
}

func nonStringIncrementalFragmentError(
	cell string,
	projection *persistenttree.Tree[incrementalIndexedPublication],
) error {
	var nonString *incrementalIndexedPublication
	var decoded any
	var decodeErr error
	projection.Root().Walk(func(_ string, winner incrementalIndexedPublication) bool {
		value, err := decodeResourceValue([]byte(winner.value))
		if err != nil {
			decodeErr = fmt.Errorf("decoding incremental ranked fragment %q/%q: %w", cell, winner.key, err)
			return true
		}
		if _, ok := value.(string); ok {
			return false
		}
		candidate := winner
		nonString = &candidate
		decoded = value
		return true
	})
	if decodeErr != nil {
		return decodeErr
	}
	if nonString == nil {
		return errors.New("incremental ranked text index has an invalid non-string count")
	}
	return fmt.Errorf(
		"incremental ranked fragment %q/%q must be a string, got %T", cell, nonString.key, decoded,
	)
}
