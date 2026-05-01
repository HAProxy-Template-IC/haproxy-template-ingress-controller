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

package dataplane

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHasFailureLines pins the severity-based filter that distinguishes
// HAProxy's advisory output (warnings, notices) from its real failure output
// (alerts, errors). It's load-bearing: webhook admission depends on this not
// rejecting configs that HAProxy considers valid.
func TestHasFailureLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{
			name:   "empty output",
			output: "",
			want:   false,
		},
		{
			name:   "only whitespace",
			output: "   \n\t\n",
			want:   false,
		},
		{
			name: "AWS-LC ignored-keyword warning (the production scenario)",
			output: `[WARNING]  (88) : config : parsing [haproxy.cfg:8] : 'tune.ssl.default-dh-param' ` +
				`is not supported by AWS-LC 1.69.0, keyword ignored`,
			want: false,
		},
		{
			name:   "warning followed by trailing newlines",
			output: "[WARNING]  (88) : config : something advisory\n\n",
			want:   false,
		},
		{
			name:   "notice line (lower severity than warning)",
			output: "[NOTICE]   (1) : haproxy version is 3.1.5",
			want:   false,
		},
		{
			name:   "alert line",
			output: "[ALERT] (001) : parsing [haproxy.cfg:15] : unknown user 'missing' in userlist 'auth_users'",
			want:   true,
		},
		{
			name: "warning and alert together — alert wins",
			output: "[WARNING] config : parsing [haproxy.cfg:8] : 'tune.x' ignored\n" +
				"[ALERT] (001) : parsing [haproxy.cfg:15] : unknown user 'missing'",
			want: true,
		},
		{
			name:   "emerg line (highest severity)",
			output: "[EMERG] kernel out of memory while parsing config",
			want:   true,
		},
		{
			name:   "crit line",
			output: "[CRIT] (1) : something critical happened",
			want:   true,
		},
		{
			name:   "err line",
			output: "[ERR] (1) : generic error",
			want:   true,
		},
		{
			name:   "indented alert line (HAProxy sometimes wraps)",
			output: "    [ALERT] (001) : indented alert",
			want:   true,
		},
		{
			name:   "alert as substring (not at line prefix) is not enough",
			output: "summary: 1 [ALERT] found earlier in run",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := hasFailureLines(tt.output)
			assert.Equal(t, tt.want, got,
				"hasFailureLines must distinguish advisory output (false) from real failures (true) — "+
					"webhook admission decisions depend on this, and a regression that flipped either "+
					"direction would either block valid configs (false→true) or admit broken configs (true→false)")
		})
	}
}

// TestInterpretHAProxyExitError pins the three-way classification of a
// non-zero haproxy -c exit. Critically, empty output must NOT be treated as
// success — that path was the original gap that admitted broken configs when
// HAProxy crashed without a structured error message (segfault, OOM-kill).
func TestInterpretHAProxyExitError(t *testing.T) {
	t.Parallel()

	exitErr := errors.New("exit status 1")

	t.Run("empty output → wrapped error", func(t *testing.T) {
		t.Parallel()
		err := interpretHAProxyExitError(nil, exitErr, "")
		require.Error(t, err,
			"empty output with non-zero exit must NOT be silently treated as success — "+
				"this path catches segfault/OOM/signal/binary-not-found scenarios where "+
				"HAProxy died without emitting anything structured")
		assert.Contains(t, err.Error(), "haproxy exited with error but produced no output")
		assert.ErrorIs(t, err, exitErr,
			"the underlying os/exec error must be wrapped so callers can inspect it")
	})

	t.Run("whitespace-only output → wrapped error", func(t *testing.T) {
		t.Parallel()
		err := interpretHAProxyExitError([]byte("   \n\t\n"), exitErr, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "haproxy exited with error but produced no output",
			"whitespace-only output must be treated identically to empty — there's no "+
				"actionable signal in either case")
	})

	t.Run("advisory-only output → nil (advisory pass)", func(t *testing.T) {
		t.Parallel()
		out := []byte(`[WARNING]  (88) : config : parsing [haproxy.cfg:8] : ` +
			`'tune.ssl.default-dh-param' is not supported by AWS-LC 1.69.0, keyword ignored`)
		err := interpretHAProxyExitError(out, exitErr, "")
		assert.NoError(t, err,
			"AWS-LC 'keyword ignored' WARNING with non-zero exit is the production "+
				"scenario this path was added for — must pass cleanly")
	})

	t.Run("output with [ALERT] → wrapped failure", func(t *testing.T) {
		t.Parallel()
		out := []byte(`[ALERT] (001) : parsing [haproxy.cfg:15] : unknown user 'missing' in userlist 'auth_users'`)
		err := interpretHAProxyExitError(out, exitErr, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "haproxy validation failed",
			"[ALERT] output must surface as a validation error — anything else would "+
				"admit broken configs")
	})
}
