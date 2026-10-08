// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package traffic

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/tests/kubeexec"
	"gitlab.com/haproxy-haptic/haptic/tests/process"
	"gitlab.com/haproxy-haptic/haptic/tests/tunnel"
)

type probeRunner struct {
	children []*probeChild
	fail     bool
}

func (*probeRunner) Run(context.Context, *process.Command) (process.Result, error) {
	panic("unexpected synchronous command")
}
func (r *probeRunner) Start(_ context.Context, command *process.Command) (process.Running, error) {
	if r.fail {
		return nil, errors.New("forwarder unavailable")
	}
	child := &probeChild{done: make(chan struct{}), ports: []int{23080 + len(r.children)*100, 23443 + len(r.children)*100}, args: slices.Clone(command.Args)}
	r.children = append(r.children, child)
	return child, nil
}

type probeChild struct {
	done    chan struct{}
	ports   []int
	args    []string
	stopped bool
}

func (c *probeChild) Done() <-chan struct{} { return c.done }
func (c *probeChild) Output() process.Result {
	return process.Result{Stdout: fmt.Sprintf("Forwarding from 127.0.0.1:%d -> 443\nForwarding from 127.0.0.1:%d -> 80\n", c.ports[1], c.ports[0])}
}
func (c *probeChild) Wait() (process.Result, error) { return c.Output(), errors.New("connection lost") }
func (c *probeChild) Stop() error                   { c.stopped = true; return nil }

func probeFixture(t *testing.T, runner *probeRunner) *PodProbe {
	t.Helper()
	return &PodProbe{Client: kubeexec.Client{Runner: runner, Kubeconfig: filepath.Join(t.TempDir(), "kubeconfig"), Context: "kind-test", Namespace: "haptic"}, Target: "pod/first", Ports: []tunnel.Port{{Remote: "http", Target: 80}, {Remote: "https", Target: 443}}, StartupTimeout: time.Second, RouteTimeout: time.Second, Interval: time.Millisecond}
}

func TestPodProbeReconnectsWithNewPortsAndJoinsAllForwarders(t *testing.T) {
	runner := &probeRunner{}
	probe := probeFixture(t, runner)
	attempts := 0
	probe.Check = func(_ context.Context, ports []int) error {
		attempts++
		require.Equal(t, runner.children[len(runner.children)-1].ports, ports)
		if attempts == 1 {
			close(runner.children[0].done)
			return errors.New("listener is not ready")
		}
		require.Equal(t, []int{23180, 23543}, ports)
		return nil
	}
	require.NoError(t, Wait(t.Context(), probe))
	require.Len(t, runner.children, 2)
	for _, child := range runner.children {
		require.True(t, child.stopped)
		require.Contains(t, child.args, "pod/first")
		require.Equal(t, []string{":http", ":https"}, child.args[len(child.args)-2:])
	}
}

func TestPodProbeDoesNotProbeBeforeTunnelReadiness(t *testing.T) {
	runner := &probeRunner{fail: true}
	probe := probeFixture(t, runner)
	probe.Check = func(context.Context, []int) error { t.Fatal("probe before tunnel readiness"); return nil }
	require.ErrorContains(t, Wait(t.Context(), probe), "forwarder unavailable")
	require.Empty(t, runner.children)
}

func TestPodProbePersistentFailureAndCancellationCleanUp(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			runner := &probeRunner{}
			probe := probeFixture(t, runner)
			probe.RouteTimeout = 10 * time.Millisecond
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			probe.Check = func(context.Context, []int) error {
				if cancelled {
					cancel()
				}
				return errors.New("wrong backend")
			}
			err := Wait(ctx, probe)
			if cancelled {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}
			require.ErrorContains(t, err, "wrong backend")
			require.Len(t, runner.children, 1)
			require.True(t, runner.children[0].stopped)
		})
	}
}
