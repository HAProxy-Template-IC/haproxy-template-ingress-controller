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

package validator

import (
	"os"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/dataplanetest"
)

// TestMain installs a fake HAProxy executor so unit tests never shell out to
// an external haproxy binary (which isn't present on every dev machine and
// has no Windows build). Installed once per package rather than per test to
// stay safe under t.Parallel.
func TestMain(m *testing.M) {
	restore := dataplanetest.InstallFakeHAProxy()
	code := m.Run()
	restore()
	os.Exit(code)
}
