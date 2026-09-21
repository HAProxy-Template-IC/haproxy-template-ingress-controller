// Copyright 2026 Philipp Hossner
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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalSocketCannotReplaceRegularFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "haproxy.cfg")
	require.NoError(t, os.WriteFile(path, []byte("configuration"), 0o600))
	require.ErrorContains(t, removeStaleSocket(path), "non-socket file")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "configuration", string(data))
}

func TestLocalSocketsCannotReplaceAgentFilesOrOtherSockets(t *testing.T) {
	for _, path := range []string{"haproxy.cfg", ".haptic-agent.json", "worker.sock", "master.sock", "drain.sock"} {
		t.Run(path, func(t *testing.T) {
			cfg := &Config{BaseDir: t.TempDir(), ConfigFile: "haproxy.cfg", StateFile: ".haptic-agent.json",
				WorkerSocket: "worker.sock", MasterSocket: "master.sock", DrainSocket: "drain.sock", AdminSocket: path}
			require.ErrorContains(t, validateLocalSocketPaths(cfg), "choose distinct")
		})
	}
	require.NoError(t, validateLocalSocketPaths(&Config{BaseDir: t.TempDir(), ConfigFile: "haproxy.cfg", AdminSocket: "admin.sock"}))
}
