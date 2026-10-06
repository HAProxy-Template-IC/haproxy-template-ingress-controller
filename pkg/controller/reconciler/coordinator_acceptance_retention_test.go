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

package reconciler

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"testing"
	"time"
	"weak"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

func TestDelayedGateVerdictsKeepTheirAcceptanceWindow(t *testing.T) {
	for _, passed := range []bool{false, true} {
		t.Run(strconv.FormatBool(passed), func(t *testing.T) {
			page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("page"))
			}))
			t.Cleanup(page.Close)
			h := newAcceptanceHarness(t, page.URL, false)
			h.reconcile("start")
			h.deployedFor("start")
			first := h.deployedFor("http_content_accepted")
			h.waitForAttempts(0)
			require.Equal(t, "a=page\n", first.config)
			for index := range 64 {
				reason := "render while the first check is running " + strconv.Itoa(index)
				h.reconcile(reason)
				require.Equal(t, first.config, h.deployedFor(reason).config)
			}
			h.verdict(first, passed)
			if !passed {
				assert.Equal(t, "a=\n", h.deployedFor("http_content_revoked").config)
				_, accepted := h.component.GetStore().Get(page.URL)
				assert.False(t, accepted)
				return
			}
			require.NoError(t, h.routes.Update(h.route(page.URL, "!"), []string{"default", "a"}))
			h.reconcile("broken change after the delayed pass")
			refused := h.deployedFor("broken change after the delayed pass")
			require.Equal(t, "a=page!\n", refused.config)
			h.verdict(refused, false)
			h.reconcile("after refusal")
			assert.Equal(t, "a=page!\n", h.deployedFor("after refusal").config)
		})
	}
}

func TestAcceptanceLedgerDoesNotRetainRenderedOccurrences(t *testing.T) {
	ledger := newAcceptanceLedger()
	reference := func() weak.Pointer[rendercycle.Occurrence] {
		cycle := testutil.NewRenderCycleFixture(t).Snapshot(t, "test config", nil, nil)
		occurrence, err := rendercycle.NewOccurrence(cycle)
		require.NoError(t, err)
		ledger.record(occurrence, acceptanceWindow{reached: 1})
		return weak.Make(occurrence)
	}()
	runtime.GC()
	assert.Nil(t, reference.Value(), "the ledger must not own a rendered snapshot")
	require.Eventually(t, func() bool {
		ledger.mu.Lock()
		defer ledger.mu.Unlock()
		return len(ledger.windows) == 0
	}, testutil.EventTimeout, time.Millisecond)
	runtime.KeepAlive(ledger)
}
