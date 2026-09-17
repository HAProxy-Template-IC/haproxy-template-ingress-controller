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

package server

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/haproxytest"
)

func TestReadBackSerializesRuntimeAndFileSnapshots(t *testing.T) {
	model := haproxytest.Start(t)
	base := t.TempDir()
	const mapPath = "maps/host.map"
	const content = "a.example.com be-a\n"
	file := filepath.Join(base, mapPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o700))
	require.NoError(t, os.WriteFile(file, []byte(content), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(base, "haproxy.cfg"), []byte("global\n"), 0o600))
	agent, err := New(t.Context(), &Config{
		BaseDir: base, ConfigFile: "haproxy.cfg", StateFile: ".haptic-agent.json",
		Listen:       "127.0.0.1:0",
		MasterSocket: model.MasterSocket(), WorkerSocket: model.WorkerSocket(),
		Logger: slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)
	type writeResult struct {
		interleaved bool
		err         error
	}
	attempt := make(chan writeResult, 1)
	model.With(func(m *haproxytest.Model) {
		m.Maps[mapPath] = []haproxytest.MapEntry{{Key: "a.example.com", Value: "be-a"}}
		m.Reject = func(command string) (string, bool) {
			if !strings.HasSuffix(command, "show map "+mapPath) {
				return "", false
			}
			if !agent.apply.TryLock() {
				attempt <- writeResult{}
				return "", false
			}
			defer agent.apply.Unlock()
			writeErr := os.WriteFile(file, []byte(content+"b.example.com be-b\n"), 0o600)
			attempt <- writeResult{interleaved: true, err: writeErr}
			return "", false
		}
	})
	agent.readBack(&applyRun{
		server: agent, result: api.ApplyResult{Mode: api.ResultRuntime, OK: true},
		touchedMaps: []string{mapPath},
	})
	select {
	case result := <-attempt:
		require.NoError(t, result.err)
		assert.False(t, result.interleaved, "another apply must not replace the file during map verification")
	default:
		t.Fatal("read-back did not inspect the runtime map")
	}
	got, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, content, string(got))
	assert.Zero(t, testutil.ToFloat64(agent.metrics.divergence))
}
