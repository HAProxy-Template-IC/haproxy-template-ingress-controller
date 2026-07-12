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

//go:build e2e

package httpclient

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

type connGenKey struct{}

// Polling must observe cluster state as it converges — but a pooled
// keep-alive connection is pinned to the HAProxy worker generation it was
// opened against (hitless reloads keep old workers serving established
// connections with the OLD routing tables). This models that: connections
// opened at generation 0 answer 404 forever; connections opened after the
// simulated deploy answer 200. The poll converges only if retries re-dial.
func TestPollRetriesOnFreshConnection(t *testing.T) {
	var gen atomic.Int64      // bumped once = "the route deployed / HAProxy reloaded"
	var conns atomic.Int64    // distinct TCP connections the server accepted
	var requests atomic.Int64 // total requests served

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		// Flip the generation after the first request, so the connection
		// that carried it is forever pre-deploy.
		defer gen.CompareAndSwap(0, 1)
		if g, _ := r.Context().Value(connGenKey{}).(int64); g < 1 {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	srv.Config.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		conns.Add(1)
		return context.WithValue(ctx, connGenKey{}, gen.Load())
	}
	srv.Start()
	t.Cleanup(srv.Close)
	port, err := strconv.Atoi(srv.URL[len("http://127.0.0.1:"):])
	if err != nil {
		t.Fatal(err)
	}

	c := ForForwarded(t, port, 0)
	c.waitCfg = testutil.WaitConfig{
		Timeout:         5 * time.Second,
		InitialInterval: 20 * time.Millisecond,
		MaxInterval:     50 * time.Millisecond,
	}
	c.onPollTimeout = nil // no cluster to snapshot in this unit test

	c.GET("pinned.example.test", "/").ExpectOK(t)

	if got := conns.Load(); got < 2 {
		t.Fatalf("poll succeeded over %d connection(s); convergence requires a re-dial (old connection can only 404)", got)
	}
}
