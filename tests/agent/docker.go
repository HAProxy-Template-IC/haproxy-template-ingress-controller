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

package agent

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"gitlab.com/haproxy-haptic/haptic/tests/kindutil"
)

// runDocker runs one docker command with stdin and returns its combined output.
func runDocker(stdin io.Reader, args ...string) (string, error) {
	return kindutil.RunDocker(stdin, args...)
}

func mustDocker(t *testing.T, args ...string) string {
	t.Helper()
	return mustDockerInput(t, "", args...)
}

func mustDockerInput(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	out, err := runDocker(strings.NewReader(stdin), args...)
	if err != nil {
		t.Fatalf("docker %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// publishAddress is the address docker binds a published port to. Under
// docker-in-docker the daemon is a separate container, so loopback there is
// unreachable from the test process.
func publishAddress() string {
	if kindutil.IsDockerInDocker() {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

// connectHost is where the test process reaches a published port.
func connectHost() string {
	if kindutil.IsDockerInDocker() {
		return kindutil.GetDindHostname()
	}
	return "127.0.0.1"
}

// publishedPort reads back the host port docker assigned to a container port.
func publishedPort(t *testing.T, container string, port int) int {
	t.Helper()
	out := mustDocker(t, "port", container, fmt.Sprintf("%d/tcp", port))
	first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(out), "\n", 2)[0])
	_, hostPort, err := net.SplitHostPort(first)
	if err != nil {
		t.Fatalf("docker port %s %d returned %q: %v", container, port, out, err)
	}
	var parsed int
	if _, err := fmt.Sscanf(hostPort, "%d", &parsed); err != nil {
		t.Fatalf("docker port %s %d returned %q", container, port, out)
	}
	return parsed
}

// waitFor polls until probe succeeds or the budget is spent, returning the last
// failure so a timeout names what never happened.
func waitFor(t *testing.T, what string, budget time.Duration, probe func() error) {
	t.Helper()
	deadline := time.Now().Add(budget)
	var last error
	for time.Now().Before(deadline) {
		last = probe()
		if last == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s: %v", budget, what, last)
}
