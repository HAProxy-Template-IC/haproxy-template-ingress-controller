// Copyright 2025 Philipp Hossner
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

package client

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func traceAttrMap(t *testing.T, attrs []any) map[string]any {
	t.Helper()
	m := map[string]any{}
	for _, a := range attrs {
		attr, ok := a.(slog.Attr)
		require.True(t, ok, "attr %v is not a slog.Attr", a)
		m[attr.Key] = attr.Value.Any()
	}
	return m
}

func TestPushTrace_RecordsEveryPhaseOfACompletedRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	trace := newPushTrace()
	req, err := http.NewRequestWithContext(trace.context(context.Background()), http.MethodPost, srv.URL, strings.NewReader("global\n"))
	require.NoError(t, err)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	m := traceAttrMap(t, trace.attrs(7, nil))
	for _, phase := range []string{"got_conn_ms", "wrote_headers_ms", "wrote_request_ms", "first_response_byte_ms"} {
		assert.GreaterOrEqual(t, m[phase].(int64), int64(0), phase)
	}
	assert.Equal(t, int64(7), m["request_body_bytes"])
	assert.Nil(t, m["write_error"])
	assert.Nil(t, m["error"])
}

func TestPushTrace_MarksPhasesThatNeverHappened(t *testing.T) {
	// No request made: every phase is -1, which is the signature of a stall
	// before the connection was even obtained.
	m := traceAttrMap(t, newPushTrace().attrs(0, errors.New("context canceled")))
	for _, phase := range []string{"got_conn_ms", "wrote_headers_ms", "wrote_request_ms", "first_response_byte_ms"} {
		assert.Equal(t, int64(-1), m[phase], phase)
	}
	assert.EqualError(t, m["error"].(error), "context canceled")
}
