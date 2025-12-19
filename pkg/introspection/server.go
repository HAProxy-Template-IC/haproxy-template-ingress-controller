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
	"fmt"
	"log/slog"
	"net/http"
	"time"

	//nolint:gosec // G108: pprof intentionally exposed for debugging
	_ "net/http/pprof" // Register pprof handlers
)

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
// The server supports two initialization patterns:
//
// 1. Simple (combined): Use Start() which calls Setup() and Serve() internally:
//
//	go server.Start(ctx)
//
// 2. Two-phase (for early health checks): Call Setup() first, then Serve() later:
//
//	server.RegisterHandler("/debug/events", eventsHandler)
//	server.SetHealthChecker(checker)
//	server.Setup()
//	go server.Serve(ctx)
//
// The two-phase pattern allows registering handlers and health checkers before
// routes are finalized, while enabling the server to start serving immediately.
type Server struct {
	addr           string
	registry       *Registry
	server         *http.Server
	logger         *slog.Logger
	mux            *http.ServeMux
	customHandlers []customHandler
	healthChecker  HealthCheckFunc
	setupDone      bool // Tracks whether Setup() has been called
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
//	go server.Start(ctx)
func NewServer(addr string, registry *Registry) *Server {
	logger := slog.Default().With("component", "introspection-server")
	mux := http.NewServeMux()

	s := &Server{
		addr:           addr,
		registry:       registry,
		logger:         logger,
		mux:            mux,
		customHandlers: []customHandler{},
	}

	// Setup will be called in Start() to include custom handlers
	return s
}

// RegisterHandler registers a custom HTTP handler for the given pattern.
// This must be called before Setup() (or Start(), which calls Setup() internally).
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

// SetHealthChecker sets a function to check component health.
// This must be called before Start().
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
	s.healthChecker = checker
}

// setupRoutes registers all HTTP handlers.
func (s *Server) setupRoutes(mux *http.ServeMux) {
	// Register custom handlers first (allow overriding defaults)
	for _, h := range s.customHandlers {
		mux.HandleFunc(h.pattern, h.handler)
	}

	// Variable endpoints (GET only)
	mux.HandleFunc("/debug/vars", requireGET(s.handleIndex))
	mux.HandleFunc("/debug/vars/", requireGET(s.handleVar)) // Trailing slash for path matching
	mux.HandleFunc("/debug/vars/all", requireGET(s.handleAllVars))

	// Health check endpoints (GET only)
	mux.HandleFunc("/health", requireGET(s.handleHealth))
	mux.HandleFunc("/healthz", requireGET(s.handleHealth))

	// Forward pprof requests to DefaultServeMux where net/http/pprof registers its handlers.
	// The pprof import side-effect registers on http.DefaultServeMux, so we forward to it.
	mux.Handle("/debug/pprof/", http.DefaultServeMux)

	// Catch-all for 404
	mux.HandleFunc("/", s.handleNotFound)
}

// Setup initializes routes and prepares the server for serving.
// This must be called before Serve(). Custom handlers and health checker
// can be registered after NewServer() but before Setup().
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

// Start is a convenience method that calls Setup() then Serve().
// For more control over timing (e.g., registering handlers after server creation
// but before routes are finalized), call Setup() and Serve() separately.
//
// This method should typically be run in a goroutine:
//
//	go server.Start(ctx)
//
// Example:
//
//	ctx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//
//	go server.Start(ctx)
//
//	// Later: cancel context to shutdown
//	cancel()
func (s *Server) Start(ctx context.Context) error {
	if !s.setupDone {
		s.Setup()
	}
	return s.Serve(ctx)
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
		return fmt.Errorf("Setup() must be called before Serve()")
	}

	s.server = &http.Server{
		Addr:              s.addr,
		Handler:           s.mux,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Channel to signal server has stopped
	serverErr := make(chan error, 1)

	// Start server in background
	go func() {
		s.logger.Info("Starting debug server", "addr", s.addr)

		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("Debug server error", "error", err)
			serverErr <- err
		}
	}()

	// Wait for context cancellation or server error
	select {
	case <-ctx.Done():
		s.logger.Info("Debug server shutting down", "reason", ctx.Err())

		// Graceful shutdown with timeout
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := s.server.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("Debug server shutdown error", "error", err)
			return fmt.Errorf("server shutdown failed: %w", err)
		}

		s.logger.Info("Debug server stopped")
		return nil

	case err := <-serverErr:
		return fmt.Errorf("server error: %w", err)
	}
}

// Addr returns the address the server is configured to listen on.
func (s *Server) Addr() string {
	return s.addr
}
