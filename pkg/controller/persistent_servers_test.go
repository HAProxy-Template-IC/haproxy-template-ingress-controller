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
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/debug"
)

type staticEventSource struct {
	events []debug.Event
}

func (s *staticEventSource) GetLast(limit int) []debug.Event {
	if limit >= len(s.events) {
		return s.events
	}
	return s.events[len(s.events)-limit:]
}

func (s *staticEventSource) FindByCorrelationID(correlationID string) []debug.Event {
	var result []debug.Event
	for _, event := range s.events {
		if event.CorrelationID == correlationID {
			result = append(result, event)
		}
	}
	return result
}

func TestPersistentEventsHandlerUsesCurrentIterationBuffer(t *testing.T) {
	infra := &persistentInfra{}
	first := &staticEventSource{events: []debug.Event{{Type: "first-iteration"}}}
	second := &staticEventSource{events: []debug.Event{{Type: "second-iteration"}}}
	handler := debug.EventsHandler(infra.repointEventSource(first))

	request := httptest.NewRequest(http.MethodGet, "/debug/events?limit=1", http.NoBody)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "first-iteration", decodeFirstEventType(t, response))

	infra.repointEventSource(second)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "second-iteration", decodeFirstEventType(t, response))
}

func decodeFirstEventType(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Events []debug.Event `json:"events"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
	require.Len(t, body.Events, 1)
	return body.Events[0].Type
}

func TestMonitorPersistentWebhookRunCancelsAfterPostBindFailure(t *testing.T) {
	procCtx, processCancel := context.WithCancel(context.Background())
	defer processCancel()
	iterCtx, iterationCancel := context.WithCancel(procCtx)
	defer iterationCancel()

	serverRun := newPersistentServerRun()
	group := &errgroup.Group{}
	monitorPersistentWebhookRun(
		procCtx,
		iterCtx,
		serverRun,
		group,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		iterationCancel,
	)

	serveErr := errors.New("serve failed after bind")
	serverRun.finish(serveErr)

	err := group.Wait()
	var persistentErr *persistentWebhookServerError
	require.ErrorAs(t, err, &persistentErr)
	require.ErrorIs(t, err, serveErr)
	require.NoError(t, procCtx.Err())
	require.ErrorIs(t, iterCtx.Err(), context.Canceled)
}

func TestPersistentProcessServerFailureCancelsProcess(t *testing.T) {
	procCtx, processCancel := context.WithCancel(context.Background())
	defer processCancel()
	infra := &persistentInfra{processCancel: processCancel}
	started := make(chan struct{})
	release := make(chan struct{})
	serveErr := errors.New("listener failed")

	run := infra.startProcessServer(
		procCtx,
		"test",
		func(context.Context) error {
			close(started)
			<-release
			return serveErr
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	<-started
	close(release)

	select {
	case <-procCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("process was not cancelled after its server stopped")
	}
	require.ErrorIs(t, run.Wait(), serveErr)
}

func TestPersistentServerJoinUsesOneSharedBudget(t *testing.T) {
	first := newPersistentServerRun()
	second := newPersistentServerRun()
	infra := &persistentInfra{introspectionRun: first, metricsRun: second}
	serveErr := errors.New("metrics listener failed")
	second.finish(serveErr)

	err := infra.waitForPersistentServers(20 * time.Millisecond)
	require.ErrorContains(t, err, "process shutdown budget")
	require.ErrorIs(t, err, serveErr)

	first.finish(nil)
}

func TestPersistentServerJoinCollectsCompletedErrorAtTimeout(t *testing.T) {
	first := newPersistentServerRun()
	second := newPersistentServerRun()
	infra := &persistentInfra{introspectionRun: first, metricsRun: second}
	serveErr := errors.New("introspection listener failed")
	first.finish(serveErr)

	err := infra.waitForPersistentServers(0)
	require.ErrorContains(t, err, "process shutdown budget")
	require.ErrorIs(t, err, serveErr)

	second.finish(nil)
}

func TestPersistentServerJoinDoesNotTimeOutAfterEveryServerStopped(t *testing.T) {
	first := newPersistentServerRun()
	second := newPersistentServerRun()
	infra := &persistentInfra{introspectionRun: first, metricsRun: second}
	firstErr := errors.New("introspection listener failed")
	secondErr := errors.New("metrics listener failed")
	first.finish(firstErr)
	second.finish(secondErr)

	err := infra.waitForPersistentServers(0)
	require.ErrorIs(t, err, firstErr)
	require.ErrorIs(t, err, secondErr)
	require.NotContains(t, err.Error(), "process shutdown budget")
}
