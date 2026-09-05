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

package incremental

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetachNodeDependencyStorageRejectsFrameMutation(t *testing.T) {
	frame := newDependencyFrame()
	firstKey := NewInputKey("input.first")
	firstEntry := inputEntry{
		revision:  NewRevision("revision.first"),
		found:     true,
		changedAt: 1,
	}
	require.NoError(t, frame.addInput(firstKey, firstEntry))
	entries := []nodeEntry{{
		deps:   frame.sortedDependencies(),
		inputs: frame.sortedInputs(),
	}}
	require.NoError(t, detachNodeDependencyStorage(entries))
	want := cloneNodeEntry(&entries[0])

	secondKey := NewInputKey("input.second")
	require.NoError(t, frame.addInput(secondKey, inputEntry{
		revision:  NewRevision("revision.second"),
		found:     true,
		changedAt: 2,
	}))
	frame.dependencySmall[0] = dependency{}
	frame.inputSmall[0] = InputRevision{}

	assert.Equal(t, want, entries[0])
	assert.Equal(t, len(entries[0].deps), cap(entries[0].deps))
	assert.Equal(t, len(entries[0].inputs), cap(entries[0].inputs))
}

func TestDetachNodeDependencyStorageUsesExactBatchArenas(t *testing.T) {
	dependencyA := dependency{key: inputDep(NewInputKey("input.a")), changedAt: 1}
	dependencyB := dependency{key: inputDep(NewInputKey("input.b")), changedAt: 2}
	inputA := InputRevision{Key: NewInputKey("input.a"), Revision: NewRevision("revision.a"), Found: true}
	inputB := InputRevision{Key: NewInputKey("input.b"), Revision: NewRevision("revision.b"), Found: true}
	sourceDependencies := []dependency{dependencyA, dependencyB}
	sourceInputs := []InputRevision{inputA, inputB}
	entries := []nodeEntry{
		{deps: sourceDependencies[:1], inputs: sourceInputs[:1]},
		{deps: sourceDependencies[1:], inputs: sourceInputs[1:]},
		{},
	}

	require.NoError(t, detachNodeDependencyStorage(entries))
	sourceDependencies[0] = dependency{}
	sourceDependencies[1] = dependency{}
	sourceInputs[0] = InputRevision{}
	sourceInputs[1] = InputRevision{}

	assert.Equal(t, []dependency{dependencyA}, entries[0].deps)
	assert.Equal(t, []dependency{dependencyB}, entries[1].deps)
	assert.Equal(t, []InputRevision{inputA}, entries[0].inputs)
	assert.Equal(t, []InputRevision{inputB}, entries[1].inputs)
	assert.Nil(t, entries[2].deps)
	assert.Nil(t, entries[2].inputs)
}
