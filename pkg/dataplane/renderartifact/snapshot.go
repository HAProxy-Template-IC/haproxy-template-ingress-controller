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
	"strings"
	"sync"
)

// Authority owns one lineage of exact snapshot roots.
type Authority struct {
	seal *Authority
}

// NewAuthority creates an isolated snapshot lineage.
func NewAuthority() *Authority {
	authority := &Authority{}
	authority.seal = authority
	return authority
}

// ValidateAuthentication verifies the authority's exact identity.
func (a *Authority) ValidateAuthentication() error {
	if a == nil || a.seal != a {
		return errInvalidAuthority
	}
	return nil
}

type artifactAuthentication struct {
	owner      *Artifact
	authority  *Authority
	descriptor *descriptorData
	content    *Content
}

// Artifact is an authenticated descriptor and immutable content pair.
type Artifact struct {
	authority  *Authority
	descriptor *descriptorData
	content    *Content
	seal       *Artifact
	auth       artifactAuthentication
}

func sealArtifact(authority *Authority, descriptor *descriptorData, content *Content) *Artifact {
	artifact := &Artifact{
		authority:  authority,
		descriptor: descriptor,
		content:    content,
	}
	artifact.seal = artifact
	artifact.auth = artifactAuthentication{
		owner:      artifact,
		authority:  artifact.authority,
		descriptor: artifact.descriptor,
		content:    artifact.content,
	}
	return artifact
}

// ValidateAuthentication verifies the artifact's exact ownership chain.
func (a *Artifact) ValidateAuthentication() error {
	if a == nil || a.seal != a || a.auth.owner != a || a.authority == nil ||
		a.auth.authority != a.authority || a.descriptor == nil ||
		a.auth.descriptor != a.descriptor || a.content == nil || a.auth.content != a.content {
		return errInvalidArtifact
	}
	if err := a.authority.ValidateAuthentication(); err != nil {
		return errors.Join(errInvalidArtifact, err)
	}
	if err := a.descriptor.validate(); err != nil {
		return err
	}
	if err := a.content.ValidateAuthentication(); err != nil {
		return errors.Join(errInvalidArtifact, err)
	}
	return nil
}

// Descriptor returns a detached descriptor value.
func (a *Artifact) Descriptor() (Descriptor, error) {
	if err := a.ValidateAuthentication(); err != nil {
		return Descriptor{}, err
	}
	return a.descriptor.detached(), nil
}

// Content returns the authenticated immutable payload.
func (a *Artifact) Content() (*Content, error) {
	if err := a.ValidateAuthentication(); err != nil {
		return nil, err
	}
	return a.content, nil
}

// SameRoot reports exact artifact identity.
func (a *Artifact) SameRoot(other *Artifact) (bool, error) {
	if err := a.ValidateAuthentication(); err != nil {
		return false, err
	}
	if err := other.ValidateAuthentication(); err != nil {
		return false, err
	}
	return a == other, nil
}

func exactArtifactEqual(left, right *Artifact) (bool, error) {
	if err := left.ValidateAuthentication(); err != nil {
		return false, err
	}
	if err := right.ValidateAuthentication(); err != nil {
		return false, err
	}
	if left == right {
		return true, nil
	}
	if !descriptorsEqual(left.descriptor, right.descriptor) {
		return false, nil
	}
	return exactContentEqual(left.content, right.content)
}

type snapshotNodeAuthentication struct {
	owner     *Authority
	artifact  *Artifact
	left      *snapshotNode
	right     *snapshotNode
	height    int
	artifacts int
}

type snapshotNode struct {
	owner     *Authority
	artifact  *Artifact
	left      *snapshotNode
	right     *snapshotNode
	height    int
	artifacts int
	seal      *snapshotNode
	auth      snapshotNodeAuthentication
}

func newSnapshotNode(authority *Authority, artifact *Artifact, left, right *snapshotNode) *snapshotNode {
	node := &snapshotNode{
		owner:     authority,
		artifact:  artifact,
		left:      left,
		right:     right,
		height:    max(snapshotNodeHeight(left), snapshotNodeHeight(right)) + 1,
		artifacts: snapshotNodeCount(left) + snapshotNodeCount(right) + 1,
	}
	node.seal = node
	node.auth = snapshotNodeAuthentication{
		owner:     node.owner,
		artifact:  node.artifact,
		left:      node.left,
		right:     node.right,
		height:    node.height,
		artifacts: node.artifacts,
	}
	return node
}

func (n *snapshotNode) validateShallow(authority *Authority) error {
	if n == nil || n.seal != n || n.owner != authority || n.auth.owner != n.owner ||
		n.artifact == nil || n.auth.artifact != n.artifact || n.auth.left != n.left ||
		n.auth.right != n.right || n.auth.height != n.height ||
		n.auth.artifacts != n.artifacts || n.height < 1 || n.artifacts < 1 {
		return errInvalidSnapshot
	}
	if err := n.artifact.ValidateAuthentication(); err != nil {
		return errors.Join(errInvalidSnapshot, err)
	}
	if n.artifact.authority != authority {
		return errInvalidSnapshot
	}
	return nil
}

func snapshotNodeHeight(node *snapshotNode) int {
	if node == nil {
		return 0
	}
	return node.height
}

func snapshotNodeCount(node *snapshotNode) int {
	if node == nil {
		return 0
	}
	return node.artifacts
}

type snapshotAuthentication struct {
	owner     *Snapshot
	authority *Authority
	root      *snapshotNode
	artifacts int
}

// Snapshot is an authenticated immutable sorted artifact set.
type Snapshot struct {
	authority *Authority
	root      *snapshotNode
	artifacts int
	seal      *Snapshot
	auth      snapshotAuthentication
}

func sealSnapshot(authority *Authority, root *snapshotNode) *Snapshot {
	snapshot := &Snapshot{
		authority: authority,
		root:      root,
		artifacts: snapshotNodeCount(root),
	}
	snapshot.seal = snapshot
	snapshot.auth = snapshotAuthentication{
		owner:     snapshot,
		authority: snapshot.authority,
		root:      snapshot.root,
		artifacts: snapshot.artifacts,
	}
	return snapshot
}

// ValidateSnapshot verifies that snapshot is authenticated by this authority.
func (a *Authority) ValidateSnapshot(snapshot *Snapshot) error {
	if err := a.ValidateAuthentication(); err != nil {
		return err
	}
	if err := snapshot.ValidateAuthentication(); err != nil {
		return err
	}
	if snapshot.authority != a {
		return errForeignSnapshot
	}
	return nil
}

// ValidateAuthentication verifies the exact immutable root in constant time.
func (s *Snapshot) ValidateAuthentication() error {
	if s == nil || s.seal != s || s.auth.owner != s || s.authority == nil ||
		s.auth.authority != s.authority || s.auth.root != s.root ||
		s.auth.artifacts != s.artifacts || s.artifacts < 0 {
		return errInvalidSnapshot
	}
	if err := s.authority.ValidateAuthentication(); err != nil {
		return errors.Join(errInvalidSnapshot, err)
	}
	if s.root == nil {
		if s.artifacts != 0 {
			return errInvalidSnapshot
		}
		return nil
	}
	if err := s.root.validateShallow(s.authority); err != nil {
		return err
	}
	if s.root.artifacts != s.artifacts {
		return errInvalidSnapshot
	}
	return nil
}

// Len returns the number of artifacts.
func (s *Snapshot) Len() (int, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return 0, err
	}
	return s.artifacts, nil
}

// SameRoot reports exact authenticated set identity.
func (s *Snapshot) SameRoot(other *Snapshot) (bool, error) {
	if err := s.ValidateAuthentication(); err != nil {
		return false, err
	}
	if err := other.ValidateAuthentication(); err != nil {
		return false, err
	}
	return s.authority == other.authority && s.root == other.root, nil
}

// ExactEqual compares every descriptor and final byte when roots differ; digests only reject.
func (s *Snapshot) ExactEqual(other *Snapshot) (bool, error) {
	same, err := s.SameRoot(other)
	if err != nil || same {
		return same, err
	}
	if s.artifacts != other.artifacts {
		return false, nil
	}
	left := newSnapshotCursor(s.root)
	right := newSnapshotCursor(other.root)
	for {
		leftArtifact, leftFound, leftErr := left.next(s.authority)
		if leftErr != nil {
			return false, leftErr
		}
		rightArtifact, rightFound, rightErr := right.next(other.authority)
		if rightErr != nil {
			return false, rightErr
		}
		if leftFound != rightFound {
			return false, errInvalidSnapshot
		}
		if !leftFound {
			return true, nil
		}
		equal, equalErr := exactArtifactEqual(leftArtifact, rightArtifact)
		if equalErr != nil || !equal {
			return equal, equalErr
		}
	}
}

// Walk visits artifacts in family and canonical-identity order.
func (s *Snapshot) Walk(visit func(*Artifact) error) error {
	if err := s.ValidateAuthentication(); err != nil {
		return err
	}
	if visit == nil {
		return errNilVisitor
	}
	cursor := newSnapshotCursor(s.root)
	for {
		artifact, found, err := cursor.next(s.authority)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if err := visit(artifact); err != nil {
			return err
		}
	}
}

type snapshotCursor struct {
	stack []*snapshotNode
}

func newSnapshotCursor(root *snapshotNode) *snapshotCursor {
	cursor := &snapshotCursor{stack: make([]*snapshotNode, 0, snapshotNodeHeight(root))}
	cursor.pushLeft(root)
	return cursor
}

func (c *snapshotCursor) pushLeft(node *snapshotNode) {
	for node != nil {
		c.stack = append(c.stack, node)
		node = node.left
	}
}

func (c *snapshotCursor) next(authority *Authority) (*Artifact, bool, error) {
	if len(c.stack) == 0 {
		return nil, false, nil
	}
	last := len(c.stack) - 1
	node := c.stack[last]
	c.stack = c.stack[:last]
	if err := node.validateShallow(authority); err != nil {
		return nil, false, err
	}
	c.pushLeft(node.right)
	return node.artifact, true, nil
}

// Builder seals one complete canonical artifact set.
type Builder struct {
	mu        sync.Mutex
	authority *Authority
	previous  *Snapshot
	entries   map[artifactKey]*Artifact
	storage   map[sharedStorageKey]*Artifact
	built     *Snapshot
	err       error
}

// NewBuilder starts a complete set under authority. Previous must belong to the
// same authority so exact roots and unchanged artifacts can be reused.
func NewBuilder(authority *Authority, previous *Snapshot) (*Builder, error) {
	if err := authority.ValidateAuthentication(); err != nil {
		return nil, err
	}
	if previous != nil {
		if err := previous.ValidateAuthentication(); err != nil {
			return nil, err
		}
		if previous.authority != authority {
			return nil, errForeignSnapshot
		}
	}
	return &Builder{
		authority: authority,
		previous:  previous,
		entries:   make(map[artifactKey]*Artifact),
		storage:   make(map[sharedStorageKey]*Artifact),
	}, nil
}

// Add declares one artifact. Exact duplicate declarations are idempotent.
func (b *Builder) Add(descriptor Descriptor, content *Content) error {
	if b == nil {
		return errInvalidAuthority
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.built != nil {
		return errBuilderSealed
	}
	if b.err != nil {
		return b.err
	}
	if err := b.authority.ValidateAuthentication(); err != nil {
		b.err = err
		return err
	}
	if content == nil {
		b.err = errNilContent
		return b.err
	}
	if err := content.ValidateAuthentication(); err != nil {
		b.err = err
		return err
	}
	descriptor, key, err := canonicalizeDescriptor(descriptor)
	if err != nil {
		b.err = err
		return err
	}
	if existing := b.entries[key]; existing != nil {
		equal, equalErr := declarationEqual(existing, descriptor, content)
		if equalErr != nil {
			b.err = equalErr
			return equalErr
		}
		if equal {
			return nil
		}
		b.err = conflictingDefinition(descriptor)
		return b.err
	}
	if storageKey, shared := descriptorSharedStorage(descriptor); shared {
		if existing := b.storage[storageKey]; existing != nil {
			b.err = fmt.Errorf(
				"render artifact %q conflicts with %q in shared general storage",
				descriptor.Name,
				existing.descriptor.value.Name,
			)
			return b.err
		}
	}
	artifact, reuseErr := b.reuseOrSeal(descriptor, key, content)
	if reuseErr != nil {
		b.err = reuseErr
		return reuseErr
	}
	b.entries[artifact.descriptor.key] = artifact
	if storageKey, shared := descriptorSharedStorage(artifact.descriptor.value); shared {
		b.storage[storageKey] = artifact
	}
	return nil
}

func declarationEqual(existing *Artifact, descriptor Descriptor, content *Content) (bool, error) {
	if err := existing.ValidateAuthentication(); err != nil {
		return false, err
	}
	if existing.descriptor.value != descriptor {
		return false, nil
	}
	return exactContentEqual(existing.content, content)
}

func conflictingDefinition(descriptor Descriptor) error {
	return fmt.Errorf(
		"render artifact family %d name %q has conflicting definitions",
		descriptor.Family,
		descriptor.Name,
	)
}

func (b *Builder) reuseOrSeal(descriptor Descriptor, key artifactKey, content *Content) (*Artifact, error) {
	if b.previous != nil {
		previous, err := findSnapshotArtifact(b.previous.authority, b.previous.root, key)
		if err != nil && !errors.Is(err, errArtifactNotFound) {
			return nil, err
		}
		if previous != nil {
			equal, err := declarationEqual(previous, descriptor, content)
			if err != nil {
				return nil, err
			}
			if equal {
				return previous, nil
			}
		}
	}
	owned, err := normalizeDescriptor(descriptor)
	if err != nil {
		return nil, err
	}
	return sealArtifact(b.authority, owned, content), nil
}

// Build seals the complete set atomically.
func (b *Builder) Build() (*Snapshot, error) {
	if b == nil {
		return nil, errInvalidAuthority
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.built != nil {
		if err := b.built.ValidateAuthentication(); err != nil {
			return nil, err
		}
		return b.built, nil
	}
	if err := b.validateForBuild(); err != nil {
		return nil, err
	}
	artifacts, err := b.sortedArtifacts()
	if err != nil {
		return nil, err
	}
	exact, err := b.exactPrevious(artifacts)
	if err != nil {
		return nil, err
	}
	if exact {
		b.built = b.previous
		return b.built, nil
	}
	var previousRoot *snapshotNode
	if b.previous != nil {
		previousRoot = b.previous.root
	}
	tree, err := buildSnapshotTree(b.authority, artifacts, previousRoot)
	if err != nil {
		return nil, err
	}
	b.built = sealSnapshot(b.authority, tree.root)
	return b.built, nil
}

func (b *Builder) validateForBuild() error {
	if b.err != nil {
		return b.err
	}
	if err := b.authority.ValidateAuthentication(); err != nil {
		return err
	}
	if b.previous == nil {
		return nil
	}
	if err := b.previous.ValidateAuthentication(); err != nil {
		return err
	}
	if b.previous.authority != b.authority {
		return errForeignSnapshot
	}
	return nil
}

func (b *Builder) sortedArtifacts() ([]*Artifact, error) {
	artifacts := make([]*Artifact, 0, len(b.entries))
	for _, artifact := range b.entries {
		if err := artifact.ValidateAuthentication(); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	slices.SortFunc(artifacts, compareArtifacts)
	return artifacts, nil
}

func (b *Builder) exactPrevious(artifacts []*Artifact) (bool, error) {
	if b.previous == nil {
		return false, nil
	}
	exact, err := exactArtifactSequence(b.previous, artifacts)
	if err != nil {
		return false, err
	}
	return exact, nil
}

func compareArtifacts(left, right *Artifact) int {
	leftKey := left.descriptor.key
	rightKey := right.descriptor.key
	if leftKey.family < rightKey.family {
		return -1
	}
	if leftKey.family > rightKey.family {
		return 1
	}
	return strings.Compare(leftKey.name, rightKey.name)
}

func exactArtifactSequence(previous *Snapshot, artifacts []*Artifact) (bool, error) {
	if previous.artifacts != len(artifacts) {
		return false, nil
	}
	cursor := newSnapshotCursor(previous.root)
	for _, artifact := range artifacts {
		previousArtifact, found, err := cursor.next(previous.authority)
		if err != nil {
			return false, err
		}
		if !found || previousArtifact != artifact {
			return false, nil
		}
	}
	_, found, err := cursor.next(previous.authority)
	if err != nil {
		return false, err
	}
	return !found, nil
}

type snapshotTreeResult struct {
	root *snapshotNode
}

func buildSnapshotTree(authority *Authority, artifacts []*Artifact, previous *snapshotNode) (snapshotTreeResult, error) {
	if len(artifacts) == 0 {
		return snapshotTreeResult{}, nil
	}
	middle := len(artifacts) / 2
	artifact := artifacts[middle]
	var previousLeft, previousRight *snapshotNode
	if previous != nil {
		if err := previous.validateShallow(authority); err != nil {
			return snapshotTreeResult{}, err
		}
		if previous.artifact.descriptor.key == artifact.descriptor.key {
			previousLeft = previous.left
			previousRight = previous.right
		} else {
			previous = nil
		}
	}
	left, err := buildSnapshotTree(authority, artifacts[:middle], previousLeft)
	if err != nil {
		return snapshotTreeResult{}, err
	}
	right, err := buildSnapshotTree(authority, artifacts[middle+1:], previousRight)
	if err != nil {
		return snapshotTreeResult{}, err
	}
	if previous != nil && previous.artifact == artifact && previous.left == left.root && previous.right == right.root {
		return snapshotTreeResult{root: previous}, nil
	}
	return snapshotTreeResult{root: newSnapshotNode(authority, artifact, left.root, right.root)}, nil
}

func findSnapshotArtifact(authority *Authority, node *snapshotNode, key artifactKey) (*Artifact, error) {
	for node != nil {
		if err := node.validateShallow(authority); err != nil {
			return nil, err
		}
		comparison := compareArtifactKeys(key, node.artifact.descriptor.key)
		switch {
		case comparison < 0:
			node = node.left
		case comparison > 0:
			node = node.right
		default:
			return node.artifact, nil
		}
	}
	return nil, errArtifactNotFound
}

func compareArtifactKeys(left, right artifactKey) int {
	if left.family < right.family {
		return -1
	}
	if left.family > right.family {
		return 1
	}
	return strings.Compare(left.name, right.name)
}
