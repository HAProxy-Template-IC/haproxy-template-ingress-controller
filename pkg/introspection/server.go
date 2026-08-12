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

package introspection

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"os"
	"path/filepath"
	rtdebug "runtime/debug"

	_ "net/http/pprof" // Register pprof handlers
)

// HeapDumpDirEnv names the directory /debug/heapdump writes its temporary file
// to. Defaults to $TMPDIR, which in a container is normally the writable layer
// and counts against ephemeral-storage limits; point it at a mounted volume when
// the heap is larger than that allowance.
const HeapDumpDirEnv = "HAPTIC_HEAPDUMP_DIR"

// HealthCheckFunc is a function that returns component health status.
// Used to integrate with lifecycle registry or other health monitoring.
type HealthCheckFunc func() map[string]ComponentHealth

// ComponentHealth represents the health status of a single component.
type ComponentHealth struct {
	Healthy bool   `json:"healthy"`
	Error   string `json:"error,omitempty"`
}

// Server serves debug variables over HTTP.
//
// The server provides HTTP endpoints for accessing variables registered in a Registry.
// It supports JSONPath field selection for querying specific fields from variables.
//
// Standard endpoints:
//   - GET /debug/vars - list all variable paths
//   - GET /debug/vars/all - get all variables
//   - GET /debug/vars/{path} - get specific variable
//   - GET /debug/vars/{path}?field={.jsonpath} - get field from variable
//   - GET /health - health check
//   - GET /debug/pprof/* - Go profiling endpoints (via import side-effect)
//
// Custom handlers can be registered using RegisterHandler() before Setup() is called.
//
// The server uses two-phase initialization: call Setup() first, then Serve():
//
//	server.RegisterHandler("/debug/events", eventsHandler)
//	server.SetHealthChecker(checker)
//	server.Setup()
//	go server.Serve(ctx)
//
// The two-phase pattern allows registering custom handlers before routes are
// finalized. The health checker may be replaced while the server is running.
type Server struct {
	addr            string
	addrMu          sync.RWMutex
	registry        *Registry
	server          *http.Server
	logger          *slog.Logger
	mux             *http.ServeMux
	customHandlers  []customHandler
	healthCheckerMu sync.RWMutex
	healthChecker   HealthCheckFunc
	setupDone       bool // Tracks whether Setup() has been called
	listening       chan struct{}
	// heapDumpInFlight serialises /debug/heapdump; each dump stops the world.
	heapDumpInFlight atomic.Bool
}

// customHandler holds a pattern and handler for custom endpoint registration.
type customHandler struct {
	pattern string
	handler http.HandlerFunc
}

// NewServer creates a new HTTP server for serving debug variables.
//
// Parameters:
//   - addr: TCP address to listen on (e.g., ":6060" or "localhost:6060")
//   - registry: The variable registry to serve
//
// Example:
//
//	registry := introspection.NewRegistry()
//	registry.Publish("config", &ConfigVar{provider})
//
//	server := introspection.NewServer(":6060", registry)
//	server.Setup()
//	go server.Serve(ctx)
func NewServer(addr string, registry *Registry) *Server {
	logger := slog.Default().With("component", "introspection-server")
	mux := http.NewServeMux()

	s := &Server{
		addr:           addr,
		registry:       registry,
		logger:         logger,
		mux:            mux,
		customHandlers: []customHandler{},
		listening:      make(chan struct{}),
	}

	// Setup must be called separately so custom handlers can be registered first
	return s
}

// RegisterHandler registers a custom HTTP handler for the given pattern.
// This must be called before Setup().
//
// Parameters:
//   - pattern: URL pattern (e.g., "/debug/events")
//   - handler: HTTP handler function
//
// Example:
//
//	server.RegisterHandler("/debug/events", func(w http.ResponseWriter, r *http.Request) {
//	    correlationID := r.URL.Query().Get("correlation_id")
//	    events := commentator.FindByCorrelationID(correlationID, 100)
//	    introspection.WriteJSON(w, events)
//	})
func (s *Server) RegisterHandler(pattern string, handler http.HandlerFunc) {
	s.customHandlers = append(s.customHandlers, customHandler{
		pattern: pattern,
		handler: handler,
	})
}

// SetHealthChecker sets or replaces the function used to check component health.
//
// The health checker function is called by the /health endpoint to get
// the health status of all components. If not set, /health just returns "ok".
//
// Example integration with lifecycle registry:
//
//	server.SetHealthChecker(func() map[string]introspection.ComponentHealth {
//	    status := registry.Status()
//	    result := make(map[string]introspection.ComponentHealth)
//	    for name, info := range status {
//	        healthy := info.Status == lifecycle.StatusRunning
//	        if info.Healthy != nil {
//	            healthy = *info.Healthy
//	        }
//	        result[name] = introspection.ComponentHealth{
//	            Healthy: healthy,
//	            Error:   info.Error,
//	        }
//	    }
//	    return result
//	})
func (s *Server) SetHealthChecker(checker HealthCheckFunc) {
	s.healthCheckerMu.Lock()
	defer s.healthCheckerMu.Unlock()
	s.healthChecker = checker
}

// Listening closes after the TCP listener has bound successfully.
func (s *Server) Listening() <-chan struct{} {
	return s.listening
}

// setupRoutes registers all HTTP handlers.
func (s *Server) setupRoutes(mux *http.ServeMux) {
	// Register custom handlers first (allow overriding defaults)
	for _, h := range s.customHandlers {
		if strings.HasPrefix(h.pattern, "/debug/") {
			mux.HandleFunc(h.pattern, requireLoopback(h.handler))
			continue
		}
		mux.HandleFunc(h.pattern, h.handler)
	}

	// Variable endpoints (GET only)
	mux.HandleFunc("/debug/vars", requireLoopback(requireGET(s.handleIndex)))
	mux.HandleFunc("/debug/vars/", requireLoopback(requireGET(s.handleVar))) // Trailing slash for path matching
	mux.HandleFunc("/debug/vars/all", requireLoopback(requireGET(s.handleAllVars)))

	// Health check endpoints (GET only)
	mux.HandleFunc("/health", requireGET(s.handleHealth))
	mux.HandleFunc("/healthz", requireGET(s.handleHealth))

	// Forward pprof requests to DefaultServeMux where net/http/pprof registers its handlers.
	// The pprof import side-effect registers on http.DefaultServeMux, so we forward to it.
	mux.Handle("/debug/pprof/", requireLoopback(http.DefaultServeMux.ServeHTTP))

	// Heap dump: the full object graph, which pprof does not carry. pprof answers
	// where memory was allocated; this answers what still holds it.
	mux.HandleFunc("/debug/heapdump", requireLoopback(requireGET(s.handleHeapDump)))

	// Catch-all for 404
	mux.HandleFunc("/", s.handleNotFound)
}

// Setup initializes routes and prepares the server for serving.
// This must be called before Serve(). Custom handlers must be registered first.
//
// After Setup() is called, no new handlers can be registered.
//
// Example:
//
//	server.RegisterHandler("/debug/events", eventsHandler)
//	server.SetHealthChecker(checker)
//	server.Setup()
//	go server.Serve(ctx)
func (s *Server) Setup() {
	s.setupRoutes(s.mux)
	s.setupDone = true
}

// Serve starts the HTTP server and blocks until the context is cancelled.
// Setup() must be called before Serve().
//
// This method should typically be run in a goroutine:
//
//	server.Setup()
//	go server.Serve(ctx)
//
// The server performs graceful shutdown when the context is cancelled,
// waiting for active connections to complete (up to a timeout).
//
// Example (two-phase initialization for early health checks):
//
//	server.RegisterHandler("/debug/events", eventsHandler)
//	server.SetHealthChecker(checker)
//	server.Setup()
//	go server.Serve(ctx)
func (s *Server) Serve(ctx context.Context) error {
	if !s.setupDone {
		return errors.New("Setup() must be called before Serve()")
	}

	s.server = &http.Server{
		Handler:           s.mux,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	s.addrMu.RLock()
	listenAddr := s.addr
	s.addrMu.RUnlock()

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("server error: %w", err)
	}

	s.addrMu.Lock()
	s.addr = listener.Addr().String()
	s.addrMu.Unlock()
	close(s.listening)

	serveDone := make(chan error, 1)

	go func() {
		s.logger.Info("Starting debug server", "addr", s.addr)
		err := s.server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("Debug server error", "error", err)
		}
		serveDone <- err
	}()

	select {
	case <-ctx.Done():
		s.logger.Info("Debug server shutting down", "reason", ctx.Err())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		shutdownErr := s.server.Shutdown(shutdownCtx)
		cancel()
		if shutdownErr != nil {
			s.logger.Error("Debug server shutdown error", "error", shutdownErr)
			shutdownErr = errors.Join(shutdownErr, s.server.Close())
		}
		serveErr := <-serveDone
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		s.logger.Info("Debug server stopped")
		return errors.Join(shutdownErr, serveErr)
	case err := <-serveDone:
		if errors.Is(err, http.ErrServerClosed) {
			return errors.New("server stopped unexpectedly")
		}
		if err == nil {
			return errors.New("server exited without an error")
		}
		return fmt.Errorf("server error: %w", err)
	}
}

// handleHeapDump streams a runtime/debug heap dump: every heap object, the
// pointer edges between them, and the roots. pprof samples allocation sites, so
// it cannot say which reference keeps an object alive; this can. Analyse the
// result with a heap-dump reader such as heapspurs, whose `--owners` walks the
// retainer chain back to a root.
//
// The dump goes to a file because WriteHeapDump suspends every goroutine until
// the write completes. Its documentation therefore forbids a descriptor "connected
// to a pipe or socket whose other end is in the same Go process": the reader could
// never run, so the write would block once the pipe filled — a deadlock, not a
// slowdown. That rules out streaming the dump as it is produced. A file also keeps
// the pause bounded by disk speed rather than by the client's read speed; a stalled
// client would otherwise hold the world stopped, taking down health checks and the
// admission webhook, which with failurePolicy: Fail rejects every write to a
// watched resource cluster-wide.
//
// The dump is roughly heap-sized. $TMPDIR is normally the container's writable
// layer and counts against ephemeral-storage limits, so the space is checked
// first and HeapDumpDirEnv can redirect it to a mounted volume.
func (s *Server) handleHeapDump(w http.ResponseWriter, _ *http.Request) {
	// One at a time: a second concurrent dump doubles both the stop-the-world
	// pause and the disk footprint for no extra information.
	if !s.heapDumpInFlight.CompareAndSwap(false, true) {
		http.Error(w, "a heap dump is already in progress", http.StatusConflict)
		return
	}
	defer s.heapDumpInFlight.Store(false)

	dir, err := heapDumpDir()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Collect first. WriteHeapDump stops the world but does not collect, so a
	// dump taken straight away is mostly unreachable objects — and an object with
	// no path to a root has no retainer, the one question this endpoint answers.
	// Sizing after the collection also measures the heap that gets written.
	runtime.GC()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	need := ms.Sys * heapDumpSizeFactor
	if avail, ok := availableBytes(dir); ok && avail < need {
		http.Error(w, fmt.Sprintf(
			"refusing to write heap dump: %s has %d MiB free, which may not hold the dump (up to %d MiB). Set %s to a volume with room.",
			dir, avail/(1<<20), need/(1<<20), HeapDumpDirEnv),
			http.StatusInsufficientStorage)
		return
	}

	f, err := os.CreateTemp(dir, "haptic-heapdump-*")
	if err != nil {
		http.Error(w, fmt.Sprintf("creating heap dump file: %v", err), http.StatusInternalServerError)
		return
	}
	// Unlink immediately: the descriptor keeps the data reachable, so the space is
	// reclaimed even if this process is killed mid-transfer.
	if err := os.Remove(f.Name()); err != nil {
		s.logger.Warn("Could not unlink heap dump file", "path", f.Name(), "error", err)
	}
	defer func() { _ = f.Close() }()

	rtdebug.WriteHeapDump(f.Fd())

	// Read the space now, not before: a filesystem that filled mid-write is
	// still full at this point, which is what makes it a usable second signal.
	availAfter, availAfterKnown := availableBytes(dir)
	if err := verifyHeapDumpComplete(f, availAfter, availAfterKnown); err != nil {
		http.Error(w, fmt.Sprintf("%v. Set %s to a volume with room.", err, HeapDumpDirEnv),
			http.StatusInsufficientStorage)
		return
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		http.Error(w, fmt.Sprintf("rewinding heap dump: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="heap.dump"`)
	if _, err := io.Copy(w, f); err != nil {
		s.logger.Warn("Heap dump transfer interrupted", "error", err)
	}
}

// heapDumpSizeFactor turns the runtime's Sys into an upper bound for the dump.
//
// Sys alone is not one. The dump writes a record per object, and on an
// object-dense heap that overhead carries it past everything the runtime
// obtained from the OS: measured at 1.39x Sys for 3M small pointer-bearing
// objects, against 0.08x for an idle test binary where fixed overhead
// dominates. HeapSys is worse still — it covers no stacks or runtime metadata.
const heapDumpSizeFactor = 2

// heapDumpEOFTag ends a complete dump: runtime/heapdump.go closes mdump with
// dumpint(tagEOF), and tagEOF is 0.
const heapDumpEOFTag = 0

// heapDumpSpaceFloor is the free space below which a dump that *looks* whole is
// not believed. The preflight required twice the runtime's Sys, so finishing
// this close to full means the filesystem filled during the write.
const heapDumpSpaceFloor = 1 << 20

// verifyHeapDumpComplete reports whether the whole dump reached disk. The
// runtime discards the result of every write it makes (dwrite and flush in
// runtime/heapdump.go), so a filesystem that fills mid-dump yields a short file
// and no error — served as 200, an operator would analyse a corrupt dump.
//
// Two signals, because the trailing tag alone is not conclusive: a truncation
// lands at an arbitrary offset, and a heap graph carries enough zero bytes that
// one can end on the tag's value by chance. The filesystem cannot lie the same
// way — one that filled mid-dump is still full when the dump returns.
func verifyHeapDumpComplete(f *os.File, availAfter uint64, availAfterKnown bool) error {
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("cannot size the heap dump: %w", err)
	}
	if size == 0 {
		return errors.New("the heap dump is empty")
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], size-1); err != nil {
		return fmt.Errorf("cannot read the heap dump trailer: %w", err)
	}
	if last[0] != heapDumpEOFTag {
		return fmt.Errorf("the heap dump is truncated at %d MiB, so the staging directory ran out of space", size/(1<<20))
	}
	if availAfterKnown && availAfter < heapDumpSpaceFloor {
		return fmt.Errorf("the heap dump ended with %d MiB free, too little to trust that all %d MiB of it were written",
			availAfter/(1<<20), size/(1<<20))
	}
	return nil
}

// heapDumpDir resolves where the heap dump is staged. An operator-supplied
// directory is accepted only as an existing absolute path, so the value cannot
// steer the write outside a directory they already chose.
func heapDumpDir() (string, error) {
	custom := os.Getenv(HeapDumpDirEnv)
	if custom == "" {
		return os.TempDir(), nil
	}
	cleaned := filepath.Clean(custom)
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("%s must be an absolute path, got %q", HeapDumpDirEnv, custom)
	}
	info, err := os.Stat(cleaned)
	if err != nil {
		return "", fmt.Errorf("%s %q is not usable: %w", HeapDumpDirEnv, cleaned, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s %q is not a directory", HeapDumpDirEnv, cleaned)
	}
	return cleaned, nil
}
