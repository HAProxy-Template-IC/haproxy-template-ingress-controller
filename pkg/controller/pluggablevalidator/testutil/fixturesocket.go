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

// Package testutil provides a fixture unix-socket server for exercising the
// pluggablevalidator client without spinning up a real haproxy-spoa-hub
// sidecar. Tests script the canned response per request and inspect what
// the client wrote.
package testutil

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	pv "gitlab.com/haproxy-haptic/haptic/pkg/controller/pluggablevalidator"
)

// FixtureServer is a single-connection-per-request unix-socket server that
// replays a canned response for every accepted connection. Mirrors the
// hub-side behaviour from haproxy-spoa-hub specs/004-validate-mode.
//
// Use NewFixtureServer to start one at a tempdir-scoped socket path. Stop
// is idempotent and called automatically via t.Cleanup.
type FixtureServer struct {
	SocketPath string

	listener net.Listener

	mu               sync.Mutex
	cannedResponse   []byte // length-prefixed JSON to write back, set via SetResponse
	responseDelay    time.Duration
	responseGate     <-chan struct{}
	closeWithoutResp bool
	requests         [][]byte // bodies (without length prefix) of requests received
	requestReceived  chan struct{}

	wg     sync.WaitGroup
	stopCh chan struct{}
}

// NewFixtureServer starts a fixture server on a tempdir-scoped socket. The
// caller MUST call SetResponse before any client connects, or the server
// will close the connection without responding.
func NewFixtureServer(t *testing.T) *FixtureServer {
	t.Helper()
	// A unix socket path must fit in sun_path (~108 bytes). t.TempDir() embeds
	// the (often long) test name, which overflows that limit under a non-trivial
	// $TMPDIR (e.g. a sandboxed /tmp/<runner-id>/...), surfacing as
	// "bind: invalid argument". Use a short os.MkdirTemp dir + short socket name
	// so the bind succeeds regardless of test name / $TMPDIR length.
	dir, err := os.MkdirTemp("", "pvfix")
	if err != nil {
		t.Fatalf("create fixture socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "v.sock")

	return NewFixtureServerAtPath(t, socketPath)
}

// NewFixtureServerAtPath starts a fixture server at socketPath. It is useful
// for tests that stop one validator runtime and start another at the same
// configured path.
func NewFixtureServerAtPath(t *testing.T, socketPath string) *FixtureServer {
	t.Helper()
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix %s: %v", socketPath, err)
	}

	srv := &FixtureServer{
		SocketPath:      socketPath,
		listener:        listener,
		stopCh:          make(chan struct{}),
		requestReceived: make(chan struct{}, 1),
	}
	srv.wg.Add(1)
	go srv.serve()
	t.Cleanup(srv.Stop)
	return srv
}

// SetResponse records the JSON value the server will encode (length-prefixed)
// onto every subsequent connection. Pass nil to make the server close the
// connection without writing anything (simulates a server crash mid-request).
func (s *FixtureServer) SetResponse(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v == nil {
		s.cannedResponse = nil
		s.closeWithoutResp = true
		return nil
	}
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	length, err := pv.NarrowToUint32(len(body))
	if err != nil {
		return fmt.Errorf("response body length: %w", err)
	}
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[:4], length)
	copy(frame[4:], body)
	s.cannedResponse = frame
	s.closeWithoutResp = false
	return nil
}

// SetRawResponse records a fully-formed wire frame (length prefix included)
// to send back. Use to inject malformed responses or oversized frames.
func (s *FixtureServer) SetRawResponse(frame []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cannedResponse = append([]byte(nil), frame...)
	s.closeWithoutResp = false
}

// SetResponseDelay makes the server wait the given duration before writing
// the response. Use to test client-side timeouts.
func (s *FixtureServer) SetResponseDelay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responseDelay = d
}

// SetResponseGate blocks responses until gate is closed.
func (s *FixtureServer) SetResponseGate(gate <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responseGate = gate
}

// RequestReceived reports when the server has read a complete request.
func (s *FixtureServer) RequestReceived() <-chan struct{} {
	return s.requestReceived
}

// Requests returns a copy of the bodies received so far, in the order the
// connections were accepted.
func (s *FixtureServer) Requests() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.requests))
	for i, r := range s.requests {
		out[i] = append([]byte(nil), r...)
	}
	return out
}

// Stop closes the listener and waits for the accept goroutine to exit.
// Safe to call more than once.
func (s *FixtureServer) Stop() {
	select {
	case <-s.stopCh:
		return
	default:
		close(s.stopCh)
	}
	_ = s.listener.Close()
	s.wg.Wait()
}

func (s *FixtureServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.stopCh:
				return
			default:
				if errors.Is(err, net.ErrClosed) {
					return
				}
				// Test will hang if accept fails for an unexpected
				// reason; surface it via the listener close, which
				// the caller's Stop handles.
				return
			}
		}
		s.wg.Add(1)
		go s.handle(conn)
	}
}

func (s *FixtureServer) handle(conn net.Conn) {
	defer s.wg.Done()
	defer func() { _ = conn.Close() }()

	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		// Client hung up before sending the prefix — record nothing.
		return
	}
	length := binary.BigEndian.Uint32(header)
	body := make([]byte, length)
	if _, err := io.ReadFull(conn, body); err != nil {
		return
	}

	s.mu.Lock()
	s.requests = append(s.requests, body)
	delay := s.responseDelay
	gate := s.responseGate
	resp := s.cannedResponse
	closeWithoutResp := s.closeWithoutResp
	s.mu.Unlock()

	select {
	case s.requestReceived <- struct{}{}:
	default:
	}
	if gate != nil {
		select {
		case <-gate:
		case <-s.stopCh:
			return
		}
	}

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-s.stopCh:
			return
		}
	}
	if closeWithoutResp {
		return
	}
	if resp != nil {
		_, _ = conn.Write(resp)
	}
}
