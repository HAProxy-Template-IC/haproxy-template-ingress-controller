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

package renderoutput

import (
	"errors"
	"slices"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

type outputFileBindingAuthentication struct {
	owner          *outputFileBinding
	descriptor     renderplan.FileDescriptor
	legacy         renderplan.File
	document       rendercontent.Document
	documentBacked bool
}

type outputFileBinding struct {
	descriptor     renderplan.FileDescriptor
	legacy         renderplan.File
	document       rendercontent.Document
	documentBacked bool
	seal           *outputFileBinding
	auth           outputFileBindingAuthentication
}

func sealLegacyOutputFileBinding(file *renderplan.File) *outputFileBinding {
	owned := *file
	owned.Path = strings.Clone(file.Path)
	owned.Kind = strings.Clone(file.Kind)
	owned.Digest = strings.Clone(file.Digest)
	owned.Content = strings.Clone(file.Content)
	return sealOutputFileBinding(renderplan.FileDescriptor{
		Path: owned.Path, Kind: owned.Kind, ReloadOnChange: owned.ReloadOnChange, Size: owned.Size,
	}, &owned, rendercontent.Document{}, false)
}

func sealDocumentOutputFileBinding(
	descriptor renderplan.FileDescriptor,
	document rendercontent.Document,
) *outputFileBinding {
	descriptor.Path = strings.Clone(descriptor.Path)
	descriptor.Kind = strings.Clone(descriptor.Kind)
	return sealOutputFileBinding(
		descriptor, nil, document, true,
	)
}

func sealOutputFileBinding(
	descriptor renderplan.FileDescriptor,
	legacy *renderplan.File,
	document rendercontent.Document,
	documentBacked bool,
) *outputFileBinding {
	legacyValue := renderplan.File{}
	if legacy != nil {
		legacyValue = *legacy
	}
	binding := &outputFileBinding{
		descriptor: descriptor, legacy: legacyValue, document: document, documentBacked: documentBacked,
	}
	binding.seal = binding
	binding.auth = outputFileBindingAuthentication{
		owner: binding, descriptor: descriptor, legacy: legacyValue,
		document: document, documentBacked: documentBacked,
	}
	return binding
}

func (b *outputFileBinding) validate() error {
	if b == nil || b.seal != b {
		return errInvalidSnapshot
	}
	expected := outputFileBindingAuthentication{
		owner: b, descriptor: b.descriptor, legacy: b.legacy,
		document: b.document, documentBacked: b.documentBacked,
	}
	if b.auth != expected || b.descriptor.Path == "" {
		return errInvalidSnapshot
	}
	if b.documentBacked {
		return b.validateDocument()
	}
	return b.validateLegacy()
}

func (b *outputFileBinding) validateDocument() error {
	if b.legacy != (renderplan.File{}) ||
		b.descriptor.Path != renderplan.ConfigFilePath ||
		b.descriptor.Kind != renderplan.FileKindConfig ||
		!b.descriptor.ReloadOnChange {
		return errInvalidSnapshot
	}
	bytes, err := b.document.Bytes()
	if err != nil || int64(bytes) != b.descriptor.Size {
		return errors.Join(errInvalidSnapshot, err)
	}
	return nil
}

func (b *outputFileBinding) validateLegacy() error {
	if b.document != (rendercontent.Document{}) ||
		b.descriptor.Path != b.legacy.Path || b.descriptor.Kind != b.legacy.Kind ||
		b.descriptor.ReloadOnChange != b.legacy.ReloadOnChange ||
		b.descriptor.Size != b.legacy.Size || !b.legacy.ContentKnown || b.legacy.Size < 0 ||
		b.legacy.Size != int64(len(b.legacy.Content)) || !validFileKind(b.legacy.Kind) {
		return errInvalidSnapshot
	}
	return nil
}

func outputFileBindingFromRecord(record *renderplan.FileRecord) (*outputFileBinding, error) {
	descriptor, err := record.Descriptor()
	if err != nil {
		return nil, err
	}
	document, documentBacked, err := record.ConfigDocument()
	if err != nil {
		return nil, err
	}
	if documentBacked {
		return sealDocumentOutputFileBinding(descriptor, document), nil
	}
	legacy, err := record.LegacyCopy()
	if err != nil {
		return nil, err
	}
	if err := validateChangedFile(-1, &legacy); err != nil {
		return nil, err
	}
	return sealLegacyOutputFileBinding(&legacy), nil
}

func exactOutputFileBinding(left, right *outputFileBinding) (bool, error) {
	if err := left.validate(); err != nil {
		return false, err
	}
	if err := right.validate(); err != nil {
		return false, err
	}
	if left == right {
		return true, nil
	}
	if left.descriptor != right.descriptor || left.documentBacked != right.documentBacked {
		return false, nil
	}
	if !left.documentBacked {
		return left.legacy == right.legacy, nil
	}
	return left.document.SameRoot(right.document)
}

type outputBindingAuthentication struct {
	owner    *outputBinding
	file     *outputFileBinding
	artifact *renderartifact.Artifact
}

type outputBinding struct {
	file     *outputFileBinding
	artifact *renderartifact.Artifact
	seal     *outputBinding
	auth     outputBindingAuthentication
}

func sealOutputBinding(
	file *outputFileBinding,
	artifact *renderartifact.Artifact,
) *outputBinding {
	binding := &outputBinding{file: file, artifact: artifact}
	binding.seal = binding
	binding.auth = outputBindingAuthentication{
		owner: binding, file: file, artifact: artifact,
	}
	return binding
}

func (b *outputBinding) validate() error {
	if b == nil || b.seal != b || b.auth != (outputBindingAuthentication{
		owner: b, file: b.file, artifact: b.artifact,
	}) {
		return errInvalidSnapshot
	}
	if err := b.file.validate(); err != nil {
		return err
	}
	if b.artifact != nil {
		return b.artifact.ValidateAuthentication()
	}
	return nil
}

type outputBindingNodeAuthentication struct {
	owner     *outputBindingNode
	key       string
	binding   *outputBinding
	left      *outputBindingNode
	right     *outputBindingNode
	height    int
	files     int
	artifacts int
}

type outputBindingNode struct {
	key       string
	binding   *outputBinding
	left      *outputBindingNode
	right     *outputBindingNode
	height    int
	files     int
	artifacts int
	seal      *outputBindingNode
	auth      outputBindingNodeAuthentication
}

func newOutputBindingNode(
	key string,
	binding *outputBinding,
	left, right *outputBindingNode,
) *outputBindingNode {
	node := &outputBindingNode{
		key: strings.Clone(key), binding: binding, left: left, right: right,
		height: max(outputBindingNodeHeight(left), outputBindingNodeHeight(right)) + 1,
		files:  outputBindingNodeFiles(left) + outputBindingNodeFiles(right) + 1,
		artifacts: outputBindingNodeArtifacts(left) + outputBindingNodeArtifacts(right) +
			artifactPresence(binding),
	}
	node.seal = node
	node.auth = outputBindingNodeAuthentication{
		owner: node, key: node.key, binding: binding, left: left, right: right,
		height: node.height, files: node.files, artifacts: node.artifacts,
	}
	return node
}

func (n *outputBindingNode) validateShallow() error {
	if n == nil || n.seal != n || n.key == "" || n.binding == nil ||
		n.auth != (outputBindingNodeAuthentication{
			owner: n, key: n.key, binding: n.binding, left: n.left, right: n.right,
			height: n.height, files: n.files, artifacts: n.artifacts,
		}) || n.height < 1 || n.files < 1 || n.artifacts < 0 || n.artifacts > n.files {
		return errInvalidSnapshot
	}
	if n.height != max(outputBindingNodeHeight(n.left), outputBindingNodeHeight(n.right))+1 ||
		n.files != outputBindingNodeFiles(n.left)+outputBindingNodeFiles(n.right)+1 ||
		n.artifacts != outputBindingNodeArtifacts(n.left)+outputBindingNodeArtifacts(n.right)+
			artifactPresence(n.binding) {
		return errInvalidSnapshot
	}
	return n.binding.validate()
}

func outputBindingNodeHeight(node *outputBindingNode) int {
	if node == nil {
		return 0
	}
	return node.height
}

func outputBindingNodeFiles(node *outputBindingNode) int {
	if node == nil {
		return 0
	}
	return node.files
}

func outputBindingNodeArtifacts(node *outputBindingNode) int {
	if node == nil {
		return 0
	}
	return node.artifacts
}

func artifactPresence(binding *outputBinding) int {
	if binding != nil && binding.artifact != nil {
		return 1
	}
	return 0
}

type outputBindingTreeAuthentication struct {
	owner     *outputBindingTree
	root      *outputBindingNode
	files     int
	artifacts int
}

type outputBindingTree struct {
	root      *outputBindingNode
	files     int
	artifacts int
	seal      *outputBindingTree
	auth      outputBindingTreeAuthentication
}

func sealOutputBindingTree(root *outputBindingNode) *outputBindingTree {
	tree := &outputBindingTree{
		root: root, files: outputBindingNodeFiles(root), artifacts: outputBindingNodeArtifacts(root),
	}
	tree.seal = tree
	tree.auth = outputBindingTreeAuthentication{
		owner: tree, root: root, files: tree.files, artifacts: tree.artifacts,
	}
	return tree
}

func (t *outputBindingTree) validate() error {
	if t == nil || t.seal != t || t.auth != (outputBindingTreeAuthentication{
		owner: t, root: t.root, files: t.files, artifacts: t.artifacts,
	}) || t.files < 0 || t.artifacts < 0 || t.artifacts > t.files ||
		t.files != outputBindingNodeFiles(t.root) || t.artifacts != outputBindingNodeArtifacts(t.root) {
		return errInvalidSnapshot
	}
	if t.root != nil {
		return t.root.validateShallow()
	}
	return nil
}

func (t *outputBindingTree) lookup(path string) (*outputBinding, bool, error) {
	if err := t.validate(); err != nil {
		return nil, false, err
	}
	node := t.root
	for node != nil {
		if err := node.validateShallow(); err != nil {
			return nil, false, err
		}
		switch strings.Compare(path, node.key) {
		case -1:
			node = node.left
		case 1:
			node = node.right
		default:
			return node.binding, true, nil
		}
	}
	return nil, false, nil
}

func (t *outputBindingTree) put(path string, binding *outputBinding) (*outputBindingTree, error) {
	if err := t.validate(); err != nil {
		return nil, err
	}
	if path == "" || binding == nil || binding.file == nil || binding.file.descriptor.Path != path {
		return nil, errInvalidSnapshot
	}
	if err := binding.validate(); err != nil {
		return nil, err
	}
	root, err := putOutputBindingNode(t.root, path, binding)
	if err != nil {
		return nil, err
	}
	if root == t.root {
		return t, nil
	}
	return sealOutputBindingTree(root), nil
}

func (t *outputBindingTree) delete(path string) (*outputBindingTree, error) {
	if err := t.validate(); err != nil {
		return nil, err
	}
	root, found, err := deleteOutputBindingNode(t.root, path)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errInvalidOutputDelta
	}
	return sealOutputBindingTree(root), nil
}

func putOutputBindingNode(
	node *outputBindingNode,
	key string,
	binding *outputBinding,
) (*outputBindingNode, error) {
	if node == nil {
		return newOutputBindingNode(key, binding, nil, nil), nil
	}
	if err := node.validateShallow(); err != nil {
		return nil, err
	}
	left, right := node.left, node.right
	switch strings.Compare(key, node.key) {
	case -1:
		var err error
		left, err = putOutputBindingNode(node.left, key, binding)
		if err != nil {
			return nil, err
		}
	case 1:
		var err error
		right, err = putOutputBindingNode(node.right, key, binding)
		if err != nil {
			return nil, err
		}
	default:
		if node.binding == binding {
			return node, nil
		}
		return balanceOutputBindingNode(node.key, binding, left, right), nil
	}
	if left == node.left && right == node.right {
		return node, nil
	}
	return balanceOutputBindingNode(node.key, node.binding, left, right), nil
}

func deleteOutputBindingNode(
	node *outputBindingNode,
	key string,
) (*outputBindingNode, bool, error) {
	if node == nil {
		return nil, false, nil
	}
	if err := node.validateShallow(); err != nil {
		return nil, false, err
	}
	switch strings.Compare(key, node.key) {
	case -1:
		left, found, err := deleteOutputBindingNode(node.left, key)
		if err != nil || !found {
			return node, found, err
		}
		return balanceOutputBindingNode(node.key, node.binding, left, node.right), true, nil
	case 1:
		right, found, err := deleteOutputBindingNode(node.right, key)
		if err != nil || !found {
			return node, found, err
		}
		return balanceOutputBindingNode(node.key, node.binding, node.left, right), true, nil
	default:
		if node.left == nil {
			return node.right, true, nil
		}
		if node.right == nil {
			return node.left, true, nil
		}
		successor, right, err := popFirstOutputBindingNode(node.right)
		if err != nil {
			return nil, false, err
		}
		return balanceOutputBindingNode(
			successor.key, successor.binding, node.left, right,
		), true, nil
	}
}

func popFirstOutputBindingNode(
	node *outputBindingNode,
) (first, remaining *outputBindingNode, err error) {
	if err := node.validateShallow(); err != nil {
		return nil, nil, err
	}
	if node.left == nil {
		return node, node.right, nil
	}
	first, left, err := popFirstOutputBindingNode(node.left)
	if err != nil {
		return nil, nil, err
	}
	return first, balanceOutputBindingNode(node.key, node.binding, left, node.right), nil
}

func balanceOutputBindingNode(
	key string,
	binding *outputBinding,
	left, right *outputBindingNode,
) *outputBindingNode {
	switch {
	case outputBindingNodeHeight(left) > outputBindingNodeHeight(right)+1:
		pivot := left
		if outputBindingNodeHeight(pivot.left) >= outputBindingNodeHeight(pivot.right) {
			newRight := newOutputBindingNode(key, binding, pivot.right, right)
			return newOutputBindingNode(pivot.key, pivot.binding, pivot.left, newRight)
		}
		middle := pivot.right
		newLeft := newOutputBindingNode(pivot.key, pivot.binding, pivot.left, middle.left)
		newRight := newOutputBindingNode(key, binding, middle.right, right)
		return newOutputBindingNode(middle.key, middle.binding, newLeft, newRight)
	case outputBindingNodeHeight(right) > outputBindingNodeHeight(left)+1:
		pivot := right
		if outputBindingNodeHeight(pivot.right) >= outputBindingNodeHeight(pivot.left) {
			newLeft := newOutputBindingNode(key, binding, left, pivot.left)
			return newOutputBindingNode(pivot.key, pivot.binding, newLeft, pivot.right)
		}
		middle := pivot.left
		newLeft := newOutputBindingNode(key, binding, left, middle.left)
		newRight := newOutputBindingNode(pivot.key, pivot.binding, middle.right, pivot.right)
		return newOutputBindingNode(middle.key, middle.binding, newLeft, newRight)
	default:
		return newOutputBindingNode(key, binding, left, right)
	}
}

func buildOutputBindingTree(
	document rendercontent.Document,
	files map[string]*renderplan.File,
	artifacts *renderartifact.Snapshot,
) (*outputBindingTree, error) {
	artifactsByPath := make(map[string]*renderartifact.Artifact, len(files)-1)
	err := artifacts.Walk(func(artifact *renderartifact.Artifact) error {
		descriptor, err := artifact.Descriptor()
		if err != nil {
			return err
		}
		if _, duplicate := artifactsByPath[descriptor.RuntimePath]; duplicate {
			return errInvalidSnapshot
		}
		artifactsByPath[descriptor.RuntimePath] = artifact
		return nil
	})
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	entries := make([]outputBindingTreeEntry, len(paths))
	for index, path := range paths {
		file := files[path]
		artifact := artifactsByPath[path]
		if file.Kind == renderplan.FileKindConfig && artifact != nil ||
			file.Kind != renderplan.FileKindConfig && artifact == nil {
			return nil, errInvalidSnapshot
		}
		delete(artifactsByPath, path)
		var binding *outputFileBinding
		if file.Kind == renderplan.FileKindConfig {
			binding = sealDocumentOutputFileBinding(renderplan.FileDescriptor{
				Path: file.Path, Kind: file.Kind,
				ReloadOnChange: file.ReloadOnChange, Size: file.Size,
			}, document)
		} else {
			binding = sealLegacyOutputFileBinding(file)
		}
		entries[index] = outputBindingTreeEntry{
			path: path, binding: sealOutputBinding(binding, artifact),
		}
	}
	if len(artifactsByPath) != 0 {
		return nil, errInvalidSnapshot
	}
	return sealOutputBindingTree(buildOutputBindingNodes(entries)), nil
}

type outputBindingTreeEntry struct {
	path    string
	binding *outputBinding
}

func buildOutputBindingNodes(entries []outputBindingTreeEntry) *outputBindingNode {
	if len(entries) == 0 {
		return nil
	}
	middle := len(entries) / 2
	return newOutputBindingNode(
		entries[middle].path,
		entries[middle].binding,
		buildOutputBindingNodes(entries[:middle]),
		buildOutputBindingNodes(entries[middle+1:]),
	)
}
