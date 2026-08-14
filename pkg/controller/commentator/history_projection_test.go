package commentator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ctlevents "gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

type reconciliationLogWriter struct {
	bytes.Buffer
	commentator      *EventCommentator
	correlationID    string
	currentWasStored bool
}

func (w *reconciliationLogWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(`"msg":"Reconciliation"`)) {
		entries := w.commentator.ringBuffer.findByCorrelationID(w.correlationID, 0)
		w.currentWasStored = len(entries) > 0 && entries[0].eventType == ctlevents.EventTypeDeploymentCompleted
	}
	return w.Buffer.Write(p)
}

func TestEventCommentator_ProcessEventProjectsPayloadAndPreservesSummary(t *testing.T) {
	logWriter := &reconciliationLogWriter{}
	logger := slog.New(slog.NewJSONHandler(logWriter, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ec := NewEventCommentator(busevents.NewEventBus(8), logger, 8)
	logWriter.commentator = ec

	trigger := ctlevents.NewReconciliationTriggeredEvent("config_change", true, ctlevents.WithNewCorrelation())
	logWriter.correlationID = trigger.CorrelationID()
	corr := ctlevents.WithCorrelation(trigger.CorrelationID(), trigger.EventID())
	render := ctlevents.NewTemplateRenderedEvent(
		strings.Repeat("payload-not-history\n", 1024),
		nil,
		nil,
		nil,
		0,
		17,
		"config_change",
		"checksum",
		true,
		corr,
	)
	validation := ctlevents.NewValidationCompletedEvent(nil, 23, "config_change", nil, true, corr)
	deployment := ctlevents.NewDeploymentCompletedEvent(&ctlevents.DeploymentResult{
		Total:              2,
		Succeeded:          2,
		DurationMs:         31,
		ReloadsTriggered:   1,
		TotalAPIOperations: 4,
	}, corr)

	for _, event := range []busevents.Event{trigger, render, validation, deployment} {
		ec.processEvent(event)
	}

	entries := ec.ringBuffer.findByCorrelationID(trigger.CorrelationID(), 0)
	require.Len(t, entries, 4)
	assert.Equal(t, []string{
		ctlevents.EventTypeDeploymentCompleted,
		ctlevents.EventTypeValidationCompleted,
		ctlevents.EventTypeTemplateRendered,
		ctlevents.EventTypeReconciliationTriggered,
	}, []string{entries[0].eventType, entries[1].eventType, entries[2].eventType, entries[3].eventType})
	assert.Equal(t, int64(23), entries[1].durationMs)
	assert.Equal(t, int64(17), entries[2].durationMs)
	assert.Equal(t, "config_change", entries[3].trigger)
	assert.NotContains(t, fmt.Sprintf("%#v", entries), "payload-not-history")
	assert.True(t, logWriter.currentWasStored, "the deployment entry must exist before its log is generated")

	var reconciliationLog map[string]any
	for _, line := range bytes.Split(logWriter.Bytes(), []byte{'\n'}) {
		var record map[string]any
		if json.Unmarshal(line, &record) == nil && record["msg"] == "Reconciliation" {
			reconciliationLog = record
		}
	}
	require.NotNil(t, reconciliationLog)
	assert.Equal(t, "config_change", reconciliationLog["trigger"])
	assert.Equal(t, float64(17), reconciliationLog["render_ms"])
	assert.Equal(t, float64(23), reconciliationLog["validate_ms"])
	assert.Equal(t, float64(31), reconciliationLog["deploy_ms"])
}

func TestHistoryEntryCannotRetainEventPayloads(t *testing.T) {
	typeOfTime := reflect.TypeOf(time.Time{})
	typeOfEntry := reflect.TypeOf(historyEntry{})
	wantFields := map[string]reflect.Type{
		"eventType":     reflect.TypeOf(""),
		"timestamp":     typeOfTime,
		"correlated":    reflect.TypeOf(false),
		"eventID":       reflect.TypeOf(""),
		"correlationID": reflect.TypeOf(""),
		"causationID":   reflect.TypeOf(""),
		"trigger":       reflect.TypeOf(""),
		"durationMs":    reflect.TypeOf(int64(0)),
	}
	require.Equal(t, len(wantFields), typeOfEntry.NumField())

	for i := range typeOfEntry.NumField() {
		field := typeOfEntry.Field(i)
		assert.Equal(t, wantFields[field.Name], field.Type, field.Name)
	}

	backing := []byte(strings.Repeat("x", 8192))
	const reasonValue = "config_change"
	const correlationValue = "correlation-id-from-large-backing"
	const causationValue = "causation-id-from-large-backing"
	copy(backing[4096:], reasonValue)
	copy(backing[2048:], correlationValue)
	copy(backing[1024:], causationValue)
	reason := unsafe.String(&backing[4096], len(reasonValue))
	correlationID := unsafe.String(&backing[2048], len(correlationValue))
	causationID := unsafe.String(&backing[1024], len(causationValue))
	event := ctlevents.NewReconciliationTriggeredEvent(
		reason,
		true,
		ctlevents.WithCorrelation(correlationID, causationID),
	)
	entry := newHistoryEntry(event)

	clear(backing)
	assert.Equal(t, reasonValue, entry.trigger)
	assert.Equal(t, correlationValue, entry.correlationID)
	assert.Equal(t, causationValue, entry.causationID)
}
