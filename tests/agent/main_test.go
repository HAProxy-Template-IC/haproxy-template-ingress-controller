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

//go:build agentdocker

// Package agent drives the HAPTIC agent as a black box: a real HAProxy in
// master-worker mode in one container, the agent in another against the same
// mounts, and the controller's client on the outside. Nothing here imports the
// agent's own packages, so the suite tests the wire contract rather than the
// implementation.
package agent

import (
	"os"
	"testing"
)

// TestMain drops the image the suite builds; containers and volumes are each
// test's own cleanup.
func TestMain(m *testing.M) {
	code := m.Run()
	removeImage(imageName)
	os.Exit(code)
}
