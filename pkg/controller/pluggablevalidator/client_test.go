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

package pluggablevalidator_test

import (
	"context"
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pv "gitlab.com/haproxy-haptic/haptic/pkg/controller/pluggablevalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pluggablevalidator/testutil"
)

func newRequest() *pv.Request {
	return &pv.Request{
		ProtocolVersion: pv.ProtocolVersion,
		Files: []pv.File{
			{Path: "hub-config.toml", Content: "[hub]\nlisten = \"0.0.0.0:9000\"\n"},
		},
	}
}

func TestClient_Validate_HappyPath(t *testing.T) {
	srv := testutil.NewFixtureServer(t)
	if err := srv.SetResponse(&pv.Response{
		ProtocolVersion: pv.ProtocolVersion,
		Result:          pv.ResultValid,
		Warnings:        []pv.Diagnostic{},
		Errors:          []pv.Diagnostic{},
	}); err != nil {
		t.Fatalf("SetResponse: %v", err)
	}

	client := pv.NewClient("coraza", srv.SocketPath, 2*time.Second, 2)
	resp, err := client.Validate(context.Background(), newRequest())
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if resp.Result != pv.ResultValid {
		t.Fatalf("result=%q want %q", resp.Result, pv.ResultValid)
	}
	if len(srv.Requests()) != 1 {
		t.Fatalf("server saw %d requests, want 1", len(srv.Requests()))
	}
}

func TestClient_Validate_ConnectionRefused(t *testing.T) {
	// Point at a path that doesn't exist; dial fails.
	missing := filepath.Join(t.TempDir(), "missing.sock")
	client := pv.NewClient("coraza", missing, 500*time.Millisecond, 2)

	resp, err := client.Validate(context.Background(), newRequest())
	if err != nil {
		t.Fatalf("Validate returned err for transport failure: %v (must surface as protocol-level error)", err)
	}
	if resp.Result != pv.ResultError {
		t.Fatalf("result=%q want %q", resp.Result, pv.ResultError)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Path != "" {
		t.Fatalf("expected single protocol-level diagnostic, got %+v", resp.Errors)
	}
	if !strings.Contains(resp.Errors[0].Message, "coraza") {
		t.Fatalf("error message must include validator name; got %q", resp.Errors[0].Message)
	}
}

func TestClient_Validate_ServerCloseWithoutResponse(t *testing.T) {
	srv := testutil.NewFixtureServer(t)
	// Don't SetResponse — server reads the request and hangs up.
	if err := srv.SetResponse(nil); err != nil {
		t.Fatalf("SetResponse(nil): %v", err)
	}

	client := pv.NewClient("coraza", srv.SocketPath, 500*time.Millisecond, 2)
	resp, err := client.Validate(context.Background(), newRequest())
	if err != nil {
		t.Fatalf("Validate returned err for closed connection: %v", err)
	}
	if resp.Result != pv.ResultError {
		t.Fatalf("expected error result for hangup, got %q", resp.Result)
	}
}

func TestClient_Validate_TimeoutEnforced(t *testing.T) {
	srv := testutil.NewFixtureServer(t)
	if err := srv.SetResponse(&pv.Response{
		ProtocolVersion: pv.ProtocolVersion,
		Result:          pv.ResultValid,
		Warnings:        []pv.Diagnostic{},
		Errors:          []pv.Diagnostic{},
	}); err != nil {
		t.Fatalf("SetResponse: %v", err)
	}
	srv.SetResponseDelay(500 * time.Millisecond)

	client := pv.NewClient("coraza", srv.SocketPath, 50*time.Millisecond, 2)
	start := time.Now()
	resp, err := client.Validate(context.Background(), newRequest())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Validate returned err: %v", err)
	}
	if resp.Result != pv.ResultError {
		t.Fatalf("expected error result on timeout, got %q", resp.Result)
	}
	// Allow significant slack for CI; the point is timeout fires before
	// the server's 500ms delay completes.
	if elapsed > 400*time.Millisecond {
		t.Fatalf("client slept %v before bailing — timeout not enforced", elapsed)
	}
}

func TestClient_Validate_ContextDeadlineEnforced(t *testing.T) {
	srv := testutil.NewFixtureServer(t)
	if err := srv.SetResponse(&pv.Response{
		ProtocolVersion: pv.ProtocolVersion,
		Result:          pv.ResultValid,
	}); err != nil {
		t.Fatalf("SetResponse: %v", err)
	}
	srv.SetResponseDelay(500 * time.Millisecond)

	client := pv.NewClient("coraza", srv.SocketPath, 5*time.Second, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	resp, _ := client.Validate(ctx, newRequest())
	elapsed := time.Since(start)

	if resp.Result != pv.ResultError {
		t.Fatalf("expected error result on context deadline, got %q", resp.Result)
	}
	if elapsed > 400*time.Millisecond {
		t.Fatalf("client did not honour context deadline; elapsed=%v", elapsed)
	}
}

func TestClient_Validate_MalformedResponse(t *testing.T) {
	srv := testutil.NewFixtureServer(t)
	// Length prefix but a body that isn't valid JSON.
	body := []byte("not json at all")
	length, err := pv.NarrowToUint32(len(body))
	if err != nil {
		t.Fatalf("encode body length: %v", err)
	}
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[:4], length)
	copy(frame[4:], body)
	srv.SetRawResponse(frame)

	client := pv.NewClient("coraza", srv.SocketPath, 500*time.Millisecond, 2)
	resp, err := client.Validate(context.Background(), newRequest())
	if err != nil {
		t.Fatalf("Validate returned err: %v", err)
	}
	if resp.Result != pv.ResultError {
		t.Fatalf("expected error for malformed response, got %q", resp.Result)
	}
}

func TestClient_Validate_NilRequest(t *testing.T) {
	client := pv.NewClient("coraza", "/nonexistent", 500*time.Millisecond, 2)
	if _, err := client.Validate(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil request — caller misused the API")
	}
}

func TestHealthCheck_HappyPath(t *testing.T) {
	srv := testutil.NewFixtureServer(t)
	if err := pv.HealthCheck(srv.SocketPath); err != nil {
		t.Fatalf("HealthCheck on live socket: %v", err)
	}
}

func TestHealthCheck_MissingSocket(t *testing.T) {
	if err := pv.HealthCheck(filepath.Join(t.TempDir(), "missing.sock")); err == nil {
		t.Fatal("expected error for missing socket path")
	}
}

func TestHealthCheck_NotASocket(t *testing.T) {
	regular := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(regular, []byte("hi"), 0o600); err != nil {
		t.Fatalf("write regular file: %v", err)
	}
	err := pv.HealthCheck(regular)
	if err == nil {
		t.Fatal("expected error: regular file is not a unix socket")
	}
	if !strings.Contains(err.Error(), "not a unix socket") {
		t.Fatalf("error message should identify the failure class; got %q", err.Error())
	}
}

func TestHealthCheck_FastEnoughForHealthz(t *testing.T) {
	srv := testutil.NewFixtureServer(t)
	// Run 10 checks back-to-back; each should be sub-millisecond on a
	// reasonable system. Allow generous CI slack — we're only guarding
	// against accidental seconds-level latency from a future regression.
	start := time.Now()
	for range 10 {
		if err := pv.HealthCheck(srv.SocketPath); err != nil {
			t.Fatalf("HealthCheck: %v", err)
		}
	}
	if avg := time.Since(start) / 10; avg > 50*time.Millisecond {
		t.Fatalf("avg HealthCheck latency %v exceeds 50ms — too slow for /healthz", avg)
	}
}

// Sanity: net.Listen on unix actually works in the test env. Sentinel test
// for environments where /tmp is on a fs that doesn't support unix sockets.
func TestNetListenUnixWorks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sentinel.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("net.Listen unix: %v", err)
	}
	_ = l.Close()
}
