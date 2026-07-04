// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSuperviseElection covers the three exit shapes of the leader-election
// loop. The critical case is "lease lost without shutdown": client-go's
// LeaderElector.Run returns permanently after a missed lease renewal, so a
// nil return with a live context MUST become an iteration-fatal error —
// otherwise the replica stays a follower with a dead elector forever
// (issue #57).
func TestSuperviseElection(t *testing.T) {
	logger := slog.Default()

	t.Run("lease lost without shutdown fails the iteration", func(t *testing.T) {
		ctx := context.Background()
		// Elector returns nil while the iteration context is still alive —
		// the lost-lease shape.
		err := superviseElection(ctx, func(context.Context) error { return nil }, logger)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lease lost without shutdown")
	})

	t.Run("normal teardown is not an error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		// Elector returns because the iteration context was cancelled
		// (shutdown or config-change reinitialization).
		err := superviseElection(ctx, func(ctx context.Context) error {
			cancel()
			<-ctx.Done()
			return nil
		}, logger)
		assert.NoError(t, err)
	})

	t.Run("elector error propagates", func(t *testing.T) {
		ctx := context.Background()
		electErr := errors.New("creating leader elector: boom")
		err := superviseElection(ctx, func(context.Context) error { return electErr }, logger)
		require.Error(t, err)
		assert.ErrorIs(t, err, electErr)
	})
}
