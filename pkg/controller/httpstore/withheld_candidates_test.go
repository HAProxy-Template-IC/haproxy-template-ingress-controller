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

package httpstore

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

// A render that must not accept new content leaves an unaccepted source out as
// a failed non-critical fetch would, without fetching it, and asks for the
// acceptance attempt; accepted content still reads normally.
func TestWithheldRenderLeavesUnacceptedSourcesOut(t *testing.T) {
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "page-%d", requests.Add(1))
	}))
	defer server.Close()
	bus, logger := testutil.NewTestBusAndLogger()
	triggers := bus.Subscribe("withheld-test", 10)
	component := New(bus, logger, 0)
	bus.Start()

	withheld := NewHTTPStoreWrapper(WithCandidatesWithheld(t.Context()), component, logger, nil, SourceModeAuthoritative)
	content, err := withheld.Fetch(server.URL+"/page", map[string]any{"critical": false})
	require.NoError(t, err)
	assert.Equal(t, "", content)
	assert.True(t, withheld.CandidateWithheld(server.URL+"/page"))
	assert.False(t, withheld.InputTransaction().HasCandidates())
	assert.Equal(t, int32(0), requests.Load())
	testutil.WaitForEvent[*events.HTTPContentAcceptanceRequestedEvent](t, triggers, testutil.EventTimeout)

	_, err = withheld.Fetch(server.URL+"/critical", map[string]any{"critical": true})
	require.ErrorIs(t, err, ErrCandidateWithheld)
	withheld.InputTransaction().Abort()

	accepting := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	content, err = accepting.Fetch(server.URL+"/page", map[string]any{"critical": false})
	require.NoError(t, err)
	assert.Equal(t, "page-1", content)
	commitInputTransaction(t, accepting)
	component.RequestRenderForAcceptedContent()
	first := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, triggers, testutil.EventTimeout)
	assert.Equal(t, "http_content_accepted", first.Reason)
	assert.NotEmpty(t, first.CorrelationID())
	assert.Empty(t, first.CausationID())
	component.RequestRenderForAcceptedContent()
	second := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, triggers, testutil.EventTimeout)
	assert.NotEmpty(t, second.CorrelationID())
	assert.NotEqual(t, first.CorrelationID(), second.CorrelationID(), "independent render requests start new chains")

	again := NewHTTPStoreWrapper(WithCandidatesWithheld(t.Context()), component, logger, nil, SourceModeAuthoritative)
	content, err = again.Fetch(server.URL+"/page", map[string]any{"critical": false})
	require.NoError(t, err)
	assert.Equal(t, "page-1", content)
	assert.False(t, again.CandidateWithheld(server.URL+"/page"))
	again.InputTransaction().Abort()
}

// A revoked acceptance is reported, asks for the render without it, and leaves
// the source out of the next deploying render until it is accepted again.
func TestRevokedContentIsWithheldAgain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("page"))
	}))
	defer server.Close()
	bus, logger := testutil.NewTestBusAndLogger()
	published := bus.Subscribe("revocation-test", 10)
	component := New(bus, logger, 0)
	bus.Start()
	accepting := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, err := accepting.Fetch(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	commitInputTransaction(t, accepting)
	refusedRender := component.AcceptanceSequence()

	require.Equal(t, 1, component.RevokeAcceptances(refusedRender))

	revoked := testutil.WaitForEvent[*events.HTTPContentRevokedEvent](t, published, testutil.EventTimeout)
	assert.Equal(t, server.URL, revoked.URL)
	assert.True(t, revoked.Critical)
	assert.False(t, revoked.Restored)
	trigger := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, published, testutil.EventTimeout)
	assert.Equal(t, "http_content_revoked", trigger.Reason)
	assert.NotEmpty(t, trigger.CorrelationID())
	assert.Empty(t, trigger.CausationID())
	withheld := NewHTTPStoreWrapper(WithCandidatesWithheld(t.Context()), component, logger, nil, SourceModeAuthoritative)
	_, err = withheld.Fetch(server.URL, map[string]any{"critical": true})
	require.ErrorIs(t, err, ErrCandidateWithheld, "a critical source fails the render rather than deploying without it")
	withheld.InputTransaction().Abort()
	assert.Zero(t, component.RevokeAcceptances(refusedRender))
}
