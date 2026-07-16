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

package controller

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLogBackgroundComponentError(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	deadlineCtx, cancelDeadline := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancelDeadline()

	tests := []struct {
		name    string
		ctx     context.Context
		err     error
		wantLog bool
	}{
		{name: "success", ctx: context.Background()},
		{name: "canceled", ctx: canceledCtx, err: context.Canceled},
		{name: "wrapped cancellation", ctx: canceledCtx, err: errors.Join(errors.New("stopped"), context.Canceled)},
		{name: "parent deadline exceeded", ctx: deadlineCtx, err: context.DeadlineExceeded},
		{name: "independent deadline exceeded", ctx: context.Background(), err: context.DeadlineExceeded, wantLog: true},
		{name: "component failure during cancellation", ctx: canceledCtx, err: errors.New("event loop failed"), wantLog: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&output, nil))

			logBackgroundComponentError(test.ctx, logger, "Metrics component", test.err)

			if test.wantLog {
				assert.Contains(t, output.String(), "Metrics component failed")
				assert.Contains(t, output.String(), test.err.Error())
				return
			}
			assert.Empty(t, output.String())
		})
	}
}
