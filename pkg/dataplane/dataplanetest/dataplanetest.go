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

// Package dataplanetest provides test doubles for pkg/dataplane.
//
// Unit tests must never shell out to external binaries: the haproxy binary
// is not present on every dev machine (it has no Windows build at all), and
// exec-ing it makes tests slow and environment-dependent. Packages whose
// tests reach dataplane.DetectLocalVersion or semantic validation install
// the fake once per package:
//
//	func TestMain(m *testing.M) {
//		restore := dataplanetest.InstallFakeHAProxy()
//		code := m.Run()
//		restore()
//		os.Exit(code)
//	}
//
// Installing per package (not per test) avoids restore-ordering hazards with
// t.Parallel. pkg/dataplane's own internal tests cannot import this package
// (cycle); they install an equivalent fake via dataplane.SetHAProxyExecutor
// directly.
package dataplanetest

import (
	"errors"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// DefaultFakeVersion is the HAProxy version the fake reports by default.
// 3.2 matches versions.env's DEFAULT_HAPROXY so capability detection in
// tests mirrors the bundled HAProxy.
const DefaultFakeVersion = "3.2.0"

// Option customizes the fake executor.
type Option func(*fakeExecutor)

// WithCheck replaces the config-check behavior. The default accepts every
// config (nil error, no output). Use this to simulate haproxy rejecting a
// config: return output containing an "[ALERT]" line together with a non-nil
// error, mirroring a real `haproxy -c` failure.
func WithCheck(check func(workDir string, args []string) ([]byte, error)) Option {
	return func(f *fakeExecutor) { f.check = check }
}

// WithRejectAll makes every config check fail the way `haproxy -c` fails on
// an invalid config: combined output carrying [ALERT] lines plus a non-nil
// exit error. alertDetail becomes the alert message, e.g.
// "parsing [haproxy.cfg:5] : unknown keyword 'invalid_directive'".
//
// Tests using this simulate haproxy's verdict rather than reproduce it —
// coverage that the real binary actually rejects a given construct belongs
// in the integration suite.
func WithRejectAll(alertDetail string) Option {
	return WithCheck(func(_ string, _ []string) ([]byte, error) {
		output := "[ALERT]    (1) : config : " + alertDetail + "\n" +
			"[ALERT]    (1) : config : fatal errors found in configuration.\n"
		return []byte(output), errors.New("exit status 1")
	})
}

// InstallFakeHAProxy installs a dataplane.HAProxyExecutor that never executes
// an external process: `haproxy -v` reports DefaultFakeVersion and config
// checks succeed. It returns a restore function; call it (or let the process
// exit) when done.
func InstallFakeHAProxy(opts ...Option) (restore func()) {
	f := &fakeExecutor{version: DefaultFakeVersion}
	for _, opt := range opts {
		opt(f)
	}
	return dataplane.SetHAProxyExecutor(f)
}

type fakeExecutor struct {
	version string
	check   func(workDir string, args []string) ([]byte, error)
}

func (f *fakeExecutor) Version() (string, error) {
	return "HAProxy version " + f.version + " 2025/01/01 - https://haproxy.org/\n", nil
}

func (f *fakeExecutor) Check(workDir string, args ...string) ([]byte, error) {
	if f.check != nil {
		return f.check(workDir, args)
	}
	return nil, nil
}
