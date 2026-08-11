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

package pluggablevalidator

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

type writeCountingConn struct {
	writes int
	closes int
}

func (c *writeCountingConn) Read([]byte) (int, error) { return 0, io.EOF }
func (c *writeCountingConn) Write(p []byte) (int, error) {
	c.writes++
	return len(p), nil
}
func (c *writeCountingConn) Close() error {
	c.closes++
	return nil
}
func (c *writeCountingConn) LocalAddr() net.Addr              { return testAddr("local") }
func (c *writeCountingConn) RemoteAddr() net.Addr             { return testAddr("remote") }
func (c *writeCountingConn) SetDeadline(time.Time) error      { return nil }
func (c *writeCountingConn) SetReadDeadline(time.Time) error  { return nil }
func (c *writeCountingConn) SetWriteDeadline(time.Time) error { return nil }

type testAddr string

func (a testAddr) Network() string { return "test" }
func (a testAddr) String() string  { return string(a) }

func validationRequest() *Request {
	return &Request{
		ProtocolVersion: ProtocolVersion,
		Files: []File{{
			Path:    "/etc/haproxy/haproxy.cfg",
			Content: "global\n    daemon\n",
		}},
	}
}

func TestCancelWatcherStopIsIdempotent(t *testing.T) {
	c := NewClient("test", "/tmp/test.sock", 0, 1)
	ctx, cancel := context.WithCancel(context.Background())
	stop := c.armCancelWatcher(ctx)

	stop()
	stop()
	cancel()
}

func TestCancelWatcherJoinsCancellationCallback(t *testing.T) {
	c := NewClient("test", "/tmp/test.sock", 0, 1)
	ctx, cancel := context.WithCancel(context.Background())
	stop := c.armCancelWatcher(ctx)
	cancel()

	stop()
}

func TestClientPreCanceledContextDoesNotAcquirePooledConnection(t *testing.T) {
	conn := &writeCountingConn{}
	client := NewClient("test", "/tmp/test.sock", time.Second, 1)
	client.idle = []*pooledConn{{conn: conn, parked: time.Now()}}

	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errors.New("validation authority expired"))
	resp, err := client.Validate(ctx, validationRequest())

	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if resp.Result != ResultError {
		t.Fatalf("result=%q want %q", resp.Result, ResultError)
	}
	if conn.writes != 0 {
		t.Fatalf("writes=%d want 0", conn.writes)
	}
	if len(client.idle) != 1 {
		t.Fatalf("idle connections=%d want 1", len(client.idle))
	}
}

func TestClientCancellationDuringDialDoesNotWrite(t *testing.T) {
	conn := &writeCountingConn{}
	client := NewClient("test", "/tmp/test.sock", time.Second, 1)
	ctx, cancel := context.WithCancelCause(context.Background())
	client.dialer = func(context.Context, string) (net.Conn, error) {
		cancel(errors.New("validation authority expired"))
		return conn, nil
	}

	resp, err := client.Validate(ctx, validationRequest())

	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if resp.Result != ResultError {
		t.Fatalf("result=%q want %q", resp.Result, ResultError)
	}
	if conn.writes != 0 {
		t.Fatalf("writes=%d want 0", conn.writes)
	}
	if conn.closes != 1 {
		t.Fatalf("closes=%d want 1", conn.closes)
	}
	if client.inFlight != 0 {
		t.Fatalf("in-flight connections=%d want 0", client.inFlight)
	}
}
