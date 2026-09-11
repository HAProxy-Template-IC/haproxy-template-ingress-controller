// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package templating

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCycleTimeSnapshotDoesNotChangeTheLiveClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		engine, err := New(map[string]string{
			"main": `{{ cycleTimeBucket(1200, "2006-01-02T15:04:05Z07:00") }}`,
		}, nil)
		require.NoError(t, err)
		started := time.Now()
		frozen := WithCycleTime(t.Context(), started)
		first, err := engine.Render(frozen, "main", nil)
		require.NoError(t, err)
		assert.Equal(t, started.UTC().Format(time.RFC3339)+"\n", first)

		time.Sleep(20 * time.Minute)
		second, err := engine.Render(frozen, "main", nil)
		require.NoError(t, err)
		assert.Equal(t, first, second)
		live, err := engine.Render(t.Context(), "main", nil)
		require.NoError(t, err)
		assert.Equal(t, started.Add(20*time.Minute).UTC().Format(time.RFC3339)+"\n", live)
		assert.Equal(t, time.Now(), cycleTime(t.Context()))
	})
}
