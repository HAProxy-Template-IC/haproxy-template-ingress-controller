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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

// Two cold renders overlap when the warmer is mid-render as the Coordinator
// starts. The older one loses the output-generation race at its cold-cache
// handoff and must release everything it prepared: it still holds its own
// release lock, which its deferred cleanup takes next, and the shared HTTP
// state lock, which every other session's commit takes.
func TestColdCommitLosingTheGenerationRaceReleasesItsLocks(t *testing.T) {
	fixture := newAmbientResourceFixture(t, false)

	older, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	newer, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)

	committed := make(chan error, 1)
	go func() { committed <- older.InputTransaction.Commit(t.Context()) }()
	select {
	case err := <-committed:
		require.ErrorIs(t, err, incremental.ErrCommitConflict)
	case <-time.After(5 * time.Second):
		t.Fatal("the older cold commit hung on the locks its own handoff left behind")
	}

	require.NoError(t, newer.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, fixture.service)
	assert.Equal(t, "route=v1\n", fixture.render(t))
	assert.Equal(t, uint64(1), fixture.executions())
}
