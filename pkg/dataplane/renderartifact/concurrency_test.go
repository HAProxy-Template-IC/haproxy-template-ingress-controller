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
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuilderAndSnapshotConcurrentAccess(t *testing.T) {
	const artifacts = 128
	authority := NewAuthority()
	builder, err := NewBuilder(authority, nil)
	require.NoError(t, err)
	require.NoError(t, addArtifactsConcurrently(builder, artifacts))
	snapshot, err := buildSnapshotConcurrently(builder, 32)
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	foreign := buildArtifactSnapshot(t, NewAuthority(), nil, concurrentSpecs(artifacts))
	require.NoError(t, readSnapshotConcurrently(snapshot, foreign, artifacts, 32))
}

func addArtifactsConcurrently(builder *Builder, count int) error {
	var additions sync.WaitGroup
	addErrors := make(chan error, count*2)
	for index := range count {
		descriptor := Descriptor{Family: Map, Path: fmt.Sprintf("map-%03d", index)}
		content := NewLiteralContent(fmt.Sprintf("key-%03d value\n", index))
		for range 2 {
			additions.Add(1)
			go func() {
				defer additions.Done()
				addErrors <- builder.Add(descriptor, content)
			}()
		}
	}
	additions.Wait()
	close(addErrors)
	for addErr := range addErrors {
		if addErr != nil {
			return addErr
		}
	}
	return nil
}

func buildSnapshotConcurrently(builder *Builder, workers int) (*Snapshot, error) {
	built := make(chan *Snapshot, workers)
	buildErrors := make(chan error, workers)
	var builds sync.WaitGroup
	for range workers {
		builds.Add(1)
		go func() {
			defer builds.Done()
			snapshot, buildErr := builder.Build()
			built <- snapshot
			buildErrors <- buildErr
		}()
	}
	builds.Wait()
	close(built)
	close(buildErrors)
	for buildErr := range buildErrors {
		if buildErr != nil {
			return nil, buildErr
		}
	}
	var snapshot *Snapshot
	for result := range built {
		if snapshot == nil {
			snapshot = result
		}
		if snapshot != result {
			return nil, errors.New("concurrent builds returned different snapshots")
		}
	}
	return snapshot, nil
}

func readSnapshotConcurrently(snapshot, foreign *Snapshot, artifacts, workers int) error {
	readErrors := make(chan error, workers)
	var reads sync.WaitGroup
	for range workers {
		reads.Add(1)
		go func() {
			defer reads.Done()
			for range 20 {
				if readErr := verifySnapshotRead(snapshot, foreign, artifacts); readErr != nil {
					readErrors <- readErr
					return
				}
			}
			readErrors <- nil
		}()
	}
	reads.Wait()
	close(readErrors)
	for readErr := range readErrors {
		if readErr != nil {
			return readErr
		}
	}
	return nil
}

func verifySnapshotRead(snapshot, foreign *Snapshot, want int) error {
	if err := snapshot.ValidateAuthentication(); err != nil {
		return err
	}
	length, err := snapshot.Len()
	if err != nil {
		return err
	}
	if length != want {
		return fmt.Errorf("got %d artifacts, want %d", length, want)
	}
	equal, err := snapshot.ExactEqual(foreign)
	if err != nil {
		return err
	}
	if !equal {
		return errorsDifferentSnapshots
	}
	visited := 0
	err = snapshot.Walk(func(artifact *Artifact) error {
		content, contentErr := artifact.Content()
		if contentErr != nil {
			return contentErr
		}
		if _, contentErr = content.String(); contentErr != nil {
			return contentErr
		}
		visited++
		return nil
	})
	if err != nil {
		return err
	}
	if visited != want {
		return fmt.Errorf("visited %d artifacts, want %d", visited, want)
	}
	return nil
}

var errorsDifferentSnapshots = errors.New("foreign snapshots differ")

func concurrentSpecs(count int) []artifactSpec {
	specs := make([]artifactSpec, count)
	for index := range specs {
		specs[index] = artifactSpec{
			descriptor: Descriptor{Family: Map, Path: fmt.Sprintf("map-%03d", index)},
			content:    NewLiteralContent(fmt.Sprintf("key-%03d value\n", index)),
		}
	}
	return specs
}
