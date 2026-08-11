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

package validator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

func waitForTemplateSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(testutil.LongTimeout):
		t.Fatal(message)
	}
}

func TestTemplateValidatorCancellationCancelsBootstrap(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	started := make(chan struct{})
	canceled := make(chan struct{})
	bootstrap := func(ctx context.Context, _ *coreconfig.Config) (*typebootstrap.Result, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	}
	validator := NewTemplateValidator(bus, logger, bootstrap)
	bus.Start()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- validator.Start(ctx) }()
	bus.Publish(events.NewConfigValidationRequest(createValidTestConfig(), "cancel"))
	waitForTemplateSignal(t, started, "template bootstrap did not start")

	cancel()
	waitForTemplateSignal(t, canceled, "template bootstrap context was not canceled")
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(testutil.LongTimeout):
		t.Fatal("template validator did not stop after cancellation")
	}
}
