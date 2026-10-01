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

package logging

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCancellationLogging(t *testing.T) {
	for _, tc := range []struct {
		name     string
		stopping bool
		err      error
		level    string
	}{
		{name: "running cancellation", err: context.Canceled, level: "ERROR"},
		{name: "shutdown cancellation", stopping: true, err: fmt.Errorf("read: %w", context.Canceled), level: "DEBUG"},
		{name: "shutdown failure", stopping: true, err: errors.New("permission denied"), level: "ERROR"},
		{name: "shutdown deadline", stopping: true, err: context.DeadlineExceeded, level: "ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.stopping {
				cancel()
			}
			var output bytes.Buffer
			logger := WithCancellationLogging(slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})), ctx)
			logger.With("component", "client").WithGroup("request").Error("read failed", "err", tc.err)
			require.Contains(t, output.String(), "level="+tc.level)
			require.Contains(t, output.String(), "component=client")
		})
	}
}

func TestCancellationLoggingRespectsLevel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var output bytes.Buffer
	logger := WithCancellationLogging(slog.New(slog.NewTextHandler(&output, nil)), ctx)
	logger.Error("read failed", "err", context.Canceled)
	require.Empty(t, output.String())
}
