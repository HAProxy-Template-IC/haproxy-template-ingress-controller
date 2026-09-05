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
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

func TestNewSnapshotFromDocumentStreamsAndPreservesLegacyBytes(t *testing.T) {
	fixture := newOutputFixture(t)
	document := segmentedOutputDocument(t, fixture.config, 11)

	snapshot, err := NewSnapshotFromDocument(
		fixture.authority, document, fixture.plan, fixture.artifacts, nil,
	)
	require.NoError(t, err)
	require.NoError(t, snapshot.ValidateAuthentication())
	assert.Empty(t, snapshot.root.config.memo.value)

	stored, err := snapshot.ConfigDocument()
	require.NoError(t, err)
	same, err := document.SameRoot(stored)
	require.NoError(t, err)
	assert.True(t, same)

	checksum, err := snapshot.ContentChecksum()
	require.NoError(t, err)
	wantChecksum, err := dataplane.ComputeSnapshotContentChecksum(fixture.config, fixture.artifacts)
	require.NoError(t, err)
	assert.Equal(t, wantChecksum, checksum)

	config, err := snapshot.Config()
	require.NoError(t, err)
	assert.Equal(t, fixture.config, config)
	assert.Equal(t, fixture.config, snapshot.root.config.memo.value)

	legacy := mustOutputSnapshot(
		t, fixture.authority, fixture.config, fixture.plan.Clone(), fixture.artifacts, nil,
	)
	equal, err := snapshot.ExactEqual(legacy)
	require.NoError(t, err)
	assert.True(t, equal)
}

func TestDocumentSnapshotReusesEqualBytesOnlyAfterValidation(t *testing.T) {
	fixture := newOutputFixture(t)
	document := segmentedOutputDocument(t, fixture.config, 7)
	previous, err := NewSnapshotFromDocument(
		fixture.authority, document, fixture.plan, fixture.artifacts, nil,
	)
	require.NoError(t, err)
	freshArtifacts := buildArtifactSnapshot(t, fixture.artifactAuthority, nil, fixture.specs)

	reused, err := NewSnapshotFromDocument(
		fixture.authority, document, fixture.plan.Clone(), freshArtifacts, previous,
	)
	require.NoError(t, err)
	assert.Same(t, previous, reused)

	equalBytes := segmentedOutputDocument(t, fixture.config, 7)
	same, err := document.SameRoot(equalBytes)
	require.NoError(t, err)
	assert.False(t, same)

	validated, err := NewSnapshotFromDocument(
		fixture.authority, equalBytes, fixture.plan.Clone(), freshArtifacts, previous,
	)
	require.NoError(t, err)
	assert.Same(t, previous, validated)
	assert.Empty(t, previous.root.config.memo.value)
}

func TestDocumentSnapshotExactEqualDoesNotMaterialize(t *testing.T) {
	leftFixture := newOutputFixture(t)
	left, err := NewSnapshotFromDocument(
		leftFixture.authority,
		segmentedOutputDocument(t, leftFixture.config, 7),
		leftFixture.plan,
		leftFixture.artifacts,
		nil,
	)
	require.NoError(t, err)
	rightFixture := newOutputFixture(t)
	right, err := NewSnapshotFromDocument(
		rightFixture.authority,
		segmentedOutputDocument(t, rightFixture.config, 13),
		rightFixture.plan,
		rightFixture.artifacts,
		nil,
	)
	require.NoError(t, err)
	assert.Empty(t, left.root.config.memo.value)
	assert.Empty(t, right.root.config.memo.value)

	equal, err := left.ExactEqual(right)
	require.NoError(t, err)
	assert.True(t, equal)
	assert.Empty(t, left.root.config.memo.value)
	assert.Empty(t, right.root.config.memo.value)
}

func TestReusePreviousDocumentRequiresEveryExactRoot(t *testing.T) {
	fixture := newOutputFixture(t)
	document := segmentedOutputDocument(t, fixture.config, 9)
	previous, err := NewSnapshotFromDocument(
		fixture.authority, document, fixture.plan, fixture.artifacts, nil,
	)
	require.NoError(t, err)
	plan, err := previous.PlanSnapshot()
	require.NoError(t, err)

	reused, hit, err := ReusePreviousDocument(
		fixture.authority, previous, document, plan, fixture.artifacts,
	)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Same(t, previous, reused)

	copyDocument := document
	reused, hit, err = ReusePreviousDocument(
		fixture.authority, previous, copyDocument, plan, fixture.artifacts,
	)
	require.NoError(t, err)
	assert.True(t, hit)
	assert.Same(t, previous, reused)

	for _, changed := range []rendercontent.Document{
		segmentedOutputDocument(t, fixture.config, 9),
		segmentedOutputDocument(t, "x"+fixture.config[1:], 9),
	} {
		reused, hit, err = ReusePreviousDocument(
			fixture.authority, previous, changed, plan, fixture.artifacts,
		)
		require.NoError(t, err)
		assert.False(t, hit)
		assert.Nil(t, reused)
	}

	_, _, err = ReusePreviousDocument(
		fixture.authority, previous, rendercontent.Document{}, plan, fixture.artifacts,
	)
	require.Error(t, err)
	foreign := newOutputFixture(t)
	_, _, err = ReusePreviousDocument(
		fixture.authority, foreignSnapshot(t, &foreign), document, plan, fixture.artifacts,
	)
	require.Error(t, err)
}

func TestNewSnapshotFromDocumentRejectsChangedAndUnauthenticatedDocuments(t *testing.T) {
	fixture := newOutputFixture(t)
	changed := segmentedOutputDocument(t, "x"+fixture.config[1:], 5)
	_, err := NewSnapshotFromDocument(
		fixture.authority, changed, fixture.plan, fixture.artifacts, nil,
	)
	require.Error(t, err)

	_, err = NewSnapshotFromDocument(
		fixture.authority, rendercontent.Document{}, fixture.plan, fixture.artifacts, nil,
	)
	require.Error(t, err)

	document := segmentedOutputDocument(t, fixture.config, 5)
	snapshot, err := NewSnapshotFromDocument(
		fixture.authority, document, fixture.plan, fixture.artifacts, nil,
	)
	require.NoError(t, err)
	assert.Empty(t, snapshot.root.config.memo.value)

	snapshot.root.config.document = changed
	_, err = snapshot.Config()
	require.ErrorIs(t, err, errInvalidSnapshot)
	assert.Empty(t, snapshot.root.config.memo.value)
	snapshot.root.config.document = document
	config, err := snapshot.Config()
	require.NoError(t, err)
	assert.Equal(t, fixture.config, config)
}

func TestDocumentSnapshotLazyConfigIsConcurrent(t *testing.T) {
	fixture := newScaleOutputFixture(t, 64)
	document := segmentedOutputDocument(t, fixture.config, 2)
	snapshot, err := NewSnapshotFromDocument(
		fixture.authority, document, fixture.plan, fixture.artifacts, nil,
	)
	require.NoError(t, err)
	assert.Empty(t, snapshot.root.config.memo.value)

	const readers = 64
	start := make(chan struct{})
	errorsChannel := make(chan error, readers)
	var wait sync.WaitGroup
	for range readers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			if readErr := validateLazyConfigReuse(&fixture, document, snapshot); readErr != nil {
				errorsChannel <- readErr
			}
		}()
	}
	close(start)
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		require.NoError(t, err)
	}
	assert.Equal(t, fixture.config, snapshot.root.config.memo.value)
}

func validateLazyConfigReuse(
	fixture *outputFixture,
	document rendercontent.Document,
	snapshot *Snapshot,
) error {
	for range 20 {
		built, err := NewSnapshotFromDocument(
			fixture.authority, document, fixture.plan, fixture.artifacts, snapshot,
		)
		if err != nil {
			return err
		}
		if built != snapshot {
			return fmt.Errorf("document root was not reused: %p", built)
		}
		config, err := built.Config()
		if err != nil {
			return err
		}
		if config != fixture.config {
			return fmt.Errorf("config differs: %q", config)
		}
	}
	return nil
}

func segmentedOutputDocument(tb testing.TB, value string, width int) rendercontent.Document {
	tb.Helper()
	var builder rendercontent.DocumentBuilder
	for offset := 0; offset < len(value); offset += width {
		end := min(offset+width, len(value))
		var childBuilder rendercontent.DocumentBuilder
		_, err := childBuilder.WriteString(value[offset:end])
		require.NoError(tb, err)
		child, err := childBuilder.Build(nil)
		require.NoError(tb, err)
		require.NoError(tb, builder.AppendDocument(child))
	}
	document, err := builder.Build(nil)
	require.NoError(tb, err)
	return document
}

func foreignSnapshot(tb testing.TB, fixture *outputFixture) *Snapshot {
	tb.Helper()
	return mustOutputSnapshot(
		tb, fixture.authority, fixture.config, fixture.plan, fixture.artifacts, nil,
	)
}
