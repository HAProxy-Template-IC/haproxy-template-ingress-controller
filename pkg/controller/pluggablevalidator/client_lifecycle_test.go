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

package pluggablevalidator

import (
	"context"
	"testing"
)

func TestCancelWatcherStopIsIdempotent(t *testing.T) {
	c := NewClient("test", "/tmp/test.sock", 0, 1)
	ctx, cancel := context.WithCancel(context.Background())
	stop := c.armCancelWatcher(ctx)

	stop()
	stop()
	cancel()
}

func TestCancelWatcherJoinsCancellationCallback(t *testing.T) {
	c := NewClient("test", "/tmp/test.sock", 0, 1)
	ctx, cancel := context.WithCancel(context.Background())
	stop := c.armCancelWatcher(ctx)
	cancel()

	stop()
}
