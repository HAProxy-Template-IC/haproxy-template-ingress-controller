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

package tunnel

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// watchParams keeps the unit tests fast: a full stall must be detected well
// under a second so the suite stays within the repo's test-time budget.
const (
	testInterval = 50 * time.Millisecond
	testProbe    = 100 * time.Millisecond
	testStrikes  = 2
)

func runWatch(ctx context.Context, t *testing.T, httpPort int, id func() any) (kills *atomic.Int32, lastID *atomic.Value) {
	t.Helper()
	kills = &atomic.Int32{}
	lastID = &atomic.Value{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		Watch(ctx, httpPort, 0, testInterval, testProbe, testStrikes, id,
			func(killed any) {
				lastID.Store(killed)
				kills.Add(1)
			},
			func(msg string) { t.Log(msg) })
	}()
	t.Cleanup(func() { <-done })
	return kills, lastID
}

// stalledListener accepts connections and never responds, modeling the
// stalled kubectl port-forward: local TCP accept succeeds, forwarded stream
// is dead. Accepted conns are kept referenced on purpose — net.Conn carries
// an fd finalizer, so dropping the reference would let a GC cycle close the
// socket and turn the stall into a reset mid-test.
func stalledListener(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		var held []net.Conn
		defer func() {
			for _, c := range held {
				_ = c.Close()
			}
		}()
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			held = append(held, conn) // hold open, never respond
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestWatchKillsStalledTunnel(t *testing.T) {
	port := stalledListener(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const procID = "tunnel-1"
	kills, lastID := runWatch(ctx, t, port, func() any { return procID })

	deadline := time.After(3 * time.Second)
	for kills.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("watchdog never killed the stalled tunnel")
		case <-time.After(20 * time.Millisecond):
		}
	}
	if got := lastID.Load(); got != procID {
		t.Fatalf("kill received identity %v, want the probed process %v", got, procID)
	}
	cancel()
}

// A tunnel whose process identity changes on every probe must never be
// killed: strikes are per-process, and each fresh process deserves its own
// budget. This is the supervisor-already-restarted race, made deterministic.
func TestWatchIdentityChangeResetsStrikes(t *testing.T) {
	port := stalledListener(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var gen atomic.Int64
	kills, _ := runWatch(ctx, t, port, func() any { return gen.Add(1) })

	<-ctx.Done()
	if got := kills.Load(); got != 0 {
		t.Fatalf("watchdog killed %d time(s) despite per-probe identity changes", got)
	}
}

// A healthy tunnel (any HTTP response, even 404) must never be killed.
func TestWatchLeavesHealthyTunnelAlone(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	port, _ := strconv.Atoi(srv.URL[len("http://127.0.0.1:"):])

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	kills, _ := runWatch(ctx, t, port, func() any { return "p" })

	<-ctx.Done()
	if got := kills.Load(); got != 0 {
		t.Fatalf("watchdog killed a healthy tunnel %d time(s)", got)
	}
}

// A refused port means the process is dead or restarting — that is the
// exit-supervisor's job, and killing would race its restart.
func TestWatchIgnoresRefusedConnections(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close() // free the port so probes are refused

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	kills, _ := runWatch(ctx, t, port, func() any { return "p" })

	<-ctx.Done()
	if got := kills.Load(); got != 0 {
		t.Fatalf("watchdog killed on refused connections %d time(s)", got)
	}
}
