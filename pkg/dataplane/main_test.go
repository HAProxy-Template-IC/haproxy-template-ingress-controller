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
	"os"
	"testing"
)

// fakeHAProxyVersionLine is the `haproxy -v` first line both local fakes
// report. Keep the version in sync with dataplanetest.DefaultFakeVersion —
// the import cycle prevents referencing the constant directly.
const fakeHAProxyVersionLine = "HAProxy version 3.2.0 2025/01/01 - https://haproxy.org/\n"

// fakeHAProxyExecutor mirrors dataplanetest's fake. The internal tests can't
// import pkg/dataplane/dataplanetest (import cycle), so the equivalent fake
// lives here.
type fakeHAProxyExecutor struct{}

func (fakeHAProxyExecutor) Version() (string, error) {
	return fakeHAProxyVersionLine, nil
}

func (fakeHAProxyExecutor) Check(_ string, _ ...string) ([]byte, error) {
	return nil, nil
}

// rejectingHAProxyExecutor simulates `haproxy -c` rejecting the config:
// [ALERT] lines in the output plus a non-zero exit. Mirrors
// dataplanetest.WithRejectAll for this package's internal tests.
type rejectingHAProxyExecutor struct{ alertDetail string }

func (rejectingHAProxyExecutor) Version() (string, error) {
	return fakeHAProxyVersionLine, nil
}

func (r rejectingHAProxyExecutor) Check(_ string, _ ...string) ([]byte, error) {
	output := "[ALERT]    (1) : config : " + r.alertDetail + "\n" +
		"[ALERT]    (1) : config : fatal errors found in configuration.\n"
	return []byte(output), errors.New("exit status 1")
}

// installRejectingHAProxy swaps in a rejecting executor for one test. Safe
// because this package's tests don't use t.Parallel.
func installRejectingHAProxy(t *testing.T, alertDetail string) {
	t.Helper()
	restore := SetHAProxyExecutor(rejectingHAProxyExecutor{alertDetail: alertDetail})
	t.Cleanup(restore)
}

// TestMain installs a fake HAProxy executor so unit tests never shell out to
// an external haproxy binary (which isn't present on every dev machine and
// has no Windows build). Installed once per package rather than per test to
// stay safe under t.Parallel.
func TestMain(m *testing.M) {
	restore := SetHAProxyExecutor(fakeHAProxyExecutor{})
	code := m.Run()
	restore()
	os.Exit(code)
}
