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

package testrunner

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// A pending fixture stands for content a render left out because it waits
// for acceptance: empty content, and http.Pending says so.
func TestPendingHTTPFixtureRendersAsWaitingForAcceptance(t *testing.T) {
	fixtures := []config.HTTPResourceFixture{
		{URL: "http://pages/accepted", Content: "page"},
		{URL: "http://pages/pending", Pending: true},
	}
	wrapper := NewFixtureHTTPStoreWrapper(CreateHTTPStoreFromFixtures(fixtures, slog.Default()), slog.Default()).
		WithPending(fixtures)

	content, err := wrapper.Fetch("http://pages/pending")
	require.NoError(t, err)
	assert.Equal(t, "", content)
	assert.True(t, wrapper.Pending("http://pages/pending"))

	content, err = wrapper.Fetch("http://pages/accepted")
	require.NoError(t, err)
	assert.Equal(t, "page", content)
	assert.False(t, wrapper.Pending("http://pages/accepted"))
}
