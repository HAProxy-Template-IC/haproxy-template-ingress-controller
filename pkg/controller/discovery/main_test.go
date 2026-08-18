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

package discovery

import (
	"os"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/dataplanetest"
)

// TestMain installs a fake HAProxy executor so the discovery tests exercise the
// component in CI without a real haproxy binary on PATH. Previously these tests
// guarded on exec.LookPath("haproxy") and skipped silently wherever the binary
// was absent (i.e. every CI runner), leaving the component effectively
// untested. The fake replaces the same haproxyExecutor seam other packages use
// (pipeline, configtest, validator), so New()/DetectLocalVersion succeed.
// Installed once per package rather than per test to stay safe under
// t.Parallel.
func TestMain(m *testing.M) {
	restore := dataplanetest.InstallFakeHAProxy()
	code := m.Run()
	restore()
	os.Exit(code)
}

// newTestComponent builds a Component on its own test bus, for the handler
// tests that drive the struct directly.
func newTestComponent(t *testing.T) *Component {
	t.Helper()
	bus, logger := testutil.NewTestBusAndLogger()
	return New(bus, logger)
}

// testEndpointIdentity is the identity of a pod named podName, as
// endpointIdentityOf would derive it.
func testEndpointIdentity(podName string) endpointIdentity {
	return endpointIdentity{
		podNamespace: "default",
		podName:      podName,
		podUID:       podName + "-uid",
		url:          "http://127.0.0.1:5555",
	}
}
