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

// Package tunnel provides stall detection for kubectl port-forward tunnels.
package tunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// Watch probes a kubectl port-forward tunnel and calls kill when the tunnel
// is wedged: the local port still accepts TCP connections (kubectl is alive)
// but forwarded requests never complete. That failure mode is invisible to
// exit-based supervision — kubectl only exits on hard upstream errors, not on
// a stalled apiserver stream — and it fails every request until the tunnel is
// replaced (observed in CI on MR !1306: HAProxy healthy and serving on both
// pods, zero requests arriving through the tunnel for the whole 15s budget).
//
// httpPort is probed with a plain HTTP request; ANY status code is a healthy
// tunnel — HAProxy answering 404 still proves end-to-end forwarding. When
// httpPort is 0, httpsPort is probed with an insecure TLS handshake instead.
// A refused connection is NOT a strike: it means the process is dead or
// restarting, which exit-based supervision already handles. After `strikes`
// consecutive probe timeouts kill is invoked once and the count resets, so a
// replacement tunnel starts with a fresh budget.
//
// Strikes are scoped to one process: id() identifies the process currently
// serving the tunnel, an identity change resets the count, and the identity
// the strikes were counted against is passed to kill so the caller can refuse
// to kill a newer process — the supervisor may swap tunnels at any moment.
//
// Watch returns when ctx is done. onEvent receives one line per kill for the
// caller to log.
func Watch(ctx context.Context, httpPort, httpsPort int, interval, probeTimeout time.Duration, strikes int, id func() any, kill func(id any), onEvent func(string)) {
	misses := 0
	var struck any
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		healthy, refused := probe(ctx, httpPort, httpsPort, probeTimeout)
		if healthy || refused {
			misses = 0
			continue
		}
		// Strikes are per-process: a replacement tunnel must stall on its
		// own before it is killed.
		if cur := id(); cur != struck {
			struck = cur
			misses = 0
		}
		misses++
		if misses >= strikes {
			onEvent(fmt.Sprintf(
				"tunnel stalled (%d consecutive probe timeouts; local port accepts but nothing is forwarded) — killing kubectl so the supervisor restarts it",
				misses))
			kill(struck)
			misses = 0
		}
	}
}

// probe reports (healthy, refused). healthy means a forwarded request
// completed end to end; refused means the local port did not accept at all.
// Anything else (typically a whole-probe timeout) is a stall indication.
func probe(ctx context.Context, httpPort, httpsPort int, timeout time.Duration) (healthy, refused bool) {
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if httpPort != 0 {
		req, err := http.NewRequestWithContext(pctx, http.MethodGet,
			fmt.Sprintf("http://127.0.0.1:%d/", httpPort), http.NoBody)
		if err != nil {
			return false, false
		}
		// Fresh connection per probe: a pooled connection would test the
		// pool, not the tunnel's ability to open and serve a new stream.
		tr := &http.Transport{DisableKeepAlives: true}
		defer tr.CloseIdleConnections()
		resp, err := (&http.Client{Transport: tr}).Do(req)
		if err == nil {
			_ = resp.Body.Close()
			return true, false
		}
		return false, errors.Is(err, syscall.ECONNREFUSED)
	}

	// TLS listener: a completed handshake proves forwarding without needing
	// a certificate we could verify.
	d := tls.Dialer{
		NetDialer: &net.Dialer{},
		Config: &tls.Config{
			InsecureSkipVerify: true, // #nosec G402 — handshake-only probe of a local test tunnel, no data sent
		},
	}
	conn, err := d.DialContext(pctx, "tcp", fmt.Sprintf("127.0.0.1:%d", httpsPort))
	if err == nil {
		_ = conn.Close()
		return true, false
	}
	return false, errors.Is(err, syscall.ECONNREFUSED)
}
