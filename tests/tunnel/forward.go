// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package tunnel

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gitlab.com/haproxy-haptic/haptic/tests/kubeexec"
	"gitlab.com/haproxy-haptic/haptic/tests/process"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

type Port struct {
	Local  int
	Remote string
	// Target is the resolved pod port; zero matches kubectl's argument order.
	Target int
}

type Forward struct {
	Process process.Running
	Locals  []int
}

var forwardLine = regexp.MustCompile(`^Forwarding from 127\.0\.0\.1:(\d+) -> (\d+)$`)

// Start bounds readiness by startup; the process lives until lifetime ends or Stop.
func Start(startup, lifetime context.Context, client kubeexec.Client, target string, ports []Port, timeout time.Duration) (*Forward, error) {
	if err := errors.Join(startup.Err(), lifetime.Err()); err != nil {
		return nil, err
	}
	if timeout <= 0 {
		return nil, errors.New("port-forward startup budget expired")
	}
	args, err := forwardArgs(target, ports)
	if err != nil {
		return nil, err
	}
	running, err := client.Start(lifetime, nil, args...)
	if err != nil {
		return nil, err
	}
	var locals []int
	err = testutil.Poll(startup, testutil.WaitConfig{Timeout: timeout, InitialInterval: 50 * time.Millisecond, MaxInterval: 50 * time.Millisecond, Multiplier: 1}, "port-forward "+target, func(context.Context) (testutil.PollResult, error) {
		select {
		case <-running.Done():
			output, waitErr := running.Wait()
			return testutil.PollFailed, fmt.Errorf("forwarder exited before readiness: %s: %w", strings.TrimSpace(output.Stderr), errors.Join(waitErr, errors.New("forwarder exited")))
		default:
		}
		var complete bool
		locals, complete = localPorts(running.Output().Stdout, ports)
		if complete {
			return testutil.PollSucceeded, nil
		}
		return testutil.PollPending, fmt.Errorf("waiting for %d mappings", len(ports))
	})
	if err != nil {
		return nil, errors.Join(err, running.Stop())
	}
	return &Forward{Process: running, Locals: locals}, nil
}

func forwardArgs(target string, ports []Port) ([]string, error) {
	if target == "" || strings.HasPrefix(target, "-") || len(ports) == 0 {
		return nil, errors.New("port-forward needs a target and at least one port")
	}
	args := []string{"port-forward", "--address=127.0.0.1", target}
	for _, port := range ports {
		if port.Local < 0 || port.Local > 65535 || port.Remote == "" || strings.ContainsAny(port.Remote, ": \t\n") || port.Target < 0 || port.Target > 65535 {
			return nil, fmt.Errorf("invalid port-forward mapping %+v", port)
		}
		local := ""
		if port.Local != 0 {
			local = strconv.Itoa(port.Local)
		}
		args = append(args, local+":"+port.Remote)
	}
	return args, nil
}

func localPorts(output string, ports []Port) ([]int, bool) {
	locals := make([]int, len(ports))
	seen := make(map[int]bool)
	ordinal := 0
	for line := range strings.SplitSeq(output, "\n") {
		match := forwardLine.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		local, _ := strconv.Atoi(match[1])
		remote, _ := strconv.Atoi(match[2])
		if local <= 0 || local > 65535 || seen[local] {
			continue
		}
		seen[local] = true
		for i, port := range ports {
			if (port.Target == remote || (port.Target == 0 && i == ordinal)) && (port.Local == 0 || port.Local == local) {
				locals[i] = local
			}
		}
		ordinal++
	}
	for _, local := range locals {
		if local == 0 {
			return locals, false
		}
	}
	return locals, true
}
