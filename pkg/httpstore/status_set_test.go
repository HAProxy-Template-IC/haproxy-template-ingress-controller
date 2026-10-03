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
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStatusSet(t *testing.T) {
	tests := []struct {
		name    string
		codes   []int
		want    []int
		wantErr string
	}{
		{name: "empty", codes: nil, want: []int{}},
		{name: "sorted and deduplicated", codes: []int{503, 404, 503}, want: []int{404, 503}},
		{name: "200 is implicit", codes: []int{200, 404}, want: []int{404}},
		{name: "bounds", codes: []int{100, 599}, want: []int{100, 599}},
		{name: "below range", codes: []int{99}, wantErr: "acceptStatus 99 is not an HTTP status"},
		{name: "above range", codes: []int{600}, wantErr: "acceptStatus 600 is not an HTTP status"},
		{name: "not modified", codes: []int{304}, wantErr: "reserved for conditional refresh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set, err := NewStatusSet(tt.codes...)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, set.Codes())
		})
	}
}

func TestDoFetchAcceptStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<h1>not here</h1>"))
	}))
	defer server.Close()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	accept404, err := NewStatusSet(404)
	require.NoError(t, err)
	content, err := New(logger, 0).Fetch(context.Background(), server.URL,
		FetchOptions{Critical: true, AcceptStatus: accept404}, nil)
	require.NoError(t, err)
	assert.Equal(t, "<h1>not here</h1>", content)

	accept503, err := NewStatusSet(503)
	require.NoError(t, err)
	_, err = New(logger, 0).Fetch(context.Background(), server.URL,
		FetchOptions{Critical: true, Retries: 1, AcceptStatus: accept503}, nil)
	require.ErrorContains(t, err, "resource not found (404 Not Found)")
}

func TestSourceIdentityIncludesAcceptStatus(t *testing.T) {
	plain, err := SourceIdentity(FetchOptions{}, nil)
	require.NoError(t, err)
	only200, err := NewStatusSet(200)
	require.NoError(t, err)
	withOnly200, err := SourceIdentity(FetchOptions{AcceptStatus: only200}, nil)
	require.NoError(t, err)
	assert.Equal(t, plain, withOnly200, "200 is always accepted, so listing it is the same source")

	forward, err := NewStatusSet(404, 503)
	require.NoError(t, err)
	reverse, err := NewStatusSet(503, 404, 404)
	require.NoError(t, err)
	forwardIdentity, err := SourceIdentity(FetchOptions{AcceptStatus: forward}, nil)
	require.NoError(t, err)
	reverseIdentity, err := SourceIdentity(FetchOptions{AcceptStatus: reverse}, nil)
	require.NoError(t, err)
	assert.Equal(t, forwardIdentity, reverseIdentity)
	assert.NotEqual(t, plain, forwardIdentity)
}
