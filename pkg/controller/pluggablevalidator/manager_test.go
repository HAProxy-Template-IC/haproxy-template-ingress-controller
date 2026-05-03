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
	"path/filepath"
	"strings"
	"testing"
	"time"

	pv "gitlab.com/haproxy-haptic/haptic/pkg/controller/pluggablevalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pluggablevalidator/testutil"
)

func managerRequest() *pv.Request {
	return &pv.Request{
		ProtocolVersion: pv.ProtocolVersion,
		Files: []pv.File{
			{Path: "hub-config.toml", Content: "[hub]\nlisten = \"0.0.0.0:9000\"\n"},
		},
	}
}

func TestManager_NewManager_RejectsDuplicateNames(t *testing.T) {
	configs := []pv.ManagerConfig{
		{Name: "coraza", SocketPath: "/x"},
		{Name: "coraza", SocketPath: "/y"},
	}
	if _, err := pv.NewManager(nil, configs); err == nil {
		t.Fatal("expected error for duplicate validator names")
	}
}

func TestManager_NewManager_RejectsEmptyName(t *testing.T) {
	if _, err := pv.NewManager(nil, []pv.ManagerConfig{{SocketPath: "/x"}}); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestManager_NewManager_RejectsEmptySocketPath(t *testing.T) {
	if _, err := pv.NewManager(nil, []pv.ManagerConfig{{Name: "coraza"}}); err == nil {
		t.Fatal("expected error for empty socketPath")
	}
}

func TestManager_NoValidators(t *testing.T) {
	mgr, err := pv.NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if mgr.Configured() {
		t.Fatal("Configured() must be false when no validators are registered")
	}
	ok, failures := mgr.Healthy()
	if !ok || failures != nil {
		t.Fatalf("empty Manager Healthy() = (%v, %v), want (true, nil)", ok, failures)
	}
}

func TestManager_Validate_UnknownValidator(t *testing.T) {
	mgr, err := pv.NewManager(nil, []pv.ManagerConfig{{Name: "coraza", SocketPath: "/no"}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if _, err := mgr.Validate(context.Background(), "otel", managerRequest()); err == nil {
		t.Fatal("expected error for unknown validator name")
	}
}

func TestManager_Validate_NilRequest(t *testing.T) {
	mgr, _ := pv.NewManager(nil, []pv.ManagerConfig{{Name: "coraza", SocketPath: "/x"}})
	if _, err := mgr.Validate(context.Background(), "coraza", nil); err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestManager_Validate_HappyPath(t *testing.T) {
	srv := testutil.NewFixtureServer(t)
	if err := srv.SetResponse(&pv.Response{
		ProtocolVersion: pv.ProtocolVersion,
		Result:          pv.ResultValid,
		Warnings:        []pv.Diagnostic{},
		Errors:          []pv.Diagnostic{},
	}); err != nil {
		t.Fatalf("SetResponse: %v", err)
	}

	mgr, err := pv.NewManager(nil, []pv.ManagerConfig{
		{Name: "coraza", SocketPath: srv.SocketPath, Timeout: time.Second},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	resp, err := mgr.Validate(context.Background(), "coraza", managerRequest())
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

func TestManager_Validate_CacheHitSkipsRoundTrip(t *testing.T) {
	srv := testutil.NewFixtureServer(t)
	if err := srv.SetResponse(&pv.Response{
		ProtocolVersion: pv.ProtocolVersion,
		Result:          pv.ResultValid,
	}); err != nil {
		t.Fatalf("SetResponse: %v", err)
	}

	mgr, _ := pv.NewManager(nil, []pv.ManagerConfig{
		{Name: "coraza", SocketPath: srv.SocketPath, Timeout: time.Second},
	})

	// First call goes over the wire.
	if _, err := mgr.Validate(context.Background(), "coraza", managerRequest()); err != nil {
		t.Fatalf("first Validate: %v", err)
	}
	// Second call with identical request must be served from cache.
	if _, err := mgr.Validate(context.Background(), "coraza", managerRequest()); err != nil {
		t.Fatalf("second Validate: %v", err)
	}
	if got := len(srv.Requests()); got != 1 {
		t.Fatalf("server saw %d requests, want 1 (cache hit must skip socket)", got)
	}
}

func TestManager_Validate_TransportErrorNotCached(t *testing.T) {
	srv := testutil.NewFixtureServer(t)
	if err := srv.SetResponse(&pv.Response{
		ProtocolVersion: pv.ProtocolVersion,
		Result:          pv.ResultValid,
	}); err != nil {
		t.Fatalf("SetResponse: %v", err)
	}

	missing := filepath.Join(t.TempDir(), "missing.sock")
	mgr, _ := pv.NewManager(nil, []pv.ManagerConfig{
		{Name: "broken", SocketPath: missing, Timeout: 200 * time.Millisecond},
		{Name: "ok", SocketPath: srv.SocketPath, Timeout: time.Second},
	})

	// Two calls against the broken validator: both must hit the
	// network (no caching of transport failures), so neither should
	// be served from cache. The cache is otherwise functional — a
	// successful round-trip against the ok validator IS cached.
	for range 2 {
		resp, err := mgr.Validate(context.Background(), "broken", managerRequest())
		if err != nil {
			t.Fatalf("Validate broken: %v", err)
		}
		if resp.Result != pv.ResultError {
			t.Fatalf("broken validator result=%q want %q", resp.Result, pv.ResultError)
		}
	}

	// Sanity: the ok validator's cache works (single round-trip).
	if _, err := mgr.Validate(context.Background(), "ok", managerRequest()); err != nil {
		t.Fatalf("Validate ok (1): %v", err)
	}
	if _, err := mgr.Validate(context.Background(), "ok", managerRequest()); err != nil {
		t.Fatalf("Validate ok (2): %v", err)
	}
	if got := len(srv.Requests()); got != 1 {
		t.Fatalf("ok validator hit network %d times, want 1 (second call should be cached)", got)
	}
}

// Regression: real validator responses that legitimately carry
// `path: ""` diagnostics (plugin panics caught by the sidecar,
// file-level errors like "directives field is required") must be
// cached. The previous heuristic mistook them for transport failures
// and skipped caching, defeating the LRU for these entries.
func TestManager_Validate_RealValidatorPathlessErrorIsCached(t *testing.T) {
	srv := testutil.NewFixtureServer(t)
	if err := srv.SetResponse(&pv.Response{
		ProtocolVersion: pv.ProtocolVersion,
		Result:          pv.ResultError,
		Warnings:        []pv.Diagnostic{},
		Errors: []pv.Diagnostic{
			// Path: "" is the spec-allowed shape for plugin panics
			// and structural errors. These are real validator output,
			// not transport failures, and must be cached.
			{Path: "", Line: 0, Column: 0, Message: "internal validator error in plugin coraza: panic: foo"},
		},
	}); err != nil {
		t.Fatalf("SetResponse: %v", err)
	}

	mgr, _ := pv.NewManager(nil, []pv.ManagerConfig{
		{Name: "coraza", SocketPath: srv.SocketPath, Timeout: time.Second},
	})

	for range 2 {
		resp, err := mgr.Validate(context.Background(), "coraza", managerRequest())
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}
		if resp.Result != pv.ResultError {
			t.Fatalf("result=%q want %q", resp.Result, pv.ResultError)
		}
	}
	if got := len(srv.Requests()); got != 1 {
		t.Fatalf("server saw %d requests, want 1 (real validator response with path:\"\" must be cached)", got)
	}
}

// Regression: synthetic ProtocolError responses (built by the client
// when the socket is unreachable, the JSON is malformed, etc.) MUST
// NOT be cached. The marker is the unexported `synthetic` field, set
// by ProtocolError() and only there.
func TestManager_Validate_SyntheticProtocolErrorNotCached(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.sock")
	mgr, _ := pv.NewManager(nil, []pv.ManagerConfig{
		{Name: "broken", SocketPath: missing, Timeout: 200 * time.Millisecond},
	})

	resp1, _ := mgr.Validate(context.Background(), "broken", managerRequest())
	resp2, _ := mgr.Validate(context.Background(), "broken", managerRequest())

	if !resp1.IsSynthetic() || !resp2.IsSynthetic() {
		t.Fatal("expected both responses to be synthetic ProtocolErrors")
	}
	if resp1 == resp2 {
		t.Fatal("synthetic responses must not be cached (got identical pointer back)")
	}
}

func TestManager_PluginsFor(t *testing.T) {
	mgr, _ := pv.NewManager(nil, []pv.ManagerConfig{
		{Name: "coraza", SocketPath: "/x", Plugins: []string{"coraza", "external_auth"}},
		{Name: "otel", SocketPath: "/y"},
	})
	plugins := mgr.PluginsFor("coraza")
	if len(plugins) != 2 || plugins[0] != "coraza" {
		t.Fatalf("PluginsFor(coraza) = %v, want [coraza external_auth]", plugins)
	}
	if got := mgr.PluginsFor("otel"); len(got) != 0 {
		t.Fatalf("PluginsFor(otel) = %v, want empty", got)
	}
	if got := mgr.PluginsFor("ghost"); got != nil {
		t.Fatalf("PluginsFor(unknown) = %v, want nil", got)
	}
}

func TestManager_Names_PreservesOrder(t *testing.T) {
	mgr, _ := pv.NewManager(nil, []pv.ManagerConfig{
		{Name: "first", SocketPath: "/a"},
		{Name: "second", SocketPath: "/b"},
		{Name: "third", SocketPath: "/c"},
	})
	got := mgr.Names()
	want := []string{"first", "second", "third"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
}

func TestManager_Healthy_AllUp(t *testing.T) {
	srv1 := testutil.NewFixtureServer(t)
	srv2 := testutil.NewFixtureServer(t)
	mgr, _ := pv.NewManager(nil, []pv.ManagerConfig{
		{Name: "coraza", SocketPath: srv1.SocketPath},
		{Name: "otel", SocketPath: srv2.SocketPath},
	})
	ok, failures := mgr.Healthy()
	if !ok || failures != nil {
		t.Fatalf("Healthy() = (%v, %v), want (true, nil)", ok, failures)
	}
}

func TestManager_Healthy_OneDown(t *testing.T) {
	srv := testutil.NewFixtureServer(t)
	missing := filepath.Join(t.TempDir(), "missing.sock")
	mgr, _ := pv.NewManager(nil, []pv.ManagerConfig{
		{Name: "coraza", SocketPath: srv.SocketPath},
		{Name: "otel", SocketPath: missing},
	})
	ok, failures := mgr.Healthy()
	if ok {
		t.Fatal("Healthy() returned true with one socket missing")
	}
	if len(failures) != 1 {
		t.Fatalf("failures=%v, want exactly 1 entry", failures)
	}
	if !strings.HasPrefix(failures[0], "otel:") {
		t.Fatalf("failure should identify the validator name; got %q", failures[0])
	}
}
