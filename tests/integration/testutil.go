//go:build integration

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

package integration

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// podGlobals are the lines every configuration this suite applies needs to run
// in the pod, added to the fixture's `global` section on load. The worker stats
// socket carries every runtime command; `default-path` and `crt-base` are what
// the chart emits, and they are what makes HAProxy name a map or a certificate
// at runtime by the same base-relative string the manifest carries.
var podGlobals = []string{
	"    stats socket " + WorkerSocketPath + " mode 600 level admin",
	"    default-path origin " + BaseDir,
	"    crt-base " + SSLDir,
}

// LoadTestConfig loads a test HAProxy configuration file and adds the pod's
// global settings to it. The path is relative to the testdata directory.
func LoadTestConfig(t *testing.T, relativePath string) string {
	t.Helper()
	return withPodGlobals(t, LoadTestFileContent(t, relativePath))
}

// LoadTestFileContent loads a testdata file verbatim.
func LoadTestFileContent(t *testing.T, relativePath string) string {
	t.Helper()

	// Resolve against this source file so the working directory of a test
	// binary run from elsewhere cannot change what it reads.
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to get caller information")

	fullPath := filepath.Join(filepath.Dir(filename), "testdata", relativePath)
	content, err := os.ReadFile(fullPath)
	require.NoError(t, err, "failed to read test file: %s", fullPath)
	return string(content)
}

// withPodGlobals inserts the pod's global settings after the `global` line.
func withPodGlobals(t *testing.T, config string) string {
	t.Helper()
	lines := strings.Split(config, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "global" {
			continue
		}
		return strings.Join(slices.Insert(lines, i+1, podGlobals...), "\n")
	}
	t.Fatalf("the fixture has no `global` section, so the worker socket cannot be declared:\n%s", config)
	return ""
}
