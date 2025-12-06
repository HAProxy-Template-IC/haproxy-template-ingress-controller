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
// Custom handlers can be registered using RegisterHandler() before Start().
//
// The server is designed to run in a separate goroutine and gracefully shut down
// when the context is cancelled.
type Server struct {
	addr           string
	registry       *Registry
	server         *http.Server
	logger         *slog.Logger
	mux            *http.ServeMux
	customHandlers []customHandler
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
// This must be called before Start().
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

// setupRoutes registers all HTTP handlers.
func (s *Server) setupRoutes(mux *http.ServeMux) {
	// Register custom handlers first (allow overriding defaults)
	for _, h := range s.customHandlers {
		mux.HandleFunc(h.pattern, h.handler)
	}

	// Variable endpoints
	mux.HandleFunc("/debug/vars", s.handleIndex)
	mux.HandleFunc("/debug/vars/", s.handleVar) // Trailing slash for path matching
	mux.HandleFunc("/debug/vars/all", s.handleAllVars)

	// Health check endpoints
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/healthz", s.handleHealth)

	// pprof endpoints are registered via import side-effect
	// Available at: /debug/pprof/*

	// Catch-all for 404
	mux.HandleFunc("/", s.handleNotFound)
}

// Start starts the HTTP server and blocks until the context is cancelled.
//
// This method should typically be run in a goroutine:
//
//	go server.Start(ctx)
//
// The server performs graceful shutdown when the context is cancelled,
// waiting for active connections to complete (up to a timeout).
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
	// Setup routes (including custom handlers) and create HTTP server
	s.setupRoutes(s.mux)

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
