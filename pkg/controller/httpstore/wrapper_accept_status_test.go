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
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	purehttpstore "gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

func TestParseFetchOptions_AcceptStatus(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		want    []int
		wantErr string
	}{
		{name: "template ints", value: []any{503, 404}, want: []int{404, 503}},
		{name: "typed ints", value: []int{404}, want: []int{404}},
		{name: "whole floats from JSON", value: []any{404.0}, want: []int{404}},
		{name: "200 is implicit", value: []any{200}, want: []int{}},
		{name: "empty list", value: []any{}, want: []int{}},
		{name: "not a list", value: 404, wantErr: "expected a list of status codes"},
		{name: "string element", value: []any{"404"}, wantErr: "expected a whole number"},
		{name: "fractional", value: []any{404.5}, wantErr: "expected a whole number"},
		{name: "out of range", value: []any{600}, wantErr: "acceptStatus 600 is not an HTTP status"},
		{name: "not modified", value: []any{304}, wantErr: "reserved for conditional refresh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := parseFetchOptions(map[string]any{"acceptStatus": tt.value})
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, opts.AcceptStatus.Codes())
		})
	}
}

func TestParseFetchArgs_AcceptStatusIsPartOfSourceIdentity(t *testing.T) {
	describe := func(options map[string]any) purehttpstore.SourceDescriptor {
		t.Helper()
		_, opts, auth, err := ParseFetchArgs([]any{"http://example.test/", options})
		require.NoError(t, err)
		descriptor, err := purehttpstore.DescribeSource(opts, auth)
		require.NoError(t, err)
		return descriptor
	}
	assert.Equal(t, describe(map[string]any{"acceptStatus": []any{404, 503}}),
		describe(map[string]any{"acceptStatus": []any{503, 404}}))
	assert.NotEqual(t, describe(map[string]any{}), describe(map[string]any{"acceptStatus": []any{404}}))
}

func TestHTTPStoreWrapper_FetchAcceptStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("maintenance page"))
	}))
	defer server.Close()
	bus, logger := testutil.NewTestBusAndLogger()

	accepting := NewHTTPStoreWrapper(context.Background(), New(bus, logger, 0), logger, nil, SourceModeReadOnly)
	content, err := accepting.Fetch(server.URL, map[string]any{"acceptStatus": []any{503}, "critical": true})
	require.NoError(t, err)
	assert.Equal(t, "maintenance page", content)

	rejecting := NewHTTPStoreWrapper(context.Background(), New(bus, logger, 0), logger, nil, SourceModeReadOnly)
	_, err = rejecting.Fetch(server.URL, map[string]any{"retries": 1, "critical": true})
	require.ErrorContains(t, err, "server error: 503")

	invalid := NewHTTPStoreWrapper(context.Background(), New(bus, logger, 0), logger, nil, SourceModeReadOnly)
	_, err = invalid.Fetch(server.URL, map[string]any{"acceptStatus": []any{99}})
	require.ErrorContains(t, err, "acceptStatus 99 is not an HTTP status")
}
