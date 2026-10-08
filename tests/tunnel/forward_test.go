// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package tunnel

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/tests/kubeexec"
	"gitlab.com/haproxy-haptic/haptic/tests/process"
)

func TestForwardOnlyAcceptsCompleteLoopbackMappings(t *testing.T) {
	tests := []struct {
		name, output string
		ports        []Port
		want         []int
		ready        bool
	}{
		{"dynamic", "Forwarding from 127.0.0.1:38001 -> 8080\nForwarding from 127.0.0.1:38002 -> 8443\n", []Port{{Remote: "http", Target: 8080}, {Remote: "https", Target: 8443}}, []int{38001, 38002}, true},
		{"partial", "Forwarding from 127.0.0.1:38001 -> 8080\n", []Port{{Remote: "http", Target: 8080}, {Remote: "https", Target: 8443}}, []int{38001, 0}, false},
		{"historical ports", "Forwarding from 127.0.0.1:38002 -> 443\nForwarding from 127.0.0.1:38001 -> 80\n", []Port{{Remote: "http", Target: 80}, {Remote: "https", Target: 443}}, []int{38001, 38002}, true},
		{"service target translated", "Forwarding from 127.0.0.1:38001 -> 18000\nForwarding from [::1]:38001 -> 18000\nForwarding from 127.0.0.1:38002 -> 18443\n", []Port{{Remote: "80"}, {Remote: "443"}}, []int{38001, 38002}, true},
		{"pinned mismatch", "Forwarding from 127.0.0.1:38002 -> 8080\n", []Port{{Local: 38001, Remote: "8080"}}, []int{0}, false},
		{"non loopback", "Forwarding from 0.0.0.0:38001 -> 8080\n", []Port{{Remote: "8080"}}, []int{0}, false},
		{"bad port", "Forwarding from 127.0.0.1:99999 -> 8080\n", []Port{{Remote: "8080"}}, []int{0}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ready := localPorts(tt.output, tt.ports)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.ready, ready)
		})
	}
}

type forwardRunner struct {
	child   *forwardChild
	command *process.Command
	ctx     context.Context
	onStart func()
}

func (*forwardRunner) Run(context.Context, *process.Command) (process.Result, error) {
	panic("unexpected synchronous execution")
}
func (r *forwardRunner) Start(ctx context.Context, command *process.Command) (process.Running, error) {
	r.command = command
	r.ctx = ctx
	if r.onStart != nil {
		r.onStart()
	}
	return r.child, nil
}

type forwardChild struct {
	done    chan struct{}
	output  process.Result
	stopped bool
}

func (c *forwardChild) Done() <-chan struct{}         { return c.done }
func (c *forwardChild) Output() process.Result        { return c.output }
func (c *forwardChild) Wait() (process.Result, error) { return c.output, errors.New("connection lost") }
func (c *forwardChild) Stop() error                   { c.stopped = true; return nil }

func TestForwardOwnsFailureCleanupAndExplicitContext(t *testing.T) {
	for _, state := range []string{"ready", "exited", "timeout", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			child := &forwardChild{done: make(chan struct{}), output: process.Result{Stdout: "Forwarding from 127.0.0.1:38001 -> 8080\n"}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch state {
			case "exited":
				close(child.done)
			case "timeout":
				child.output.Stdout = ""
			case "cancelled":
				cancel()
			}
			runner := &forwardRunner{child: child}
			kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
			timeout := time.Second
			if state == "timeout" {
				timeout = time.Millisecond
			}
			forward, err := Start(ctx, ctx, kubeexec.Client{Runner: runner, Kubeconfig: kubeconfig, Context: "kind-private", Namespace: "scenario"}, "pod/haproxy", []Port{{Remote: "http", Target: 8080}}, timeout)
			if state == "ready" {
				require.NoError(t, err)
				require.Equal(t, []int{38001}, forward.Locals)
				require.False(t, child.stopped)
				require.NoError(t, forward.Process.Stop())
			} else {
				require.Error(t, err)
				require.Nil(t, forward)
				require.Equal(t, state != "cancelled", child.stopped)
			}
			if state == "cancelled" {
				require.Nil(t, runner.command)
				return
			}
			require.Equal(t, []string{"--kubeconfig", kubeconfig, "--context", "kind-private", "--namespace", "scenario", "port-forward", "--address=127.0.0.1", "pod/haproxy", ":http"}, runner.command.Args)
		})
	}
}

func TestRecoveredForwardSurvivesStartupAndStopsWithItsOwner(t *testing.T) {
	lifetime, stop := context.WithCancel(t.Context())
	defer stop()
	for _, stage := range []string{"initial", "replacement"} {
		t.Run(stage, func(t *testing.T) {
			child := &forwardChild{done: make(chan struct{}), output: process.Result{Stdout: "Forwarding from 127.0.0.1:38001 -> 8080\n"}}
			runner := &forwardRunner{child: child}
			client := kubeexec.Client{Runner: runner, Kubeconfig: filepath.Join(t.TempDir(), "kubeconfig"), Context: "kind-private"}
			var startup context.Context
			got := Reestablish(lifetime, func(attempt context.Context) error {
				startup = attempt
				_, err := Start(attempt, lifetime, client, "pod/haproxy", []Port{{Remote: "http", Target: 8080}}, time.Second)
				return err
			}, fastRecoveryConfig(), nil)
			require.Equal(t, RecoveryRecovered, got)
			require.ErrorIs(t, startup.Err(), context.Canceled)
			require.NoError(t, runner.ctx.Err(), "successful startup must not kill the forwarder")
			require.False(t, child.stopped)
			if stage == "replacement" {
				stop()
				require.ErrorIs(t, runner.ctx.Err(), context.Canceled)
			}
		})
	}
}

func TestForwardStartupCancellationCleansUpAnUnreadyChild(t *testing.T) {
	startup, cancel := context.WithCancel(t.Context())
	defer cancel()
	child := &forwardChild{done: make(chan struct{})}
	runner := &forwardRunner{child: child, onStart: cancel}
	client := kubeexec.Client{Runner: runner, Kubeconfig: filepath.Join(t.TempDir(), "kubeconfig"), Context: "kind-private"}
	forward, err := Start(startup, t.Context(), client, "pod/haproxy", []Port{{Remote: "http", Target: 8080}}, time.Second)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, forward)
	require.True(t, child.stopped)
	require.NoError(t, runner.ctx.Err())
}
