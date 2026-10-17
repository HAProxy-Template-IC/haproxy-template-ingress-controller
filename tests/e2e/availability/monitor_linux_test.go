//go:build e2e

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

package availability

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// forkHold outlasts the monitor's `sleep 0.1` and a local curl probe.
const forkHold = 200 * time.Millisecond

const monitorToken = "0123456789abcdef"

type monitorRun struct {
	shell    *forkDelayedShell
	stateDir string
	stderr   string
	// handshake carries the monitor's first stdout line, empty on EOF.
	handshake chan string
}

func startDelayedMonitor(t *testing.T, statusFor func(request int64) int) (*monitorRun, *atomic.Int64) {
	t.Helper()
	_, err := exec.LookPath("curl")
	require.NoError(t, err, "the monitor probes with curl")

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(statusFor(requests.Add(1)))
	}))
	t.Cleanup(server.Close)
	port, found := strings.CutPrefix(server.URL, "http://127.0.0.1")
	require.True(t, found, "the monitor only probes 127.0.0.1, got %s", server.URL)

	dir := t.TempDir()
	run := &monitorRun{
		stateDir:  filepath.Join(dir, "state"),
		stderr:    filepath.Join(dir, "stderr"),
		handshake: make(chan string, 1),
	}
	stdin, err := os.Open(os.DevNull)
	require.NoError(t, err)
	defer stdin.Close()
	stdoutRead, stdoutWrite, err := os.Pipe()
	require.NoError(t, err)
	stderr, err := os.Create(run.stderr)
	require.NoError(t, err)
	defer stderr.Close()

	run.shell, err = startForkDelayedShell(forkHold, []*os.File{stdin, stdoutWrite, stderr},
		"-c", MonitorScript, "haproxy-availability-monitor", "monitor.test", port+"/probe", run.stateDir, monitorToken)
	require.NoError(t, stdoutWrite.Close())
	require.NoError(t, err)
	t.Cleanup(run.shell.kill)

	go func() {
		defer stdoutRead.Close()
		scanner := bufio.NewScanner(stdoutRead)
		scanner.Scan()
		run.handshake <- scanner.Text()
		for scanner.Scan() {
		}
	}()
	return run, &requests
}

func (r *monitorRun) awaitReady(t *testing.T) {
	t.Helper()
	select {
	case line := <-r.handshake:
		if line != "READY" {
			t.Fatalf("monitor handshake was %q; %s", line, r.describeExit(t))
		}
	case <-time.After(30 * time.Second):
		t.Fatal("monitor sent no handshake")
	}
}

func (r *monitorRun) awaitProbes(t *testing.T, requests *atomic.Int64, want int64) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for requests.Load() < want {
		select {
		case <-r.shell.exited:
			t.Fatalf("monitor stopped after %d of %d probes; %s", requests.Load(), want, r.describeExit(t))
		case <-deadline:
			t.Fatalf("monitor made %d of %d probes", requests.Load(), want)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (r *monitorRun) awaitExit(t *testing.T) int {
	t.Helper()
	select {
	case <-r.shell.exited:
	case <-time.After(30 * time.Second):
		t.Fatal("monitor did not exit")
	}
	require.NoError(t, r.shell.traceErr)
	return r.shell.exitCode
}

func (r *monitorRun) describeExit(t *testing.T) string {
	t.Helper()
	code := r.awaitExit(t)
	stderr, err := os.ReadFile(r.stderr)
	require.NoError(t, err)
	return fmt.Sprintf("it exited %d with stderr %q", code, stderr)
}

// Every fork is held until its curl or sleep child has exited, so the shell
// reaps each child before the monitor can record it.
func TestMonitorSurvivesChildrenReapedBeforeTheyAreRecorded(t *testing.T) {
	run, requests := startDelayedMonitor(t, func(int64) int { return http.StatusOK })
	run.awaitReady(t)
	run.awaitProbes(t, requests, 4)

	stop := exec.Command("/bin/sh", "-c", StopScript, "haproxy-availability-monitor-cleanup", run.stateDir, monitorToken, "0")
	output, err := stop.CombinedOutput()
	require.NoError(t, err, "stop script: %s", output)

	require.Zero(t, run.awaitExit(t), run.describeExit(t))
	require.NoDirExists(t, run.stateDir)
	require.GreaterOrEqual(t, run.shell.childExitedDuringHold.Load(), int64(2),
		"no child exited during a hold, so this run did not exercise the reap race")
}

func TestMonitorReportsAFailedProbeWhileForksAreDelayed(t *testing.T) {
	run, _ := startDelayedMonitor(t, func(request int64) int {
		if request > 3 {
			return http.StatusServiceUnavailable
		}
		return http.StatusOK
	})
	run.awaitReady(t)

	require.Equal(t, 1, run.awaitExit(t))
	stderr, err := os.ReadFile(run.stderr)
	require.NoError(t, err)
	require.Contains(t, string(stderr), FailureMarker+" status=503")
}
