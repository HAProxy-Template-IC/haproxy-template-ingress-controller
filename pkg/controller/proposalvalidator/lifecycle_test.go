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

package proposalvalidator

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

type blockingOutputValidator struct {
	started  chan struct{}
	canceled chan struct{}
}

func (v *blockingOutputValidator) ValidateRenderedOutput(ctx context.Context, _ *pipeline.PipelineResult) ([]string, error) {
	close(v.started)
	<-ctx.Done()
	close(v.canceled)
	return nil, ctx.Err()
}

func waitForSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(testutil.LongTimeout):
		t.Fatal(message)
	}
}

func TestStartCancellationCancelsProposalValidation(t *testing.T) {
	bus := busevents.NewEventBus(100)
	validator := &blockingOutputValidator{started: make(chan struct{}), canceled: make(chan struct{})}
	component := New(&ComponentConfig{
		EventBus:          bus,
		Pipeline:          createTestPipelineWithOutputValidator(t, testutil.MinimalHAProxyConfig, validator),
		BaseStoreProvider: stores.NewRealStoreProvider(map[string]stores.Store{}),
		Logger:            slog.Default(),
	})
	bus.Start()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- component.Start(ctx) }()
	bus.Publish(events.NewProposalValidationRequestedEvent(nil, nil, "test", "cancel"))
	waitForSignal(t, validator.started, "proposal validation did not start")

	cancel()
	waitForSignal(t, validator.canceled, "proposal validation context was not canceled")
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(testutil.LongTimeout):
		t.Fatal("proposal validator did not stop after cancellation")
	}
}
