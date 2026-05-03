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
	"fmt"
	"net"
	"os"
	"time"
)

// DefaultTimeout matches the hub-side default (--validate-timeout-ms) so
// operators see consistent latency budgets across both ends of the wire.
const DefaultTimeout = 5 * time.Second

// Client is a synchronous unix-socket client speaking the validator wire
// protocol. One Client maps to one configured validator (one socket path,
// one timeout). Clients are safe for concurrent use; each Validate call
// opens its own connection per the wire protocol.
type Client struct {
	// Name is the operator-facing validator name from spec.validators[i].name.
	// Surfaced in synthetic protocol-level diagnostics so users can identify
	// which validator failed.
	Name string

	// SocketPath is the absolute filesystem path to the validator's unix
	// domain socket.
	SocketPath string

	// Timeout is the per-call deadline covering connect + write + read.
	// Zero falls back to DefaultTimeout.
	Timeout time.Duration

	// dialer is overridable in tests. nil means use the default unix
	// dialer.
	dialer func(ctx context.Context, path string) (net.Conn, error)
}

// NewClient builds a Client for the given validator socket.
func NewClient(name, socketPath string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{Name: name, SocketPath: socketPath, Timeout: timeout}
}

// Validate sends one request frame and reads one response frame, then closes
// the connection. Returns the decoded Response or, on transport / protocol
// failure, a synthetic ProtocolError Response.
//
// Validate never returns a non-nil error alongside a non-nil Response: the
// caller treats every transport failure as a protocol-level error
// diagnostic. The error return is reserved for caller-supplied invariant
// violations (nil request, etc.) where the failure is "you misused this
// API" rather than "the validator is unreachable".
func (c *Client) Validate(ctx context.Context, req *Request) (*Response, error) {
	if req == nil {
		return nil, errors.New("validator client: nil request")
	}

	deadline := time.Now().Add(c.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	dialCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	conn, err := c.dial(dialCtx, c.SocketPath)
	if err != nil {
		return ProtocolError(fmt.Sprintf(
			"validator %q: connect %s: %v", c.Name, c.SocketPath, err,
		)), nil
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetDeadline(deadline); err != nil {
		return ProtocolError(fmt.Sprintf(
			"validator %q: set deadline: %v", c.Name, err,
		)), nil
	}

	if _, err := EncodeRequest(conn, req); err != nil {
		return ProtocolError(fmt.Sprintf(
			"validator %q: encode request: %v", c.Name, err,
		)), nil
	}

	resp, err := DecodeResponse(conn)
	if err != nil {
		return ProtocolError(fmt.Sprintf(
			"validator %q: decode response: %v", c.Name, err,
		)), nil
	}
	return resp, nil
}

// dial returns a unix-socket connection. Test code may override Client.dialer
// to redirect through an in-process listener.
func (c *Client) dial(ctx context.Context, path string) (net.Conn, error) {
	if c.dialer != nil {
		return c.dialer(ctx, path)
	}
	d := net.Dialer{}
	return d.DialContext(ctx, "unix", path)
}

// HealthCheck verifies a validator socket is reachable and accepting
// connections. Returns nil on success or a wrapped error describing the
// failure.
//
// The check is intentionally lightweight (sub-ms in the happy path) so it
// can run on every Kubernetes liveness/readiness probe interval (default
// 10s). It does NOT exercise the protocol — a malformed validator that
// accepts connections but produces garbage will pass HealthCheck. Catching
// that needs a deeper round-trip probe, which is out of scope for /healthz.
//
// On Linux, `os.OpenFile` against a stream unix socket fails with ENXIO
// regardless of the socket's state, so we use a short non-blocking dial as
// the readiness check instead. Stat + mode check still rules out the
// regular-file case before paying the dial cost.
func HealthCheck(socketPath string) error {
	info, err := os.Stat(socketPath)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("path is not a unix socket (mode=%s)", info.Mode())
	}
	d := net.Dialer{Timeout: 100 * time.Millisecond}
	conn, err := d.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	_ = conn.Close()
	return nil
}
