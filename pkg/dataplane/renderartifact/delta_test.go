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
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArtifactTransactionAppliesAuthenticatedChanges(t *testing.T) {
	authority := NewAuthority()
	base := artifactDeltaFixture(t, authority, map[string]string{
		"a.map": "a value\n",
		"b.map": "b value\n",
		"c.map": "c value\n",
	})
	replaced := requireArtifactHandle(t, base, Descriptor{Family: Map, Path: "b.map"})
	deleted := requireArtifactHandle(t, base, Descriptor{Family: Map, Path: "c.map"})
	transaction, err := BeginTransaction(authority, base)
	require.NoError(t, err)
	require.NoError(t, transaction.Replace(
		replaced,
		Descriptor{Family: Map, Path: "b.map"},
		NewLiteralContent("b changed\n"),
	))
	require.NoError(t, transaction.Delete(deleted))
	require.NoError(t, transaction.Insert(
		Descriptor{Family: Map, Path: "d.map"},
		NewLiteralContent("d value\n"),
	))

	next, delta, err := transaction.Commit()
	require.NoError(t, err)
	require.NoError(t, next.ValidateAuthentication())
	require.NoError(t, delta.ValidateAuthentication())
	structural, err := delta.RequiresFullValidation()
	require.NoError(t, err)
	assert.True(t, structural)
	assert.Equal(t, map[string]string{
		"a.map": "a value\n",
		"b.map": "b changed\n",
		"d.map": "d value\n",
	}, artifactDeltaContents(t, next))

	changes, err := delta.Changes()
	require.NoError(t, err)
	assert.Len(t, changes, 3)
	applied, err := delta.Apply(base)
	require.NoError(t, err)
	same, err := applied.SameRoot(next)
	require.NoError(t, err)
	assert.True(t, same)

	again, againDelta, err := transaction.Commit()
	require.NoError(t, err)
	assert.Same(t, next, again)
	assert.Same(t, delta, againDelta)
}

func TestArtifactTransactionNoOpReusesExactRoot(t *testing.T) {
	authority := NewAuthority()
	content := NewLiteralContent("value\n")
	builder, err := NewBuilder(authority, nil)
	require.NoError(t, err)
	descriptor := Descriptor{Family: Map, Path: "a.map"}
	require.NoError(t, builder.Add(descriptor, content))
	base, err := builder.Build()
	require.NoError(t, err)
	handle := requireArtifactHandle(t, base, descriptor)
	transaction, err := BeginTransaction(authority, base)
	require.NoError(t, err)
	require.NoError(t, transaction.Replace(handle, descriptor, content))
	next, delta, err := transaction.Commit()
	require.NoError(t, err)
	assert.Same(t, base, next)
	unchanged, err := delta.SameRoot()
	require.NoError(t, err)
	assert.True(t, unchanged)
	changes, err := delta.Changes()
	require.NoError(t, err)
	assert.Empty(t, changes)
	structural, err := delta.RequiresFullValidation()
	require.NoError(t, err)
	assert.False(t, structural)
}

func TestArtifactTransactionMarksDescriptorReplacementStructural(t *testing.T) {
	authority := NewAuthority()
	descriptor := Descriptor{Family: General, Name: "routes.lst", Path: "files/routes.lst"}
	builder, err := NewBuilder(authority, nil)
	require.NoError(t, err)
	require.NoError(t, builder.Add(descriptor, NewLiteralContent("value\n")))
	base, err := builder.Build()
	require.NoError(t, err)
	handle := requireArtifactHandle(t, base, descriptor)
	changed := descriptor
	changed.ReloadOnChange = true
	transaction, err := BeginTransaction(authority, base)
	require.NoError(t, err)
	require.NoError(t, transaction.Replace(handle, changed, NewLiteralContent("changed\n")))
	_, delta, err := transaction.Commit()
	require.NoError(t, err)
	structural, err := delta.RequiresFullValidation()
	require.NoError(t, err)
	assert.True(t, structural)
}

func TestArtifactTransactionRejectsForeignCopiedStaleAndConflictingProofs(t *testing.T) {
	authority := NewAuthority()
	base := artifactDeltaFixture(t, authority, map[string]string{"a.map": "a\n"})
	handle := requireArtifactHandle(t, base, Descriptor{Family: Map, Path: "a.map"})
	foreignAuthority := NewAuthority()
	foreign := artifactDeltaFixture(t, foreignAuthority, map[string]string{"a.map": "a\n"})
	foreignTransaction, err := BeginTransaction(foreignAuthority, foreign)
	require.NoError(t, err)
	require.ErrorIs(t, foreignTransaction.Delete(handle), errInvalidArtifactHandle)
	_, _, err = foreignTransaction.Commit()
	require.ErrorIs(t, err, errInvalidArtifactHandle)

	copiedHandle := *handle
	transaction, err := BeginTransaction(authority, base)
	require.NoError(t, err)
	require.ErrorIs(t, transaction.Delete(&copiedHandle), errInvalidArtifactHandle)

	transaction, err = BeginTransaction(authority, base)
	require.NoError(t, err)
	require.NoError(t, transaction.Delete(handle))
	require.ErrorIs(t, transaction.Delete(handle), errArtifactChangeConflict)
	_, _, err = transaction.Commit()
	require.ErrorIs(t, err, errArtifactChangeConflict)

	transaction, err = BeginTransaction(authority, base)
	require.NoError(t, err)
	require.NoError(t, transaction.Replace(
		handle,
		Descriptor{Family: Map, Path: "a.map"},
		NewLiteralContent("changed\n"),
	))
	next, delta, err := transaction.Commit()
	require.NoError(t, err)
	_, err = delta.Apply(next)
	require.ErrorIs(t, err, errInvalidArtifactDelta)

	copiedDelta := *delta
	_, err = copiedDelta.Apply(base)
	require.ErrorIs(t, err, errInvalidArtifactDelta)
	delta.structural = true
	require.ErrorIs(t, delta.ValidateAuthentication(), errInvalidArtifactDelta)
	delta.structural = false
	require.NoError(t, delta.ValidateAuthentication())

	copiedTransaction := *mustArtifactTransaction(t, authority, base)
	_, _, err = copiedTransaction.Commit()
	require.ErrorIs(t, err, errInvalidArtifactTransaction)
}

func TestArtifactTransactionRejectsConflictWithUnchangedSharedStorage(t *testing.T) {
	authority := NewAuthority()
	builder, err := NewBuilder(authority, nil)
	require.NoError(t, err)
	require.NoError(t, builder.Add(
		Descriptor{Family: General, Name: "shared.pem", Path: "files/shared.pem"},
		NewLiteralContent("general\n"),
	))
	base, err := builder.Build()
	require.NoError(t, err)
	transaction, err := BeginTransaction(authority, base)
	require.NoError(t, err)
	require.NoError(t, transaction.Insert(
		Descriptor{Family: GeneralCA, Name: "shared.pem", Path: "files/shared.pem"},
		NewLiteralContent("ca\n"),
	))
	_, _, err = transaction.Commit()
	require.ErrorContains(t, err, "shared general storage")
	assert.Equal(t, map[string]string{"files/shared.pem": "general\n"}, artifactDeltaContents(t, base))
}

func TestArtifactTransactionSiblingCommitsAreIndependent(t *testing.T) {
	authority := NewAuthority()
	base := artifactDeltaFixture(t, authority, map[string]string{
		"a.map": "a\n",
		"b.map": "b\n",
	})
	descriptors := []Descriptor{
		{Family: Map, Path: "a.map"},
		{Family: Map, Path: "b.map"},
	}
	results := make([]*Snapshot, len(descriptors))
	errors := make([]error, len(descriptors))
	var group sync.WaitGroup
	for index := range descriptors {
		group.Add(1)
		go func() {
			defer group.Done()
			handle, found, err := base.Lookup(descriptors[index])
			if err == nil && !found {
				err = errArtifactNotFound
			}
			var transaction *Transaction
			if err == nil {
				transaction, err = BeginTransaction(authority, base)
			}
			if err == nil {
				err = transaction.Replace(
					handle,
					descriptors[index],
					NewLiteralContent(fmt.Sprintf("changed-%d\n", index)),
				)
			}
			if err == nil {
				results[index], _, err = transaction.Commit()
			}
			errors[index] = err
		}()
	}
	group.Wait()
	for _, err := range errors {
		require.NoError(t, err)
	}
	assert.Equal(t, "changed-0\n", artifactDeltaContents(t, results[0])["a.map"])
	assert.Equal(t, "b\n", artifactDeltaContents(t, results[0])["b.map"])
	assert.Equal(t, "a\n", artifactDeltaContents(t, results[1])["a.map"])
	assert.Equal(t, "changed-1\n", artifactDeltaContents(t, results[1])["b.map"])
}

func BenchmarkArtifactTransactionReplaceOneOf3000(b *testing.B) {
	authority := NewAuthority()
	values := make(map[string]string, 3000)
	for index := range 3000 {
		path := fmt.Sprintf("map-%06d.map", index)
		values[path] = fmt.Sprintf("route-%06d backend\n", index)
	}
	base := artifactDeltaFixture(b, authority, values)
	descriptor := Descriptor{Family: Map, Path: "map-001500.map"}
	handle := requireArtifactHandle(b, base, descriptor)
	changed := NewLiteralContent("changed backend\n")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		transaction, err := BeginTransaction(authority, base)
		if err != nil {
			b.Fatal(err)
		}
		if err := transaction.Replace(handle, descriptor, changed); err != nil {
			b.Fatal(err)
		}
		benchmarkSnapshotSink, _, err = transaction.Commit()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func artifactDeltaFixture(
	tb testing.TB,
	authority *Authority,
	values map[string]string,
) *Snapshot {
	tb.Helper()
	builder, err := NewBuilder(authority, nil)
	require.NoError(tb, err)
	for path, value := range values {
		require.NoError(tb, builder.Add(
			Descriptor{Family: Map, Path: path},
			NewLiteralContent(value),
		))
	}
	snapshot, err := builder.Build()
	require.NoError(tb, err)
	return snapshot
}

func requireArtifactHandle(
	tb testing.TB,
	snapshot *Snapshot,
	descriptor Descriptor,
) *Handle {
	tb.Helper()
	handle, found, err := snapshot.Lookup(descriptor)
	require.NoError(tb, err)
	require.True(tb, found)
	return handle
}

func mustArtifactTransaction(
	tb testing.TB,
	authority *Authority,
	base *Snapshot,
) *Transaction {
	tb.Helper()
	transaction, err := BeginTransaction(authority, base)
	require.NoError(tb, err)
	return transaction
}

func artifactDeltaContents(tb testing.TB, snapshot *Snapshot) map[string]string {
	tb.Helper()
	values := make(map[string]string)
	require.NoError(tb, snapshot.Walk(func(artifact *Artifact) error {
		descriptor, err := artifact.Descriptor()
		if err != nil {
			return err
		}
		content, err := artifact.Content()
		if err != nil {
			return err
		}
		value, err := content.String()
		if err != nil {
			return err
		}
		values[descriptor.RuntimePath] = value
		return nil
	}))
	return values
}
