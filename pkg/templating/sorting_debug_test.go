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

package templating

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSortByItems_DebugLogging verifies that filter-debug mode makes sort_by
// log each comparison with the documented structured fields, and that it stays
// silent when disabled (template-engine spec: Filter Debug Logging).
func TestSortByItems_DebugLogging(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	items := []any{
		map[string]any{"priority": 1},
		map[string]any{"priority": 2},
	}
	criteria := []string{"$.priority:desc"}

	// Debug disabled → no comparison logging.
	buf.Reset()
	_, err := sortByItems(items, criteria, false)
	require.NoError(t, err)
	require.NotContains(t, buf.String(), "SORT comparison")

	// Debug enabled → each comparison logged with the documented fields.
	buf.Reset()
	_, err = sortByItems(items, criteria, true)
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "SORT comparison")
	require.Contains(t, out, "criterion=")
	require.Contains(t, out, "valA_type=")
	require.Contains(t, out, "result=")
}
