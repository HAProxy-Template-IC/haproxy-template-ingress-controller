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

package renderartifact

import (
	"errors"
	"fmt"
	"slices"
	"sync"
)

var (
	errInvalidArtifactHandle      = errors.New("render artifact handle is invalid")
	errInvalidArtifactDelta       = errors.New("render artifact delta is invalid")
	errInvalidArtifactTransaction = errors.New("render artifact transaction is invalid")
	errArtifactTransactionSealed  = errors.New("render artifact transaction is sealed")
	errArtifactChangeConflict     = errors.New("render artifact transaction repeats an artifact key")
	errArtifactAlreadyPresent     = errors.New("render artifact is already present")
)

// Handle proves the exact artifact currently stored under one canonical key.
type Handle struct {
	base     *Snapshot
	artifact *Artifact
	key      artifactKey
	seal     *Handle
}

// SnapshotChange is one authenticated artifact transition.
type SnapshotChange struct {
	Before *Artifact
	After  *Artifact
}

type sealedSnapshotChange struct {
	before *Artifact
	after  *Artifact
	key    artifactKey
	seal   *sealedSnapshotChange
	auth   sealedSnapshotChangeAuthentication
}

type sealedSnapshotChangeAuthentication struct {
	owner  *sealedSnapshotChange
	before *Artifact
	after  *Artifact
	key    artifactKey
}

type deltaAuthentication struct {
	owner      *Delta
	authority  *Authority
	base       *Snapshot
	next       *Snapshot
	changes    []*sealedSnapshotChange
	structural bool
}

// Delta is an authenticated transition between exact artifact roots.
type Delta struct {
	authority  *Authority
	base       *Snapshot
	next       *Snapshot
	changes    []*sealedSnapshotChange
	structural bool
	seal       *Delta
	auth       deltaAuthentication
}

type transactionAuthentication struct {
	owner     *Transaction
	authority *Authority
	base      *Snapshot
}

// Transaction atomically path-copies artifact changes from one exact base.
type Transaction struct {
	mu         sync.Mutex
	authority  *Authority
	base       *Snapshot
	changes    map[artifactKey]*sealedSnapshotChange
	structural bool
	built      *Snapshot
	delta      *Delta
	err        error
	sealed     bool
	seal       *Transaction
	auth       transactionAuthentication
}

// Lookup returns an exact handle for descriptor's canonical key.
func (s *Snapshot) Lookup(descriptor Descriptor) (*Handle, bool, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return nil, false, err
	}
	_, key, err := canonicalizeDescriptor(descriptor)
	if err != nil {
		return nil, false, err
	}
	artifact, err := findSnapshotArtifact(s.authority, s.root, key)
	if errors.Is(err, errArtifactNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	handle := &Handle{base: s, artifact: artifact, key: key}
	handle.seal = handle
	return handle, true, nil
}

// BeginTransaction starts an atomic edit against base.
func BeginTransaction(authority *Authority, base *Snapshot) (*Transaction, error) {
	if err := authority.ValidateSnapshot(base); err != nil {
		return nil, err
	}
	transaction := &Transaction{
		authority: authority,
		base:      base,
		changes:   make(map[artifactKey]*sealedSnapshotChange),
	}
	transaction.seal = transaction
	transaction.auth = transactionAuthentication{
		owner: transaction, authority: authority, base: base,
	}
	return transaction, nil
}

// Insert adds an artifact only when its canonical key is absent from base.
func (t *Transaction) Insert(descriptor Descriptor, content *Content) error {
	if t == nil {
		return errInvalidArtifactTransaction
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validateOpen(); err != nil {
		return t.recordError(err)
	}
	owned, key, err := ownTransactionArtifact(t.authority, descriptor, content)
	if err != nil {
		return t.recordError(err)
	}
	if _, findErr := findSnapshotArtifact(t.authority, t.base.root, key); findErr == nil {
		return t.recordError(errArtifactAlreadyPresent)
	} else if !errors.Is(findErr, errArtifactNotFound) {
		return t.recordError(findErr)
	}
	t.structural = true
	return t.addChange(nil, owned, key)
}

// Replace changes only the exact artifact proven by expected.
func (t *Transaction) Replace(expected *Handle, descriptor Descriptor, content *Content) error {
	if t == nil {
		return errInvalidArtifactTransaction
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validateOpen(); err != nil {
		return t.recordError(err)
	}
	if err := validateArtifactHandle(expected, t.base); err != nil {
		return t.recordError(err)
	}
	owned, key, err := ownTransactionArtifact(t.authority, descriptor, content)
	if err != nil {
		return t.recordError(err)
	}
	if key != expected.key {
		return t.recordError(errors.New("render artifact replacement changes its canonical key"))
	}
	equal, err := exactArtifactEqual(expected.artifact, owned)
	if err != nil {
		return t.recordError(err)
	}
	if equal {
		return nil
	}
	if expected.artifact.descriptor.value != owned.descriptor.value {
		t.structural = true
	}
	return t.addChange(expected.artifact, owned, key)
}

// Delete removes only the exact artifact proven by expected.
func (t *Transaction) Delete(expected *Handle) error {
	if t == nil {
		return errInvalidArtifactTransaction
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validateOpen(); err != nil {
		return t.recordError(err)
	}
	if err := validateArtifactHandle(expected, t.base); err != nil {
		return t.recordError(err)
	}
	t.structural = true
	return t.addChange(expected.artifact, nil, expected.key)
}

// Commit seals the next snapshot and its exact base-to-next proof.
func (t *Transaction) Commit() (*Snapshot, *Delta, error) {
	if t == nil {
		return nil, nil, errInvalidArtifactTransaction
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.validateAuthentication(); err != nil {
		return nil, nil, err
	}
	if t.sealed {
		if t.err != nil {
			return nil, nil, t.err
		}
		if err := t.delta.ValidateAuthentication(); err != nil {
			return nil, nil, err
		}
		return t.built, t.delta, nil
	}
	t.sealed = true
	if t.err != nil {
		return nil, nil, t.err
	}
	changes := make([]*sealedSnapshotChange, 0, len(t.changes))
	for _, change := range t.changes {
		changes = append(changes, change)
	}
	slices.SortFunc(changes, func(left, right *sealedSnapshotChange) int {
		return compareArtifactKeys(left.key, right.key)
	})
	root := t.base.root
	for _, change := range changes {
		var err error
		if change.after == nil {
			root, _, err = deleteArtifactNode(t.authority, root, change.key)
		} else {
			root, _, err = putArtifactNode(t.authority, root, change.after)
		}
		if err != nil {
			t.err = err
			return nil, nil, err
		}
	}
	if err := validateChangedSharedStorage(t.authority, root, changes); err != nil {
		t.err = err
		return nil, nil, err
	}
	t.built = t.base
	if root != t.base.root {
		t.built = sealSnapshot(t.authority, root)
	}
	t.delta = sealDelta(t.authority, t.base, t.built, changes, t.structural)
	return t.built, t.delta, nil
}

// Apply returns this delta's next root only for its exact base.
func (d *Delta) Apply(base *Snapshot) (*Snapshot, error) {
	if err := d.ValidateAuthentication(); err != nil {
		return nil, err
	}
	if base != d.base {
		return nil, errInvalidArtifactDelta
	}
	return d.next, nil
}

// Changes returns the authenticated changed artifacts without materializing content.
func (d *Delta) Changes() ([]SnapshotChange, error) {
	if err := d.ValidateAuthentication(); err != nil {
		return nil, err
	}
	changes := make([]SnapshotChange, len(d.changes))
	for index, change := range d.changes {
		changes[index] = SnapshotChange{Before: change.before, After: change.after}
	}
	return changes, nil
}

// RequiresFullValidation reports set or descriptor changes that affect unchanged consumers.
func (d *Delta) RequiresFullValidation() (bool, error) {
	if err := d.ValidateAuthentication(); err != nil {
		return false, err
	}
	return d.structural, nil
}

// ValidateAuthentication verifies the exact transition and every changed record.
func (d *Delta) ValidateAuthentication() error {
	if err := validateArtifactDeltaHeader(d); err != nil {
		return errInvalidArtifactDelta
	}
	if err := d.authority.ValidateSnapshot(d.base); err != nil {
		return errors.Join(errInvalidArtifactDelta, err)
	}
	if err := d.authority.ValidateSnapshot(d.next); err != nil {
		return errors.Join(errInvalidArtifactDelta, err)
	}
	structural, countDelta, err := validateArtifactDeltaChanges(d)
	if err != nil {
		return err
	}
	if structural != d.structural || d.next.artifacts != d.base.artifacts+countDelta ||
		len(d.changes) == 0 && d.base != d.next {
		return errInvalidArtifactDelta
	}
	return nil
}

func validateArtifactDeltaHeader(d *Delta) error {
	if d == nil || d.seal != d || d.authority == nil || d.base == nil || d.next == nil {
		return errInvalidArtifactDelta
	}
	expected := deltaAuthentication{
		owner: d, authority: d.authority, base: d.base, next: d.next,
		changes: d.auth.changes, structural: d.structural,
	}
	if d.auth.owner != expected.owner || d.auth.authority != expected.authority ||
		d.auth.base != expected.base || d.auth.next != expected.next ||
		d.auth.structural != expected.structural || !sameChangeSlice(d.auth.changes, d.changes) {
		return errInvalidArtifactDelta
	}
	return nil
}

func validateArtifactDeltaChanges(d *Delta) (structural bool, countDelta int, err error) {
	for _, change := range d.changes {
		if err := validateSealedSnapshotChange(d.authority, d.base, d.next, change); err != nil {
			return false, 0, errors.Join(errInvalidArtifactDelta, err)
		}
		switch {
		case change.before == nil:
			structural = true
			countDelta++
		case change.after == nil:
			structural = true
			countDelta--
		case change.before.descriptor.value != change.after.descriptor.value:
			structural = true
		}
	}
	return structural, countDelta, nil
}

// SameRoot reports whether no artifact changed.
func (d *Delta) SameRoot() (bool, error) {
	if err := d.ValidateAuthentication(); err != nil {
		return false, err
	}
	return d.base == d.next, nil
}

func sealDelta(
	authority *Authority,
	base, next *Snapshot,
	changes []*sealedSnapshotChange,
	structural bool,
) *Delta {
	owned := slices.Clone(changes)
	delta := &Delta{
		authority: authority, base: base, next: next, changes: owned, structural: structural,
	}
	delta.seal = delta
	delta.auth = deltaAuthentication{
		owner: delta, authority: authority, base: base, next: next,
		changes: slices.Clone(owned), structural: structural,
	}
	return delta
}

func (t *Transaction) addChange(before, after *Artifact, key artifactKey) error {
	if _, exists := t.changes[key]; exists {
		return t.recordError(errArtifactChangeConflict)
	}
	change := &sealedSnapshotChange{before: before, after: after, key: key}
	change.seal = change
	change.auth = sealedSnapshotChangeAuthentication{
		owner: change, before: before, after: after, key: key,
	}
	t.changes[key] = change
	return nil
}

func (t *Transaction) validateOpen() error {
	if err := t.validateAuthentication(); err != nil {
		return err
	}
	if t.sealed {
		return errArtifactTransactionSealed
	}
	if t.err != nil {
		return t.err
	}
	return nil
}

func (t *Transaction) validateAuthentication() error {
	if t == nil || t.seal != t || t.auth.owner != t || t.authority == nil ||
		t.auth.authority != t.authority || t.base == nil || t.auth.base != t.base ||
		t.changes == nil {
		return errInvalidArtifactTransaction
	}
	if err := t.authority.ValidateSnapshot(t.base); err != nil {
		return errors.Join(errInvalidArtifactTransaction, err)
	}
	return nil
}

func (t *Transaction) recordError(err error) error {
	if t.err == nil {
		t.err = err
	}
	return err
}

func ownTransactionArtifact(
	authority *Authority,
	descriptor Descriptor,
	content *Content,
) (*Artifact, artifactKey, error) {
	if content == nil {
		return nil, artifactKey{}, errNilContent
	}
	if err := content.ValidateAuthentication(); err != nil {
		return nil, artifactKey{}, err
	}
	owned, err := normalizeDescriptor(descriptor)
	if err != nil {
		return nil, artifactKey{}, err
	}
	return sealArtifact(authority, owned, content), owned.key, nil
}

func validateArtifactHandle(handle *Handle, base *Snapshot) error {
	if handle == nil || handle.seal != handle || handle.base != base || handle.artifact == nil {
		return errInvalidArtifactHandle
	}
	found, err := findSnapshotArtifact(base.authority, base.root, handle.key)
	if err != nil || found != handle.artifact {
		return errInvalidArtifactHandle
	}
	return nil
}

func validateSealedSnapshotChange(
	authority *Authority,
	base, next *Snapshot,
	change *sealedSnapshotChange,
) error {
	if err := validateSnapshotChangeAuthentication(change); err != nil {
		return errInvalidArtifactDelta
	}
	if err := validateSnapshotChangeSide(authority, base, change.key, change.before); err != nil {
		return errInvalidArtifactDelta
	}
	if err := validateSnapshotChangeSide(authority, next, change.key, change.after); err != nil {
		return errInvalidArtifactDelta
	}
	return nil
}

func validateSnapshotChangeAuthentication(change *sealedSnapshotChange) error {
	if change == nil || change.seal != change || change.before == nil && change.after == nil {
		return errInvalidArtifactDelta
	}
	expected := sealedSnapshotChangeAuthentication{
		owner: change, before: change.before, after: change.after, key: change.key,
	}
	if change.auth != expected {
		return errInvalidArtifactDelta
	}
	return nil
}

func validateSnapshotChangeSide(
	authority *Authority,
	snapshot *Snapshot,
	key artifactKey,
	expected *Artifact,
) error {
	if expected == nil {
		_, err := findSnapshotArtifact(authority, snapshot.root, key)
		if errors.Is(err, errArtifactNotFound) {
			return nil
		}
		return errInvalidArtifactDelta
	}
	if err := expected.ValidateAuthentication(); err != nil || expected.authority != authority ||
		expected.descriptor.key != key {
		return errInvalidArtifactDelta
	}
	found, err := findSnapshotArtifact(authority, snapshot.root, key)
	if err != nil || found != expected {
		return errInvalidArtifactDelta
	}
	return nil
}

func validateChangedSharedStorage(
	authority *Authority,
	root *snapshotNode,
	changes []*sealedSnapshotChange,
) error {
	for _, change := range changes {
		if change.after == nil {
			continue
		}
		storageKey, shared := descriptorSharedStorage(change.after.descriptor.value)
		if !shared {
			continue
		}
		for _, family := range []Family{General, GeneralCA, CRTList} {
			candidate, err := findSnapshotArtifact(authority, root, artifactKey{family: family, name: storageKey.name})
			if errors.Is(err, errArtifactNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if candidate != change.after {
				return fmt.Errorf(
					"render artifact %q conflicts with %q in shared general storage",
					change.after.descriptor.value.Name,
					candidate.descriptor.value.Name,
				)
			}
		}
	}
	return nil
}

func sameChangeSlice(left, right []*sealedSnapshotChange) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func putArtifactNode(
	authority *Authority,
	node *snapshotNode,
	artifact *Artifact,
) (*snapshotNode, bool, error) {
	if node == nil {
		return newSnapshotNode(authority, artifact, nil, nil), true, nil
	}
	if err := node.validateShallow(authority); err != nil {
		return nil, false, err
	}
	comparison := compareArtifactKeys(artifact.descriptor.key, node.artifact.descriptor.key)
	if comparison == 0 {
		if node.artifact == artifact {
			return node, false, nil
		}
		return newSnapshotNode(authority, artifact, node.left, node.right), true, nil
	}
	left, right := node.left, node.right
	var changed bool
	var err error
	if comparison < 0 {
		left, changed, err = putArtifactNode(authority, left, artifact)
	} else {
		right, changed, err = putArtifactNode(authority, right, artifact)
	}
	if err != nil || !changed {
		return node, changed, err
	}
	return balanceArtifactNodes(authority, node.artifact, left, right), true, nil
}

func deleteArtifactNode(
	authority *Authority,
	node *snapshotNode,
	key artifactKey,
) (*snapshotNode, bool, error) {
	if node == nil {
		return nil, false, errArtifactNotFound
	}
	if err := node.validateShallow(authority); err != nil {
		return nil, false, err
	}
	comparison := compareArtifactKeys(key, node.artifact.descriptor.key)
	left, right := node.left, node.right
	switch {
	case comparison < 0:
		var changed bool
		var err error
		left, changed, err = deleteArtifactNode(authority, left, key)
		if err != nil || !changed {
			return node, changed, err
		}
	case comparison > 0:
		var changed bool
		var err error
		right, changed, err = deleteArtifactNode(authority, right, key)
		if err != nil || !changed {
			return node, changed, err
		}
	default:
		if left == nil {
			return right, true, nil
		}
		if right == nil {
			return left, true, nil
		}
		var successor *Artifact
		var err error
		right, successor, err = deleteMinimumArtifactNode(authority, right)
		if err != nil {
			return nil, false, err
		}
		node = newSnapshotNode(authority, successor, left, right)
		return balanceArtifactNodes(authority, node.artifact, node.left, node.right), true, nil
	}
	return balanceArtifactNodes(authority, node.artifact, left, right), true, nil
}

func deleteMinimumArtifactNode(
	authority *Authority,
	node *snapshotNode,
) (*snapshotNode, *Artifact, error) {
	if err := node.validateShallow(authority); err != nil {
		return nil, nil, err
	}
	if node.left == nil {
		return node.right, node.artifact, nil
	}
	left, artifact, err := deleteMinimumArtifactNode(authority, node.left)
	if err != nil {
		return nil, nil, err
	}
	return balanceArtifactNodes(authority, node.artifact, left, node.right), artifact, nil
}

func balanceArtifactNodes(
	authority *Authority,
	artifact *Artifact,
	left, right *snapshotNode,
) *snapshotNode {
	switch {
	case snapshotNodeHeight(left) > snapshotNodeHeight(right)+1:
		pivot := left
		if snapshotNodeHeight(pivot.left) >= snapshotNodeHeight(pivot.right) {
			newRight := newSnapshotNode(authority, artifact, pivot.right, right)
			return newSnapshotNode(authority, pivot.artifact, pivot.left, newRight)
		}
		middle := pivot.right
		newLeft := newSnapshotNode(authority, pivot.artifact, pivot.left, middle.left)
		newRight := newSnapshotNode(authority, artifact, middle.right, right)
		return newSnapshotNode(authority, middle.artifact, newLeft, newRight)
	case snapshotNodeHeight(right) > snapshotNodeHeight(left)+1:
		pivot := right
		if snapshotNodeHeight(pivot.right) >= snapshotNodeHeight(pivot.left) {
			newLeft := newSnapshotNode(authority, artifact, left, pivot.left)
			return newSnapshotNode(authority, pivot.artifact, newLeft, pivot.right)
		}
		middle := pivot.left
		newLeft := newSnapshotNode(authority, artifact, left, middle.left)
		newRight := newSnapshotNode(authority, pivot.artifact, middle.right, pivot.right)
		return newSnapshotNode(authority, middle.artifact, newLeft, newRight)
	default:
		return newSnapshotNode(authority, artifact, left, right)
	}
}
