// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchManifest(t *testing.T) {
	saved := manifestFetchWait
	manifestFetchWait.InitialInterval, manifestFetchWait.MaxInterval = time.Millisecond, time.Millisecond
	t.Cleanup(func() { manifestFetchWait = saved })

	tests := []struct {
		name         string
		statuses     []int
		wantErr      bool
		wantAttempts int32
	}{
		{name: "retries gateway timeouts", statuses: []int{http.StatusGatewayTimeout, http.StatusTooManyRequests, http.StatusOK}, wantAttempts: 3},
		{name: "fails fast on not found", statuses: []int{http.StatusNotFound, http.StatusOK}, wantErr: true, wantAttempts: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				status := tt.statuses[attempts.Add(1)-1]
				w.WriteHeader(status)
				if status == http.StatusOK {
					_, _ = w.Write([]byte("kind: Namespace"))
				}
			}))
			t.Cleanup(server.Close)

			content, err := fetchManifest(context.Background(), server.URL)
			assert.Equal(t, tt.wantAttempts, attempts.Load())
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "kind: Namespace", string(content))
		})
	}
}
