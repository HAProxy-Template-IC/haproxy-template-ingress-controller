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

package renderer

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestIncrementalResourceDeriverRecordsExactChain(t *testing.T) {
	source := incrementalDerivationTestSource()
	raw := encodeIncrementalDerivationTestValue(t, source)
	wantSource := append([]byte(nil), raw...)
	deriver, err := newIncrementalResourceDeriver("routes", "default", "route", raw)
	require.NoError(t, err)

	raw[0] = '['
	first, err := deriver.DeriveResource("routes", source, "spec.mode", "governed")
	require.NoError(t, err)
	second, err := deriver.DeriveResource("routes", first, "spec.enabled", true)
	require.NoError(t, err)

	derivations := deriver.freeze()
	require.Len(t, derivations, 1)
	assert.Equal(t, rendercontext.DerivedResourceIdentity{
		Resource:  "routes",
		Namespace: "default",
		Name:      "route",
	}, derivations[0].Identity)
	assert.Equal(t, wantSource, derivations[0].Source)
	assert.JSONEq(t, string(encodeIncrementalDerivationTestValue(t, second)), string(derivations[0].Value))
	assert.Equal(t, "original", source["spec"].(map[string]any)["mode"])

	derivations[0].Source[0] = '['
	derivations[0].Value[0] = '['
	stable := deriver.freeze()
	require.Len(t, stable, 1)
	assert.Equal(t, wantSource, stable[0].Source)
	assert.JSONEq(t, string(encodeIncrementalDerivationTestValue(t, second)), string(stable[0].Value))

	_, err = deriver.DeriveResource("routes", second, "spec.afterFreeze", true)
	require.ErrorIs(t, err, rendercontext.ErrDerivedResourceViewFrozen)
}

func TestIncrementalResourceDeriverRejectsInvalidChainsWithoutPoisoning(t *testing.T) {
	tests := map[string]struct {
		prepare func(*testing.T, *incrementalResourceDeriver, map[string]any) any
		attempt func(*testing.T, *incrementalResourceDeriver, map[string]any, any) error
		wantErr string
		isStale bool
	}{
		"another source": {
			attempt: func(_ *testing.T, deriver *incrementalResourceDeriver, _ map[string]any, current any) error {
				_, err := deriver.DeriveResource("other", current, "spec.rejected", true)
				return err
			},
			wantErr: `bound to source "routes", not "other"`,
		},
		"another identity": {
			attempt: func(t *testing.T, deriver *incrementalResourceDeriver, _ map[string]any, current any) error {
				t.Helper()
				forged := cloneIncrementalDerivationTestValue(t, current)
				forged["metadata"].(map[string]any)["name"] = "other"
				_, err := deriver.DeriveResource("routes", forged, "spec.rejected", true)
				return err
			},
			wantErr: "source identity is default/other, expected default/route",
		},
		"forged initial value": {
			attempt: func(t *testing.T, deriver *incrementalResourceDeriver, _ map[string]any, current any) error {
				t.Helper()
				forged := cloneIncrementalDerivationTestValue(t, current)
				forged["spec"].(map[string]any)["forged"] = true
				_, err := deriver.DeriveResource("routes", forged, "spec.rejected", true)
				return err
			},
			wantErr: rendercontext.ErrDerivedResourceStale.Error(),
			isStale: true,
		},
		"stale predecessor": {
			prepare: func(t *testing.T, deriver *incrementalResourceDeriver, source map[string]any) any {
				t.Helper()
				current, err := deriver.DeriveResource("routes", source, "spec.first", true)
				require.NoError(t, err)
				return current
			},
			attempt: func(_ *testing.T, deriver *incrementalResourceDeriver, source map[string]any, _ any) error {
				_, err := deriver.DeriveResource("routes", source, "spec.rejected", true)
				return err
			},
			wantErr: rendercontext.ErrDerivedResourceStale.Error(),
			isStale: true,
		},
		"identity change": {
			attempt: func(_ *testing.T, deriver *incrementalResourceDeriver, _ map[string]any, current any) error {
				_, err := deriver.DeriveResource("routes", current, "metadata.name", "other")
				return err
			},
			wantErr: "cannot change resource identity",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			source := incrementalDerivationTestSource()
			deriver, err := newIncrementalResourceDeriver(
				"routes",
				"default",
				"route",
				encodeIncrementalDerivationTestValue(t, source),
			)
			require.NoError(t, err)
			current := any(source)
			if test.prepare != nil {
				current = test.prepare(t, deriver, source)
			}
			err = test.attempt(t, deriver, source, current)
			require.ErrorContains(t, err, test.wantErr)
			if test.isStale {
				require.ErrorIs(t, err, rendercontext.ErrDerivedResourceStale)
			}
			_, err = deriver.DeriveResource("routes", current, "spec.accepted", true)
			require.NoError(t, err)
		})
	}
}

func TestNewIncrementalResourceDeriverValidatesSource(t *testing.T) {
	valid := encodeIncrementalDerivationTestValue(t, incrementalDerivationTestSource())
	tests := map[string]struct {
		source    string
		namespace string
		name      string
		raw       []byte
		wantErr   string
	}{
		"missing alias": {
			namespace: "default",
			name:      "route",
			raw:       valid,
			wantErr:   "requires a source and name",
		},
		"missing bound name": {
			source:    "routes",
			namespace: "default",
			raw:       valid,
			wantErr:   "requires a source and name",
		},
		"invalid JSON": {
			source:    "routes",
			namespace: "default",
			name:      "route",
			raw:       []byte(`{"metadata":`),
			wantErr:   "not valid JSON",
		},
		"noncanonical JSON": {
			source:    "routes",
			namespace: "default",
			name:      "route",
			raw:       append(append([]byte(nil), valid...), '\n'),
			wantErr:   "not canonical JSON",
		},
		"missing source identity": {
			source:  "routes",
			name:    "route",
			raw:     []byte(`{"spec":{}}`),
			wantErr: "has no metadata.name",
		},
		"mismatched source identity": {
			source:    "routes",
			namespace: "default",
			name:      "other",
			raw:       valid,
			wantErr:   "source identity is default/route, expected default/other",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := newIncrementalResourceDeriver(test.source, test.namespace, test.name, test.raw)
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestIncrementalRecorderEventsMatchEventCollector(t *testing.T) {
	events := []templating.RenderedEvent{
		{
			Namespace: "z", Name: "route", APIVersion: "example.io/v1", Kind: "Route",
			Type: templating.EventTypeWarning, Reason: "Rejected", Message: "z",
		},
		{
			Namespace: "a", Name: "route", APIVersion: "example.io/v1", Kind: "Route",
			Type: templating.EventTypeNormal, Reason: "Accepted", Message: "a",
		},
	}
	recorder := &incrementalRecorder{}
	wantCollector := templating.NewEventCollector()
	for _, event := range append(events, events[0]) {
		require.NoError(t, recorder.RecordEvent(
			event.Namespace,
			event.Name,
			event.APIVersion,
			event.Kind,
			event.Type,
			event.Reason,
			event.Message,
		))
		require.NoError(t, wantCollector.Register(
			event.Namespace,
			event.Name,
			event.APIVersion,
			event.Kind,
			event.Type,
			event.Reason,
			event.Message,
		))
	}

	result, err := recorder.result("")
	require.NoError(t, err)
	assert.Equal(t, wantCollector.Events(), result.Events)
	require.Len(t, result.Events, 2)
	result.Events[0].Message = "mutated"
	repeated, err := recorder.result("")
	require.NoError(t, err)
	assert.Equal(t, wantCollector.Events(), repeated.Events)
}

func TestIncrementalRecorderRejectsInvalidEventsLikeEventCollector(t *testing.T) {
	tests := map[string]templating.RenderedEvent{
		"identity": {Type: templating.EventTypeWarning, Reason: "Rejected", Message: "message"},
		"reason": {
			Name: "route", APIVersion: "example.io/v1", Kind: "Route",
			Type: templating.EventTypeWarning, Message: "message",
		},
		"type": {
			Name: "route", APIVersion: "example.io/v1", Kind: "Route",
			Type: "Invalid", Reason: "Rejected", Message: "message",
		},
	}
	for name, event := range tests {
		t.Run(name, func(t *testing.T) {
			recorder := &incrementalRecorder{}
			got := recorder.RecordEvent(
				event.Namespace,
				event.Name,
				event.APIVersion,
				event.Kind,
				event.Type,
				event.Reason,
				event.Message,
			)
			want := templating.NewEventCollector().Register(
				event.Namespace,
				event.Name,
				event.APIVersion,
				event.Kind,
				event.Type,
				event.Reason,
				event.Message,
			)
			require.EqualError(t, got, want.Error())
			result, err := recorder.result("")
			require.NoError(t, err)
			assert.Empty(t, result.Events)
		})
	}
}

func TestIncrementalRecorderConcurrentEventsAreDeduplicated(t *testing.T) {
	recorder := &incrementalRecorder{}
	var wait sync.WaitGroup
	eventErrors := make(chan error, 64)
	for range 64 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			eventErrors <- recorder.RecordEvent(
				"default",
				"route",
				"example.io/v1",
				"Route",
				templating.EventTypeWarning,
				"Rejected",
				"message",
			)
		}()
	}
	wait.Wait()
	close(eventErrors)
	for err := range eventErrors {
		require.NoError(t, err)
	}
	result, err := recorder.result("")
	require.NoError(t, err)
	assert.Len(t, result.Events, 1)
}

func TestIncrementalRecorderResultDetachesEffects(t *testing.T) {
	source := incrementalDerivationTestSource()
	deriver, err := newIncrementalResourceDeriver(
		"routes",
		"default",
		"route",
		encodeIncrementalDerivationTestValue(t, source),
	)
	require.NoError(t, err)
	_, err = deriver.DeriveResource("routes", source, "spec.mode", "governed")
	require.NoError(t, err)
	recorder := &incrementalRecorder{deriver: deriver}
	require.NoError(t, recorder.RecordEvent(
		"default",
		"route",
		"example.io/v1",
		"Route",
		templating.EventTypeWarning,
		"Governed",
		"changed",
	))

	first, err := recorder.result("")
	require.NoError(t, err)
	require.Len(t, first.Derivations, 1)
	require.Len(t, first.Events, 1)
	encoded, err := json.Marshal(first)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"derivations"`)
	assert.Contains(t, string(encoded), `"events"`)

	first.Derivations[0].Source[0] = '['
	first.Derivations[0].Value[0] = '['
	first.Events[0].Message = "mutated"
	second, err := recorder.result("")
	require.NoError(t, err)
	assert.True(t, json.Valid(second.Derivations[0].Source))
	assert.True(t, json.Valid(second.Derivations[0].Value))
	assert.Equal(t, "changed", second.Events[0].Message)
}

func incrementalDerivationTestSource() map[string]any {
	return map[string]any{
		"apiVersion": "example.io/v1",
		"kind":       "Route",
		"metadata": map[string]any{
			"namespace": "default",
			"name":      "route",
		},
		"spec": map[string]any{"mode": "original"},
	}
}

func encodeIncrementalDerivationTestValue(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}

func cloneIncrementalDerivationTestValue(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded := encodeIncrementalDerivationTestValue(t, value)
	var result map[string]any
	require.NoError(t, json.Unmarshal(encoded, &result))
	return result
}
