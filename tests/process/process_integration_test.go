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

//go:build testinfra && unix

package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

func fixtureCommand(t *testing.T, mode string) *Command {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	return &Command{Name: executable, Args: []string{"-test.run=^TestProcessFixture$"}, Env: map[string]string{"HAPTIC_PROCESS_FIXTURE": mode}}
}

func TestProcessFixture(t *testing.T) {
	switch os.Getenv("HAPTIC_PROCESS_FIXTURE") {
	case "inspect":
		body, err := io.ReadAll(os.Stdin)
		require.NoError(t, err)
		dir, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, json.NewEncoder(os.Stdout).Encode(map[string]string{"input": string(body), "dir": dir, "env": os.Getenv("HAPTIC_PROCESS_VALUE")}))
		fmt.Fprint(os.Stderr, "separate stderr")
		os.Exit(0)
	case "fail":
		fmt.Fprint(os.Stdout, "partial stdout")
		fmt.Fprint(os.Stderr, "failure stderr")
		os.Exit(23)
	case "server":
		serveProcessFixture(t)
	case "parent", "orphan":
		mode := os.Getenv("HAPTIC_PROCESS_FIXTURE")
		command := fixtureCommand(t, "server")
		child := exec.Command(command.Name, command.Args...)
		child.Env = append(os.Environ(), environment(command.Env)...)
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		require.NoError(t, child.Start())
		if mode == "orphan" {
			os.Exit(0)
		}
		require.NoError(t, child.Wait())
	}
}

func serveProcessFixture(t *testing.T) {
	t.Helper()
	signal.Ignore(syscall.SIGTERM)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	fmt.Fprintln(os.Stdout, listener.Addr().String())
	for {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		_ = conn.Close()
	}
}

func TestExecutorPreservesInputEnvironmentDirectoryAndStreams(t *testing.T) {
	command := fixtureCommand(t, "inspect")
	command.Dir = t.TempDir()
	command.Stdin = strings.NewReader("literal input\n")
	command.Env["HAPTIC_PROCESS_VALUE"] = "value with spaces = literal"
	result, err := (Executor{}).Run(t.Context(), command)
	require.NoError(t, err)
	assert.Zero(t, result.ExitCode)
	assert.Equal(t, "separate stderr", result.Stderr)
	var got map[string]string
	require.NoError(t, json.Unmarshal([]byte(result.Stdout), &got))
	assert.Equal(t, map[string]string{"input": "literal input\n", "dir": command.Dir, "env": command.Env["HAPTIC_PROCESS_VALUE"]}, got)
}

func TestExecutorPreservesFailureExitAndOutput(t *testing.T) {
	result, err := (Executor{}).Run(t.Context(), fixtureCommand(t, "fail"))
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, 23, result.ExitCode)
	assert.Equal(t, "partial stdout", result.Stdout)
	assert.Equal(t, "failure stderr", result.Stderr)
}

func TestExecutorCancellationAndStopKillDescendants(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprintf("stop=%t", stop), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			child, err := (Executor{}).Start(ctx, fixtureCommand(t, "parent"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, child.Stop()) })
			var address string
			cfg := testutil.FastWaitConfig()
			cfg.Timeout = 2 * time.Second
			require.NoError(t, testutil.Poll(ctx, cfg, "child listener", func(context.Context) (testutil.PollResult, error) {
				address = strings.TrimSpace(child.Output().Stdout)
				if address == "" {
					return testutil.PollPending, nil
				}
				return testutil.PollSucceeded, nil
			}))
			if stop {
				require.NoError(t, child.Stop())
			} else {
				cancel()
			}
			_, err = child.Wait()
			require.ErrorIs(t, err, context.Canceled)
			assertListenerGone(t, address)
		})
	}
}

func TestExecutorReapsDescendantsAfterParentExits(t *testing.T) {
	result, err := (Executor{}).Run(t.Context(), fixtureCommand(t, "orphan"))
	require.ErrorIs(t, err, exec.ErrWaitDelay)
	assert.Zero(t, result.ExitCode)
	assertListenerGone(t, strings.TrimSpace(result.Stdout))
}

func assertListenerGone(t *testing.T, address string) {
	t.Helper()
	require.NotEmpty(t, address)
	cfg := testutil.WaitConfig{Timeout: time.Second, InitialInterval: time.Millisecond, MaxInterval: 10 * time.Millisecond, Multiplier: 2}
	require.NoError(t, testutil.Poll(t.Context(), cfg, "descendant listener closed after SIGKILL", func(ctx context.Context) (testutil.PollResult, error) {
		dialer := net.Dialer{Timeout: 100 * time.Millisecond}
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if conn != nil {
			_ = conn.Close()
			return testutil.PollPending, nil
		}
		if errors.Is(err, syscall.ECONNREFUSED) {
			return testutil.PollSucceeded, nil
		}
		if errors.Is(err, syscall.ECONNRESET) {
			return testutil.PollPending, err
		}
		return testutil.PollFailed, fmt.Errorf("checking descendant listener: %w", err)
	}))
}
