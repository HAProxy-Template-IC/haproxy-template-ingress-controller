// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package introspection

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The dump is what pprof cannot give: the object graph. A response that is
// merely non-empty proves nothing, so assert the format header a heap-dump
// reader keys on.
func TestServer_HandleHeapDump(t *testing.T) {
	server := NewServer("localhost:0", NewRegistry())
	cancel := startServer(t, server)
	defer cancel()

	resp, err := http.Get("http://" + server.addrForTest() + "/debug/heapdump")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/octet-stream", resp.Header.Get("Content-Type"))
	assert.Contains(t, resp.Header.Get("Content-Disposition"), "heap.dump")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Greater(t, len(body), 1024, "a heap dump of a running process is never trivially small")
	assert.True(t, strings.HasPrefix(string(body), "go1."),
		"dump must carry the runtime heapdump header, got %q", string(body[:min(16, len(body))]))
}

// The handler writes to a file because WriteHeapDump forbids a pipe whose reader
// is in the same process. It must not leave the file behind — a heap-sized file
// per call would fill the container's writable layer.
func TestServer_HandleHeapDump_RemovesTempFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	server := NewServer("localhost:0", NewRegistry())
	cancel := startServer(t, server)
	defer cancel()

	resp, err := http.Get("http://" + server.addrForTest() + "/debug/heapdump")
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	leftovers, err := filepath.Glob(filepath.Join(tmp, "haptic-heapdump-*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers, "temp dump file must not survive the response")
	_ = os.Remove(tmp)
}

// HAPTIC_HEAPDUMP_DIR redirects the temp file off the container's writable
// layer, whose ephemeral-storage allowance is usually far below heap size.
func TestServer_HandleHeapDump_HonoursDirEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(HeapDumpDirEnv, dir)

	server := NewServer("localhost:0", NewRegistry())
	cancel := startServer(t, server)
	defer cancel()

	resp, err := http.Get("http://" + server.addrForTest() + "/debug/heapdump")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, strings.HasPrefix(string(body), "go1."), "dump must come back even from a custom directory")

	leftovers, err := filepath.Glob(filepath.Join(dir, "haptic-heapdump-*"))
	require.NoError(t, err)
	assert.Empty(t, leftovers, "custom directory must be cleaned up too")
}

// Each dump stops the world, so a second concurrent request must be refused
// rather than doubling the pause and the disk footprint.
func TestServer_HandleHeapDump_RejectsConcurrent(t *testing.T) {
	server := NewServer("localhost:0", NewRegistry())
	cancel := startServer(t, server)
	defer cancel()

	require.True(t, server.heapDumpInFlight.CompareAndSwap(false, true), "guard should start clear")
	defer server.heapDumpInFlight.Store(false)

	resp, err := http.Get("http://" + server.addrForTest() + "/debug/heapdump")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusConflict, resp.StatusCode,
		"a dump already in progress must be refused, not queued behind another stop-the-world pause")
}

// The endpoint must collect before dumping. Without that the dump is dominated
// by unreachable objects, and an unreachable object has no retainer to report —
// so a reader's "what keeps this alive" query correctly returns nothing for
// almost everything, which reads as a broken tool rather than a useless dump.
// This asserts the collection actually happens.
func TestServer_HandleHeapDump_CollectsBeforeDumping(t *testing.T) {
	server := NewServer("localhost:0", NewRegistry())
	cancel := startServer(t, server)
	defer cancel()

	// Allocate in a callee so the garbage becomes unreachable when it returns;
	// nil-ing a local here would be an ineffectual assignment.
	allocateGarbage()

	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	resp, err := http.Get("http://" + server.addrForTest() + "/debug/heapdump")
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	assert.Greater(t, after.NumGC, before.NumGC,
		"the endpoint must run a collection before writing, or the dump is mostly garbage")
}

// allocateGarbage leaves ~64 MiB unreachable behind it.
func allocateGarbage() {
	garbage := make([][]byte, 0, 64)
	for range 64 {
		garbage = append(garbage, make([]byte, 1<<20))
	}
	runtime.KeepAlive(garbage)
}

// A dump served with a 200 is trusted. The runtime discards its write errors, so
// nothing but this check stands between a full filesystem and an operator
// reasoning from a truncated object graph.
//
// The free-space reading is an argument rather than a call inside the function,
// so the low-space verdict can be exercised without a near-full filesystem.
func TestVerifyHeapDumpComplete(t *testing.T) {
	whole := append([]byte("go1.7 heap dump\n"), 0x01, 0x02, heapDumpEOFTag)
	const roomy = 100 << 20

	tests := []struct {
		name            string
		content         []byte
		availAfter      uint64
		availAfterKnown bool
		wantErr         string
	}{
		{
			name:            "complete dump on a filesystem with room",
			content:         whole,
			availAfter:      roomy,
			availAfterKnown: true,
		},
		{
			name:            "truncated dump",
			content:         append([]byte("go1.7 heap dump\n"), 0x01, 0x02, 0x03),
			availAfter:      roomy,
			availAfterKnown: true,
			wantErr:         "truncated",
		},
		{
			name:            "empty dump",
			content:         nil,
			availAfter:      roomy,
			availAfterKnown: true,
			wantErr:         "empty",
		},
		{
			// The tag says whole, the filesystem says it filled. A truncation
			// can land on the tag's byte value by chance, so this is refused.
			name:            "dump looks whole but the filesystem is full",
			content:         whole,
			availAfter:      0,
			availAfterKnown: true,
			wantErr:         "too little to trust",
		},
		{
			// An unqueryable filesystem must not fail a good dump; the space
			// reading is a second opinion, not a gate.
			name:            "space unknown falls back to the tag alone",
			content:         whole,
			availAfterKnown: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "dump")
			require.NoError(t, os.WriteFile(path, tt.content, 0o600))
			f, err := os.Open(path)
			require.NoError(t, err)
			defer f.Close()

			err = verifyHeapDumpComplete(f, tt.availAfter, tt.availAfterKnown)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}
