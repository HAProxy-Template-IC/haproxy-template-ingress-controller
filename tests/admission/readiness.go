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

// Package admission probes real admission without storing a resource.
package admission

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gitlab.com/haproxy-haptic/haptic/tests/kubeexec"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

var pendingErrors = []*regexp.Regexp{
	regexp.MustCompile(`^Error from server \(InternalError\): .*Internal error occurred: failed calling webhook .*failed to call webhook: .*connect: connection refused$`),
	regexp.MustCompile(`^Error from server \(InternalError\): .*Internal error occurred: failed calling webhook .*no endpoints available for service .*$`),
	regexp.MustCompile(`^error when creating .*: Post "https://.*": context deadline exceeded$`),
}

// ConnectionPending requires every error line to be a recognized startup failure.
func ConnectionPending(stderr string) bool {
	pending := false
	for line := range strings.SplitSeq(stderr, "\n") {
		if line == "" || strings.HasPrefix(line, "Warning:") {
			continue
		}
		if !pendingLine(line) {
			return false
		}
		pending = true
	}
	return pending
}

func pendingLine(line string) bool {
	for _, pattern := range pendingErrors {
		if pattern.MatchString(line) {
			return true
		}
	}
	return false
}

func Wait(ctx context.Context, client kubeexec.Client, manifest []byte, timeout time.Duration) error {
	cfg := testutil.WaitConfig{Timeout: timeout, InitialInterval: time.Second, MaxInterval: time.Second, Multiplier: 1}
	return testutil.Poll(ctx, cfg, "admission readiness", func(ctx context.Context) (testutil.PollResult, error) {
		return Probe(ctx, client, manifest)
	})
}

func Probe(ctx context.Context, client kubeexec.Client, manifest []byte) (testutil.PollResult, error) {
	requestTimeout := 10 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		requestTimeout = min(requestTimeout, time.Until(deadline))
	}
	if requestTimeout <= 0 {
		return testutil.PollFailed, context.DeadlineExceeded
	}
	result, err := client.Run(ctx, bytes.NewReader(manifest), "create", "--dry-run=server", "--request-timeout="+requestTimeout.String(), "-f", "-")
	if err == nil {
		return testutil.PollSucceeded, nil
	}
	failure := fmt.Errorf("admission create dry run failed: %s: %w", strings.TrimSpace(result.Stderr), err)
	if ConnectionPending(result.Stderr) {
		return testutil.PollPending, failure
	}
	return testutil.PollFailed, failure
}
