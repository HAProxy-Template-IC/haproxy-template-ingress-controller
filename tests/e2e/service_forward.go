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

//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/tunnel"
)

// ServiceForward owns a supervised loopback tunnel. Ports includes every mapping;
// HTTPPort and HTTPSPort are aliases for Service ports 80 and 443.
type ServiceForward struct {
	Ports     map[int]int
	HTTPPort  int
	HTTPSPort int
}

var forwardLineRe = regexp.MustCompile(`Forwarding from 127\.0\.0\.1:(\d+) -> (\d+)`)

// Recovery budget for a kubectl port-forward that stalls or exits mid-test. The
// watchdog tears a stalled tunnel down; these bound how long the supervisor
// re-opens it before the failure is attributed rather than retried forever.
const (
	tunnelRecoveryMinBackoff = 250 * time.Millisecond
	tunnelRecoveryMaxBackoff = 5 * time.Second
	tunnelInitialBudget      = 60 * time.Second
	tunnelRestartBudget      = 90 * time.Second
	tunnelRestartCooldown    = 500 * time.Millisecond
)

// ForwardGateway starts a kubectl port-forward to the per-Gateway Service
// of the given Gateway and returns the local port mapping. servicePorts
// lists the Service ports to forward. The tunnel is torn down via t.Cleanup.
//
// The per-Gateway Service is created by the chart's render pipeline, so it
// appears shortly after the Gateway; the wait below covers that
// propagation. Traffic through the tunnel lands on one HAProxy pod's
// per-Gateway bind — routing behavior is identical to the LB path minus
// the load-balancer hop.
func ForwardGateway(ctx context.Context, t *testing.T, gatewayNamespace, gatewayName string, servicePorts ...int) ServiceForward {
	t.Helper()
	// Gate on Programmed=True first: the chart flips it only after HAProxy
	// reload verification, i.e. once the per-Gateway bind is actually
	// listening. Tunneling earlier is fatal — kubectl port-forward treats
	// the first refused upstream connection as "lost connection to pod"
	// and exits, so a tunnel opened before the bind exists dies on the
	// test's first probe.
	if out, err := exec.CommandContext(ctx, "kubectl", kubeconfigFlag, kubeconfigPath,
		"-n", gatewayNamespace, "wait", "--for=condition=Programmed",
		"gateway/"+gatewayName, "--timeout=30s").CombinedOutput(); err != nil {
		t.Fatalf("gateway %s/%s never became Programmed: %v (%s)", gatewayNamespace, gatewayName, err, out)
	}

	// Discover the per-Gateway Service by its gateway labels rather than
	// computing its name: the chart shortens long names to fit the 63-char
	// Service name cap, so the naming scheme is chart-internal. The labels
	// (gateway-name + gateway-namespace) are the stable public contract —
	// the upstream GatewayInfrastructure conformance test discovers the
	// Service the same way.
	svc := waitForGatewayService(ctx, t, gatewayNamespace, gatewayName)
	return ForwardService(t, svc, servicePorts...)
}

// ForwardService tunnels to a Service in the controller namespace.
func ForwardService(t *testing.T, svc string, servicePorts ...int) ServiceForward {
	t.Helper()
	if len(servicePorts) == 0 {
		servicePorts = []int{80}
	}

	// Deliberately context.Background(): the tunnel must outlive the Setup
	// phase's ctx and is torn down via t.Cleanup below.
	fwdCtx, stop := context.WithCancel(context.Background())

	portArgs := make([]string, len(servicePorts))
	for i, p := range servicePorts {
		portArgs[i] = ":" + strconv.Itoa(p) // random local port
	}
	// A stall or a transient apiserver hiccup during setup must recover, not
	// fail the job: retry the first handshake within a budget before giving up.
	var cmd *exec.Cmd
	var locals []int
	if tunnel.Reestablish(fwdCtx, func(ctx context.Context) error {
		c, l, startErr := startForwardTunnel(ctx, svc, portArgs, len(servicePorts))
		if startErr != nil {
			return startErr
		}
		cmd, locals = c, l
		return nil
	}, tunnel.RecoveryConfig{
		MinBackoff: tunnelRecoveryMinBackoff,
		MaxBackoff: tunnelRecoveryMaxBackoff,
		Budget:     tunnelInitialBudget,
	}, func(msg string) {
		t.Logf("ForwardService %s: %s", svc, msg)
	}) != tunnel.RecoveryRecovered {
		stop()
		t.Fatalf("kubectl port-forward %s: tunnel not established within %s", svc, tunnelInitialBudget)
	}

	fwd := newServiceForward(servicePorts, locals)

	// Supervisor: restart the tunnel on the SAME local ports when kubectl
	// exits mid-test (e.g. "lost connection to pod" after an upstream RST —
	// issue #48). Poll loops using fwd's ports see at most a brief
	// connection-refused window and then recover.
	pinned := make([]string, len(servicePorts))
	for i, p := range servicePorts {
		pinned[i] = strconv.Itoa(locals[i]) + ":" + strconv.Itoa(p)
	}
	var mu sync.Mutex // guards current: written by the supervisor, read by the watchdog
	current := cmd
	// establishPinned re-opens the tunnel on the pinned local ports and swaps it
	// in for the watchdog to probe. Used for every recovery after the first.
	establishPinned := func(ctx context.Context) error {
		next, _, err := startForwardTunnel(ctx, svc, pinned, len(servicePorts))
		if err != nil {
			return err
		}
		mu.Lock()
		current = next
		mu.Unlock()
		return nil
	}
	supervisorDone := make(chan struct{})
	go func() {
		defer close(supervisorDone)
		runForwardSupervisor(fwdCtx, t, svc, pinned, func() *exec.Cmd {
			mu.Lock()
			defer mu.Unlock()
			return current
		}, establishPinned)
	}()
	// Watchdog: exit-based supervision misses the tunnel's second failure
	// mode — kubectl stays alive and keeps accepting on the local port while
	// the apiserver stream is wedged, so every forwarded request hangs to
	// its deadline. Probe the tunnel and kill kubectl on consecutive probe
	// timeouts; the supervisor above then restarts it on the pinned ports.
	watchdogDone := make(chan struct{})
	go func() {
		defer close(watchdogDone)
		if fwd.HTTPPort == 0 && fwd.HTTPSPort == 0 {
			return
		}
		tunnel.Watch(fwdCtx, fwd.HTTPPort, fwd.HTTPSPort,
			3*time.Second, 3*time.Second, 2,
			func() any {
				mu.Lock()
				defer mu.Unlock()
				return current.Process
			},
			func(id any) {
				// Kill only the process the strikes were counted against —
				// the supervisor may have already swapped in a fresh tunnel.
				mu.Lock()
				p := current.Process
				mu.Unlock()
				if p == id {
					_ = p.Kill()
				}
			},
			func(msg string) {
				if fwdCtx.Err() == nil {
					t.Logf("ForwardService %s: %s", svc, msg)
				}
			})
	}()
	t.Cleanup(func() {
		stop()
		<-watchdogDone   // stop probing before the tunnel goes away
		<-supervisorDone // the supervisor reaps the current kubectl process
	})
	return fwd
}

func newServiceForward(servicePorts, locals []int) ServiceForward {
	fwd := ServiceForward{Ports: make(map[int]int, len(servicePorts))}
	for i, port := range servicePorts {
		fwd.Ports[port] = locals[i]
	}
	fwd.HTTPPort = fwd.Ports[80]
	fwd.HTTPSPort = fwd.Ports[443]
	return fwd
}

// startForwardTunnel starts one `kubectl port-forward service/<svc>` process
// and parses the local ports from its "Forwarding from 127.0.0.1:<local> ->
// <target>" lines. kubectl reports the resolved TARGET port (the pod's
// per-Gateway bind port, chart-allocated), not the Service port asked for —
// so local ports are matched to the requested ports by ORDER, which is the
// order kubectl emits them. The IPv6 twin lines ("[::1]:...") repeat the
// same local port and are deduplicated. On error the started process is
// killed; on success the caller owns reaping it via Wait.
func startForwardTunnel(ctx context.Context, svc string, portArgs []string, wantPorts int) (*exec.Cmd, []int, error) {
	args := make([]string, 0, 6+len(portArgs))
	args = append(args, kubeconfigFlag, kubeconfigPath, "-n", ControllerNamespace, "port-forward", "service/"+svc)
	args = append(args, portArgs...)
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start: %w", err)
	}
	fail := func(cause error) (*exec.Cmd, []int, error) {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, nil, cause
	}

	lines := streamForwardLines(ctx, stdout)

	deadline := time.After(20 * time.Second)
	seen := map[int]bool{}
	var locals []int
	for len(locals) < wantPorts {
		select {
		case line, ok := <-lines:
			if !ok {
				return fail(fmt.Errorf("exited before forwarding (parsed %d/%d ports)", len(locals), wantPorts))
			}
			m := forwardLineRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			local, _ := strconv.Atoi(m[1])
			if seen[local] {
				continue
			}
			seen[local] = true
			locals = append(locals, local)
		case <-deadline:
			return fail(fmt.Errorf("timed out waiting for forwarding lines (parsed %d/%d)", len(locals), wantPorts))
		}
	}
	// Keep draining stdout ("Handling connection for ..." chatter) so the
	// pipe can never fill up and stall kubectl's forwarding loop.
	go func() {
		for range lines {
			continue // discard until the process exits and the channel closes
		}
	}()
	return cmd, locals, nil
}

func waitForGatewayService(ctx context.Context, t *testing.T, namespace, name string) string {
	t.Helper()
	selector := fmt.Sprintf(
		"gateway.networking.k8s.io/gateway-name=%s,gateway.networking.k8s.io/gateway-namespace=%s",
		name, namespace)
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		out, err := exec.CommandContext(waitCtx, "kubectl", kubeconfigFlag, kubeconfigPath,
			"-n", ControllerNamespace, "get", "service", "-l", selector,
			"-o", "jsonpath={.items[0].metadata.name}").Output()
		if err == nil && len(out) > 0 {
			return string(out)
		}
		select {
		case <-waitCtx.Done():
			t.Fatalf("per-Gateway Service for %s/%s never appeared (selector %q): %v", namespace, name, selector, err)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func streamForwardLines(ctx context.Context, reader io.Reader) <-chan string {
	lines := make(chan string, 8)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	return lines
}

func runForwardSupervisor(
	ctx context.Context, t *testing.T, service string, ports []string,
	current func() *exec.Cmd, establish func(context.Context) error,
) {
	t.Helper()
	for {
		waitErr := current().Wait()
		if ctx.Err() != nil {
			return
		}
		t.Logf("ForwardService %s: tunnel exited unexpectedly (%v); re-establishing on pinned ports %v", service, waitErr, ports)
		switch tunnel.Reestablish(ctx, establish, tunnel.RecoveryConfig{
			MinBackoff: tunnelRecoveryMinBackoff,
			MaxBackoff: tunnelRecoveryMaxBackoff,
			Budget:     tunnelRestartBudget,
		}, func(msg string) {
			t.Logf("ForwardService %s: %s", service, msg)
		}) {
		case tunnel.RecoveryCtxDone:
			return
		case tunnel.RecoveryBudgetExceeded:
			t.Errorf("ForwardService %s: port-forward could not be re-established within %s; "+
				"the apiserver port-forward path is unhealthy", service, tunnelRestartBudget)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(tunnelRestartCooldown):
		}
	}
}
