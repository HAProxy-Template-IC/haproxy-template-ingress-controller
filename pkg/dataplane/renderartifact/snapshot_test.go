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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type artifactSpec struct {
	descriptor Descriptor
	content    *Content
}

func TestSnapshotPreservesEveryLegacyFamily(t *testing.T) {
	authority := NewAuthority()
	specs := []artifactSpec{
		{
			descriptor: Descriptor{Family: GeneralCA, Name: "dynamic-ca.pem", Path: "files/dynamic-ca.pem"},
			content:    NewLiteralContent("dynamic ca"),
		},
		{
			descriptor: Descriptor{Family: CRTList, Name: "ignored", Path: "lists/frontends.list"},
			content:    NewLiteralContent("crt list"),
		},
		{
			descriptor: Descriptor{Family: CA, Name: "ignored", Path: "ssl/ca/trust.pem"},
			content:    NewLiteralContent("ca"),
		},
		{
			descriptor: Descriptor{Family: Certificate, Name: "ignored", Path: "ssl/cert.pem"},
			content:    NewLiteralContent("cert"),
		},
		{
			descriptor: Descriptor{Family: General, Name: "error.http", Path: "files/error.http", ReloadOnChange: true},
			content:    NewLiteralContent("error body"),
		},
		{
			descriptor: Descriptor{Family: Map, Name: "ignored", Path: "routes.map"},
			content:    NewLiteralContent("host backend\n"),
		},
	}
	snapshot := buildArtifactSnapshot(t, authority, nil, specs)
	require.NoError(t, snapshot.ValidateAuthentication())
	length, err := snapshot.Len()
	require.NoError(t, err)
	assert.Equal(t, len(specs), length)

	var families []Family
	var names []string
	err = snapshot.Walk(func(artifact *Artifact) error {
		descriptor, descriptorErr := artifact.Descriptor()
		require.NoError(t, descriptorErr)
		families = append(families, descriptor.Family)
		names = append(names, descriptor.Name)
		descriptor.Name = "mutated"
		descriptor.Path = "mutated"
		descriptor.RuntimePath = "mutated"
		assert.Equal(t, "mutated", descriptor.Name)
		assert.Equal(t, "mutated", descriptor.Path)
		assert.Equal(t, "mutated", descriptor.RuntimePath)
		again, againErr := artifact.Descriptor()
		require.NoError(t, againErr)
		assert.NotEqual(t, "mutated", again.Name)
		assert.NotEqual(t, "mutated", again.Path)
		assert.NotEqual(t, "mutated", again.RuntimePath)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, []Family{Map, General, Certificate, CA, CRTList, GeneralCA}, families)
	assert.Equal(t, []string{
		"routes.map", "error.http", "cert.pem", "trust.pem", "frontends.list", "dynamic-ca.pem",
	}, names)
}

func TestBuilderDeduplicatesExactDeclarationsAndRejectsConflicts(t *testing.T) {
	base := Descriptor{Family: General, Name: "error.http", Path: "files/error.http", ReloadOnChange: true}
	tests := []struct {
		name   string
		first  Descriptor
		second Descriptor
		left   string
		right  string
	}{
		{name: "content", first: base, second: base, left: "one", right: "two"},
		{name: "path metadata", first: base, second: Descriptor{Family: General, Name: base.Name, Path: "other/error.http", ReloadOnChange: true}, left: "one", right: "one"},
		{name: "reload metadata", first: base, second: Descriptor{Family: General, Name: base.Name, Path: base.Path}, left: "one", right: "one"},
		{name: "map path identity", first: Descriptor{Family: Map, Path: "same.map"}, second: Descriptor{Family: Map, Name: "different", Path: "same.map"}, left: "one", right: "two"},
		{name: "certificate basename", first: Descriptor{Family: Certificate, Path: "ssl/a/cert.pem"}, second: Descriptor{Family: Certificate, Path: "ssl/b/cert.pem"}, left: "one", right: "one"},
		{name: "CA basename", first: Descriptor{Family: CA, Path: "ssl/a/ca.pem"}, second: Descriptor{Family: CA, Path: "ssl/b/ca.pem"}, left: "one", right: "one"},
		{name: "CRT-list basename", first: Descriptor{Family: CRTList, Path: "lists/a/frontends.list"}, second: Descriptor{Family: CRTList, Path: "lists/b/frontends.list"}, left: "one", right: "one"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder, err := NewBuilder(NewAuthority(), nil)
			require.NoError(t, err)
			require.NoError(t, builder.Add(test.first, NewLiteralContent(test.left)))
			addErr := builder.Add(test.second, NewLiteralContent(test.right))
			require.Error(t, addErr)
			_, buildErr := builder.Build()
			require.ErrorIs(t, buildErr, addErr)
		})
	}

	builder, err := NewBuilder(NewAuthority(), nil)
	require.NoError(t, err)
	require.NoError(t, builder.Add(base, NewLiteralContent("same")))
	require.NoError(t, builder.Add(base, NewLiteralContent("same")))
	snapshot, err := builder.Build()
	require.NoError(t, err)
	length, err := snapshot.Len()
	require.NoError(t, err)
	assert.Equal(t, 1, length)
	require.ErrorIs(t, builder.Add(base, NewLiteralContent("same")), errBuilderSealed)
	again, err := builder.Build()
	require.NoError(t, err)
	assert.Same(t, snapshot, again)
}

func TestBuilderPinsLegacyCrossFamilyCollisions(t *testing.T) {
	conflicts := []struct {
		name   string
		first  Descriptor
		second Descriptor
	}{
		{
			name:   "general and general CA",
			first:  Descriptor{Family: General, Name: "shared.pem", Path: "files/shared.pem"},
			second: Descriptor{Family: GeneralCA, Name: "shared.pem", Path: "files/shared.pem"},
		},
		{
			name:   "general and CRT-list",
			first:  Descriptor{Family: General, Name: "shared.list", Path: "files/shared.list"},
			second: Descriptor{Family: CRTList, Path: "lists/shared.list"},
		},
		{
			name:   "general CA and CRT-list",
			first:  Descriptor{Family: GeneralCA, Name: "shared.list", Path: "files/shared.list"},
			second: Descriptor{Family: CRTList, Path: "lists/shared.list"},
		},
	}
	for _, test := range conflicts {
		t.Run(test.name, func(t *testing.T) {
			builder, err := NewBuilder(NewAuthority(), nil)
			require.NoError(t, err)
			require.NoError(t, builder.Add(test.first, NewLiteralContent("same")))
			require.ErrorContains(t, builder.Add(test.second, NewLiteralContent("same")), "shared general storage")
			_, err = builder.Build()
			require.Error(t, err)
		})
	}

	allowed := []Descriptor{
		{Family: Map, Path: "shared.pem"},
		{Family: General, Name: "shared.pem", Path: "files/shared.pem"},
		{Family: Certificate, Path: "ssl/shared.pem"},
		{Family: CA, Path: "ca/shared.pem"},
	}
	snapshot := buildArtifactSnapshot(t, NewAuthority(), nil, makeSpecs(allowed, "same"))
	length, err := snapshot.Len()
	require.NoError(t, err)
	assert.Equal(t, len(allowed), length)
}

func TestBuilderNormalizesFixedReloadFamilies(t *testing.T) {
	tests := []Descriptor{
		{Family: Map, Path: "file.map"},
		{Family: Certificate, Path: "ssl/cert.pem"},
		{Family: CA, Path: "ssl/ca.pem"},
		{Family: CRTList, Path: "lists/frontends.list"},
		{Family: GeneralCA, Name: "dynamic-ca.pem", Path: "files/dynamic-ca.pem"},
	}
	for _, descriptor := range tests {
		t.Run(fmt.Sprintf("family-%d", descriptor.Family), func(t *testing.T) {
			authority := NewAuthority()
			base := buildArtifactSnapshot(t, authority, nil, []artifactSpec{{descriptor, NewLiteralContent("same")}})
			descriptor.ReloadOnChange = true
			unchanged := buildArtifactSnapshot(t, authority, base, []artifactSpec{{descriptor, NewLiteralContent("same")}})
			assert.Same(t, base, unchanged)
			artifact := artifactPointers(t, unchanged)
			for _, item := range artifact {
				actual, err := item.Descriptor()
				require.NoError(t, err)
				assert.False(t, actual.ReloadOnChange)
			}
		})
	}

	authority := NewAuthority()
	general := Descriptor{Family: General, Name: "file", Path: "files/file"}
	base := buildArtifactSnapshot(t, authority, nil, []artifactSpec{{general, NewLiteralContent("same")}})
	general.ReloadOnChange = true
	changed := buildArtifactSnapshot(t, authority, base, []artifactSpec{{general, NewLiteralContent("same")}})
	assert.NotSame(t, base, changed)
	equal, err := base.ExactEqual(changed)
	require.NoError(t, err)
	assert.False(t, equal)
}

func TestRuntimePathIsExactMetadataAndDefaultsToPath(t *testing.T) {
	authority := NewAuthority()
	baseDescriptor := Descriptor{Family: Map, Path: "maps/routes.map"}
	base := buildArtifactSnapshot(t, authority, nil, []artifactSpec{{
		descriptor: baseDescriptor,
		content:    NewLiteralContent("safe"),
	}})
	artifacts := artifactPointers(t, base)
	descriptor, err := artifacts["maps/routes.map"].Descriptor()
	require.NoError(t, err)
	assert.Equal(t, descriptor.Path, descriptor.RuntimePath)

	explicitDefault := baseDescriptor
	explicitDefault.RuntimePath = explicitDefault.Path
	exact := buildArtifactSnapshot(t, authority, base, []artifactSpec{{
		descriptor: explicitDefault,
		content:    NewLiteralContent("safe"),
	}})
	assert.Same(t, base, exact)

	changedDescriptor := baseDescriptor
	changedDescriptor.RuntimePath = "runtime/routes.map"
	changed := buildArtifactSnapshot(t, authority, base, []artifactSpec{{
		descriptor: changedDescriptor,
		content:    NewLiteralContent("safe"),
	}})
	assert.NotSame(t, base, changed)
	equal, err := base.ExactEqual(changed)
	require.NoError(t, err)
	assert.False(t, equal)

	changedArtifact := artifactPointers(t, changed)["maps/routes.map"]
	changedValue, err := changedArtifact.Descriptor()
	require.NoError(t, err)
	assert.Equal(t, "runtime/routes.map", changedValue.RuntimePath)

	builder, err := NewBuilder(NewAuthority(), nil)
	require.NoError(t, err)
	require.NoError(t, builder.Add(baseDescriptor, NewLiteralContent("safe")))
	require.Error(t, builder.Add(changedDescriptor, NewLiteralContent("safe")))
}

func TestBuilderReusesExactSnapshotAndUnchangedArtifacts(t *testing.T) {
	authority := NewAuthority()
	descriptors := make([]Descriptor, 7)
	for index := range descriptors {
		descriptors[index] = Descriptor{Family: Map, Path: string(rune('a'+index)) + ".map"}
	}
	base := buildArtifactSnapshot(t, authority, nil, makeSpecs(descriptors, "base"))
	reversed := slices.Clone(descriptors)
	slices.Reverse(reversed)
	exact := buildArtifactSnapshot(t, authority, base, makeSpecs(reversed, "base"))
	assert.Same(t, base, exact)
	same, err := base.SameRoot(exact)
	require.NoError(t, err)
	assert.True(t, same)

	changedSpecs := makeSpecs(descriptors, "base")
	changedSpecs[0].content = NewLiteralContent("changed")
	changed := buildArtifactSnapshot(t, authority, base, changedSpecs)
	assert.NotSame(t, base, changed)
	same, err = base.SameRoot(changed)
	require.NoError(t, err)
	assert.False(t, same)
	assert.Same(t, base.root.right, changed.root.right)
	assert.NotSame(t, base.root.left.left, changed.root.left.left)

	baseArtifacts := artifactPointers(t, base)
	changedArtifacts := artifactPointers(t, changed)
	for key, artifact := range baseArtifacts {
		if key == "a.map" {
			assert.NotSame(t, artifact, changedArtifacts[key])
			continue
		}
		assert.Same(t, artifact, changedArtifacts[key])
	}

	empty := buildArtifactSnapshot(t, authority, nil, nil)
	emptyAgain := buildArtifactSnapshot(t, authority, empty, nil)
	assert.Same(t, empty, emptyAgain)

	removed := buildArtifactSnapshot(t, authority, base, makeSpecs(descriptors[:len(descriptors)-1], "base"))
	assert.NotSame(t, base, removed)
	removedLength, err := removed.Len()
	require.NoError(t, err)
	assert.Equal(t, len(descriptors)-1, removedLength)
	addedDescriptors := append(slices.Clone(descriptors), Descriptor{Family: Map, Path: "h.map"})
	added := buildArtifactSnapshot(t, authority, base, makeSpecs(addedDescriptors, "base"))
	assert.NotSame(t, base, added)
	addedLength, err := added.Len()
	require.NoError(t, err)
	assert.Equal(t, len(descriptors)+1, addedLength)
}

func TestSnapshotExactEqualUsesExactForeignBytes(t *testing.T) {
	descriptor := Descriptor{Family: General, Name: "file", Path: "files/file", ReloadOnChange: true}
	left := buildArtifactSnapshot(t, NewAuthority(), nil, []artifactSpec{{descriptor, NewLiteralContent("safe")}})
	right := buildArtifactSnapshot(t, NewAuthority(), nil, []artifactSpec{{descriptor, NewLiteralContent("safe")}})
	same, err := left.SameRoot(right)
	require.NoError(t, err)
	assert.False(t, same)
	equal, err := left.ExactEqual(right)
	require.NoError(t, err)
	assert.True(t, equal)

	document := buildTestDocument(t, "safe")
	direct, err := NewDocumentContent(document, "safe", true)
	require.NoError(t, err)
	directSnapshot := buildArtifactSnapshot(t, NewAuthority(), nil, []artifactSpec{{descriptor, direct}})
	equal, err = left.ExactEqual(directSnapshot)
	require.NoError(t, err)
	assert.True(t, equal)

	changed := buildArtifactSnapshot(t, NewAuthority(), nil, []artifactSpec{{descriptor, NewLiteralContent("evil")}})
	changed.root.artifact.content.digest = left.root.artifact.content.digest
	changed.root.artifact.content.auth.digest = left.root.artifact.content.digest
	require.NoError(t, changed.ValidateAuthentication())
	equal, err = left.ExactEqual(changed)
	require.NoError(t, err)
	assert.False(t, equal)

	metadata := descriptor
	metadata.Path = "files/other"
	metadataSnapshot := buildArtifactSnapshot(t, NewAuthority(), nil, []artifactSpec{{metadata, NewLiteralContent("safe")}})
	equal, err = left.ExactEqual(metadataSnapshot)
	require.NoError(t, err)
	assert.False(t, equal)

	differentFamily := buildArtifactSnapshot(t, NewAuthority(), nil, []artifactSpec{{
		Descriptor{Family: GeneralCA, Name: descriptor.Name, Path: descriptor.Path, ReloadOnChange: true},
		NewLiteralContent("safe"),
	}})
	equal, err = left.ExactEqual(differentFamily)
	require.NoError(t, err)
	assert.False(t, equal)

	emptyLeft := buildArtifactSnapshot(t, NewAuthority(), nil, nil)
	emptyRight := buildArtifactSnapshot(t, NewAuthority(), nil, nil)
	equal, err = emptyLeft.ExactEqual(emptyRight)
	require.NoError(t, err)
	assert.True(t, equal)
}

func TestAuthoritiesAndSnapshotsRejectPoisonedValues(t *testing.T) {
	authority := NewAuthority()
	require.NoError(t, authority.ValidateAuthentication())
	copyAuthority := *authority
	require.ErrorIs(t, copyAuthority.ValidateAuthentication(), errInvalidAuthority)
	var zeroAuthority Authority
	require.ErrorIs(t, zeroAuthority.ValidateAuthentication(), errInvalidAuthority)
	_, err := NewBuilder(nil, nil)
	require.ErrorIs(t, err, errInvalidAuthority)

	snapshot := buildArtifactSnapshot(t, authority, nil, makeSpecs([]Descriptor{{Family: Map, Path: "a.map"}}, "safe"))
	artifact := snapshot.root.artifact
	shallowArtifact := *artifact
	require.ErrorIs(t, shallowArtifact.ValidateAuthentication(), errInvalidArtifact)
	poisonedArtifact := *artifact
	poisonedArtifact.content = NewLiteralContent("evil")
	require.ErrorIs(t, poisonedArtifact.ValidateAuthentication(), errInvalidArtifact)
	poisonedArtifact = *artifact
	poisonedArtifact.descriptor = mustDescriptor(t, Descriptor{Family: Map, Path: "evil.map"})
	require.ErrorIs(t, poisonedArtifact.ValidateAuthentication(), errInvalidArtifact)

	shallowSnapshot := *snapshot
	require.ErrorIs(t, shallowSnapshot.ValidateAuthentication(), errInvalidSnapshot)
	poisonedSnapshot := *snapshot
	poisonedSnapshot.root = nil
	require.ErrorIs(t, poisonedSnapshot.ValidateAuthentication(), errInvalidSnapshot)
	poisonedSnapshot = *snapshot
	poisonedSnapshot.artifacts++
	require.ErrorIs(t, poisonedSnapshot.ValidateAuthentication(), errInvalidSnapshot)
	poisonedSnapshot = *snapshot
	poisonedSnapshot.authority = NewAuthority()
	require.ErrorIs(t, poisonedSnapshot.ValidateAuthentication(), errInvalidSnapshot)
	var zeroSnapshot Snapshot
	require.ErrorIs(t, zeroSnapshot.ValidateAuthentication(), errInvalidSnapshot)
	_, err = (*Snapshot)(nil).Len()
	require.ErrorIs(t, err, errInvalidSnapshot)
	_, err = snapshot.SameRoot(nil)
	require.ErrorIs(t, err, errInvalidSnapshot)
	require.ErrorIs(t, snapshot.Walk(nil), errNilVisitor)

	foreign := buildArtifactSnapshot(t, NewAuthority(), nil, nil)
	_, err = NewBuilder(authority, foreign)
	require.ErrorIs(t, err, errForeignSnapshot)
	foreignCopy := *snapshot
	_, err = NewBuilder(authority, &foreignCopy)
	require.ErrorIs(t, err, errInvalidSnapshot)
}

func TestAuthorityValidatesOwnedSnapshots(t *testing.T) {
	authority := NewAuthority()
	snapshot := buildArtifactSnapshot(t, authority, nil, makeSpecs([]Descriptor{{Family: Map, Path: "a.map"}}, "safe"))
	require.NoError(t, authority.ValidateSnapshot(snapshot))
	require.ErrorIs(t, authority.ValidateSnapshot(nil), errInvalidSnapshot)
	require.ErrorIs(t, NewAuthority().ValidateSnapshot(snapshot), errForeignSnapshot)
	require.ErrorIs(t, (*Authority)(nil).ValidateSnapshot(snapshot), errInvalidAuthority)
}

func TestDescriptorAuthenticationRejectsRuntimePathPoison(t *testing.T) {
	snapshot := buildArtifactSnapshot(
		t, NewAuthority(), nil, makeSpecs([]Descriptor{{Family: Map, Path: "a.map"}}, "safe"),
	)
	artifact := snapshot.root.artifact
	originalDescriptor := artifact.descriptor.value
	artifact.descriptor.value.RuntimePath = "evil.map"
	require.ErrorIs(t, artifact.ValidateAuthentication(), errInvalidArtifact)
	artifact.descriptor.value = originalDescriptor
	require.NoError(t, artifact.ValidateAuthentication())
}

func TestSnapshotTraversalAndBuilderFailClosedOnDeepPoison(t *testing.T) {
	authority := NewAuthority()
	descriptors := make([]Descriptor, 7)
	for index := range descriptors {
		descriptors[index] = Descriptor{Family: Map, Path: fmt.Sprintf("%d.map", index)}
	}
	snapshot := buildArtifactSnapshot(t, authority, nil, makeSpecs(descriptors, "safe"))
	poisoned := snapshot.root.right.left
	require.NotNil(t, poisoned)
	originalSeal := poisoned.seal
	poisoned.seal = nil
	require.NoError(t, snapshot.ValidateAuthentication())
	require.ErrorIs(t, snapshot.Walk(func(*Artifact) error { return nil }), errInvalidSnapshot)
	_, err := snapshot.ExactEqual(snapshot)
	require.NoError(t, err)

	poisoned.seal = originalSeal
	builder, err := NewBuilder(authority, snapshot)
	require.NoError(t, err)
	for _, spec := range makeSpecs(descriptors, "safe") {
		require.NoError(t, builder.Add(spec.descriptor, spec.content))
	}
	poisoned.seal = nil
	_, err = builder.Build()
	require.ErrorIs(t, err, errInvalidSnapshot)
	poisoned.seal = originalSeal
	require.NoError(t, snapshot.ValidateAuthentication())
}

func TestBuilderRejectsInvalidInputsAndKeepsFirstFailure(t *testing.T) {
	builder, err := NewBuilder(NewAuthority(), nil)
	require.NoError(t, err)
	require.ErrorIs(t, builder.Add(Descriptor{Family: Map, Path: "map"}, nil), errNilContent)
	require.ErrorIs(t, builder.Add(Descriptor{Family: Map, Path: "other"}, NewLiteralContent("safe")), errNilContent)
	_, err = builder.Build()
	require.ErrorIs(t, err, errNilContent)

	builder, err = NewBuilder(NewAuthority(), nil)
	require.NoError(t, err)
	invalid := NewLiteralContent("safe")
	invalid.seal = nil
	require.ErrorIs(t, builder.Add(Descriptor{Family: Map, Path: "map"}, invalid), errInvalidContent)
	_, err = builder.Build()
	require.ErrorIs(t, err, errInvalidContent)

	builder, err = NewBuilder(NewAuthority(), nil)
	require.NoError(t, err)
	require.ErrorIs(t, builder.Add(Descriptor{}, NewLiteralContent("safe")), errInvalidFamily)
	_, err = builder.Build()
	require.ErrorIs(t, err, errInvalidFamily)

	_, err = (*Builder)(nil).Build()
	require.ErrorIs(t, err, errInvalidAuthority)
	require.ErrorIs(t, (*Builder)(nil).Add(Descriptor{}, nil), errInvalidAuthority)
}

func TestSnapshotWalkReturnsVisitorFailure(t *testing.T) {
	snapshot := buildArtifactSnapshot(t, NewAuthority(), nil, makeSpecs([]Descriptor{{Family: Map, Path: "a"}}, "safe"))
	sentinel := errors.New("stop")
	err := snapshot.Walk(func(*Artifact) error { return sentinel })
	require.ErrorIs(t, err, sentinel)
}

func buildArtifactSnapshot(t *testing.T, authority *Authority, previous *Snapshot, specs []artifactSpec) *Snapshot {
	t.Helper()
	builder, err := NewBuilder(authority, previous)
	require.NoError(t, err)
	for _, spec := range specs {
		require.NoError(t, builder.Add(spec.descriptor, spec.content))
	}
	snapshot, err := builder.Build()
	require.NoError(t, err)
	return snapshot
}

func makeSpecs(descriptors []Descriptor, content string) []artifactSpec {
	specs := make([]artifactSpec, len(descriptors))
	for index := range descriptors {
		specs[index] = artifactSpec{descriptor: descriptors[index], content: NewLiteralContent(content)}
	}
	return specs
}

func artifactPointers(t *testing.T, snapshot *Snapshot) map[string]*Artifact {
	t.Helper()
	artifacts := make(map[string]*Artifact)
	require.NoError(t, snapshot.Walk(func(artifact *Artifact) error {
		descriptor, err := artifact.Descriptor()
		require.NoError(t, err)
		artifacts[descriptor.Name] = artifact
		return nil
	}))
	return artifacts
}

func mustDescriptor(t *testing.T, descriptor Descriptor) *descriptorData {
	t.Helper()
	data, err := normalizeDescriptor(descriptor)
	require.NoError(t, err)
	return data
}
