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

package diagnostics

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBundleIsPrivateCompleteAndNeverOverwrites(t *testing.T) {
	report := &Report{SchemaVersion: 1, Namespace: "haptic", Release: "haptic", Healthy: false, Complete: false, Findings: []Finding{{Code: "agent-unavailable", Severity: "error", Action: "Check agent health."}}}
	path := filepath.Join(t.TempDir(), "support.zip")
	require.NoError(t, WriteBundle(path, report))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.ErrorIs(t, WriteBundle(path, &Report{Healthy: true}), os.ErrExist)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
	archive, err := zip.OpenReader(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, archive.Close()) })
	require.Len(t, archive.File, 2)
	require.Equal(t, "README.txt", archive.File[0].Name)
	require.Equal(t, "report.json", archive.File[1].Name)
	reader, err := archive.File[1].Open()
	require.NoError(t, err)
	payload, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	var got Report
	require.NoError(t, json.Unmarshal(payload, &got))
	require.Equal(t, *report, got)
}
