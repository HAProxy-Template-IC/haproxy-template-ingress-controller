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
	"log/slog"
	"net/http/httptrace"
	"sync"
	"time"
)

// slowPushThreshold is the point past which a configuration push gets its HTTP
// phase timings logged. A raw push completes in well under a second even at
// 15k lines, so anything slower is a stall worth locating: connection wait,
// request write, or the wait for the first response byte.
const slowPushThreshold = 5 * time.Second

// pushTrace records the client-side HTTP phases of one configuration push.
// Populated from httptrace callbacks, which the transport may invoke from
// other goroutines, hence the mutex.
type pushTrace struct {
	mu        sync.Mutex
	start     time.Time
	gotConn   time.Time
	reused    bool
	wasIdle   bool
	idleTime  time.Duration
	wroteHdrs time.Time
	wroteReq  time.Time
	wroteErr  error
	firstByte time.Time
}

func newPushTrace() *pushTrace {
	return &pushTrace{start: time.Now()}
}

func (t *pushTrace) context(ctx context.Context) context.Context {
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			t.mu.Lock()
			defer t.mu.Unlock()
			t.gotConn, t.reused, t.wasIdle, t.idleTime = time.Now(), info.Reused, info.WasIdle, info.IdleTime
		},
		WroteHeaders: func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			t.wroteHdrs = time.Now()
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			t.mu.Lock()
			defer t.mu.Unlock()
			t.wroteReq, t.wroteErr = time.Now(), info.Err
		},
		GotFirstResponseByte: func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			t.firstByte = time.Now()
		},
	})
}

// attrs returns the phase timings as log attributes, in milliseconds since
// the push started; -1 marks a phase that never happened, which is the
// diagnostic value when a push stalls.
func (t *pushTrace) attrs(bodyBytes int, err error) []any {
	t.mu.Lock()
	defer t.mu.Unlock()
	since := func(ts time.Time) int64 {
		if ts.IsZero() {
			return -1
		}
		return ts.Sub(t.start).Milliseconds()
	}
	return []any{
		slog.Int64("total_ms", time.Since(t.start).Milliseconds()),
		slog.Int64("got_conn_ms", since(t.gotConn)),
		slog.Bool("conn_reused", t.reused),
		slog.Bool("conn_was_idle", t.wasIdle),
		slog.Int64("conn_idle_ms", t.idleTime.Milliseconds()),
		slog.Int64("wrote_headers_ms", since(t.wroteHdrs)),
		slog.Int64("wrote_request_ms", since(t.wroteReq)),
		slog.Any("write_error", t.wroteErr),
		slog.Int64("first_response_byte_ms", since(t.firstByte)),
		slog.Int("request_body_bytes", bodyBytes),
		slog.Any("error", err),
	}
}
