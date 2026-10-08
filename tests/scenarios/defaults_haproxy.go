// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

var haproxyWarning = regexp.MustCompile(`(?m)^\s*\[WARNING\]|Warnings were found\.`)

func (s *Session) defaultsSyntax(ctx context.Context) error {
	pods, err := s.Pods(ctx, "loadbalancer")
	if err != nil {
		return err
	}
	if len(pods) == 0 {
		return errors.New("no HAProxy pods to validate")
	}
	for i := range pods {
		result, err := s.Kube(ctx, nil, "exec", pods[i].Name, "-c", "haproxy", "--", "haproxy", "-c", "-f", "/etc/haproxy/haproxy.cfg")
		if err != nil {
			return err
		}
		if haproxyWarning.MatchString(result.Stdout + result.Stderr) {
			return fmt.Errorf("pod %s HAProxy validation emitted warnings: %s %s", pods[i].Name, result.Stdout, result.Stderr)
		}
	}
	return nil
}

func (s *Session) defaultsRetirement(ctx context.Context) error {
	pods, err := s.Pods(ctx, "loadbalancer")
	if err != nil {
		return err
	}
	if len(pods) == 0 {
		return errors.New("no HAProxy pods to inspect")
	}
	for i := range pods {
		if err := poll(ctx, 65*time.Second, time.Second, "bootstrap retirement on "+pods[i].Name, func(ctx context.Context) (testutil.PollResult, error) {
			result, err := s.Kube(ctx, strings.NewReader("show proc\n"), "exec", "-i", pods[i].Name, "-c", "haproxy", "--", "socat", "-", "UNIX-CONNECT:/etc/haproxy/haproxy-master.sock")
			if err != nil {
				return testutil.PollFailed, err
			}
			if !bootstrapRetired(result.Stdout) {
				return testutil.PollPending, fmt.Errorf("bootstrap worker remains: %s", result.Stdout)
			}
			return testutil.PollSucceeded, nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func bootstrapRetired(output string) bool {
	reloads := 0
	oldSection := false
	for line := range strings.SplitSeq(output, "\n") {
		if strings.HasPrefix(line, "# old workers") {
			oldSection = true
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[1] == "master" {
			reloads, _ = strconv.Atoi(fields[2])
		}
		if oldSection && len(fields) > 0 {
			if _, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
				return false
			}
		}
	}
	return reloads > 0
}
