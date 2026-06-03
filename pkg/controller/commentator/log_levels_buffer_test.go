package commentator

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

func TestShouldStoreInBuffer(t *testing.T) {
	tests := []struct {
		name  string
		event busevents.Event
		want  bool
	}{
		{
			name:  "TemplateRenderedEvent is filtered (heavyweight)",
			event: &events.TemplateRenderedEvent{},
			want:  false,
		},
		{
			name:  "ValidationCompletedEvent is filtered (heavyweight)",
			event: &events.ValidationCompletedEvent{},
			want:  false,
		},
		{
			name:  "DeploymentScheduledEvent is filtered (heavyweight)",
			event: &events.DeploymentScheduledEvent{},
			want:  false,
		},
		{
			name:  "ReconciliationStartedEvent is stored",
			event: &events.ReconciliationStartedEvent{},
			want:  true,
		},
		{
			name:  "ConfigParsedEvent is stored",
			event: &events.ConfigParsedEvent{},
			want:  true,
		},
		{
			name:  "ReconciliationCompletedEvent is stored",
			event: &events.ReconciliationCompletedEvent{},
			want:  true,
		},
		{
			name:  "TemplateRenderFailedEvent is stored (failure events kept)",
			event: &events.TemplateRenderFailedEvent{},
			want:  true,
		},
		{
			name:  "ValidationFailedEvent is stored (failure events kept)",
			event: &events.ValidationFailedEvent{},
			want:  true,
		},
		{
			name:  "unknown event type is stored by default",
			event: mockEvent{eventType: "some.unknown.event"},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldStoreInBuffer(tt.event)
			assert.Equal(t, tt.want, got)
		})
	}
}
