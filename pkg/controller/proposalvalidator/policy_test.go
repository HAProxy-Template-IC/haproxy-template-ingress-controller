package proposalvalidator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func TestAdmissionAndHTTPPromotionKeepDistinctFailurePolicies(t *testing.T) {
	t.Run("admission compares the invalid baseline", func(t *testing.T) {
		reject := &cancelingRejectingOutputValidator{}
		service := NewService(&ServiceConfig{
			Pipeline:          createTestPipelineWithOutputValidator(t, testutil.MinimalHAProxyConfig, reject),
			BaseStoreProvider: stores.NewRealStoreProvider(map[string]stores.Store{}),
		})
		output, result := service.ValidateSync(t.Context(), nil)
		require.NotNil(t, output)
		require.True(t, result.Valid)
		assert.Equal(t, 2, reject.calls)
	})
	t.Run("HTTP promotion rejects without a baseline exception", func(t *testing.T) {
		reject := &cancelingRejectingOutputValidator{}
		bus := busevents.NewEventBus(16)
		adapter := New(bus, &ServiceConfig{
			Pipeline:          createTestPipelineWithOutputValidator(t, testutil.MinimalHAProxyConfig, reject),
			BaseStoreProvider: stores.NewRealStoreProvider(map[string]stores.Store{}),
		})
		responses := bus.SubscribeTypes("test", 4, events.EventTypeProposalValidationCompleted)
		bus.Start()
		go func() { _ = adapter.Start(t.Context()) }()
		request := events.NewProposalValidationRequestedEvent(nil, nil, "httpstore", "pending content")
		bus.Publish(request)
		response := testutil.WaitForEvent[*events.ProposalValidationCompletedEvent](t, responses, testutil.LongTimeout)
		assert.Equal(t, request.ID, response.RequestID)
		assert.False(t, response.Valid)
		assert.Contains(t, response.Error, "rendered output rejected")
		assert.Equal(t, 1, reject.calls)
	})
}
