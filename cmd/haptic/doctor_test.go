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

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/diagnostics"
)

func TestDoctorWritesEvidenceBeforeReturningUnhealthy(t *testing.T) {
	for _, tt := range []struct {
		name              string
		healthy, complete bool
	}{
		{name: "healthy", healthy: true, complete: true}, {name: "unhealthy", healthy: false, complete: true}, {name: "incomplete", healthy: false, complete: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			report := &diagnostics.Report{SchemaVersion: 1, Namespace: "haptic", Healthy: tt.healthy, Complete: tt.complete}
			var output, stderr bytes.Buffer
			command := newDoctorCommand()
			command.SetOut(&output)
			command.SetErr(&stderr)
			options := &doctorOptions{output: "json", bundle: filepath.Join(t.TempDir(), "report.zip")}
			err := options.writeReport(command, report)
			if tt.healthy && tt.complete {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			var got diagnostics.Report
			require.NoError(t, json.Unmarshal(output.Bytes(), &got))
			require.Equal(t, *report, got)
			_, err = os.Stat(options.bundle)
			require.NoError(t, err)
			require.Contains(t, stderr.String(), options.bundle)
		})
	}
}

func TestDoctorRejectsInvalidOutputAndTimeoutBeforeConnecting(t *testing.T) {
	for _, args := range [][]string{{"--output", "yaml"}, {"--timeout", "0s"}} {
		command := newDoctorCommand()
		command.SetArgs(args)
		command.SetOut(&bytes.Buffer{})
		command.SetErr(&bytes.Buffer{})
		require.Error(t, command.Execute())
	}
}
