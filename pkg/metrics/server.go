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

package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Server serves Prometheus metrics over HTTP.
//
// IMPORTANT: Server is instance-based (not global). Create one per application lifecycle
// to ensure metrics are garbage collected when the server stops.
//
// The server provides a /metrics endpoint for Prometheus scraping and gracefully shuts down
// when the context is cancelled.
type Server struct {
	addr     string
	addrMu   sync.RWMutex
	registry prometheus.Gatherer
	server   *http.Server
	logger   *slog.Logger
}

// NewServer creates a new metrics server.
//
// IMPORTANT: Pass an instance-based registry (prometheus.NewRegistry()),
// NOT prometheus.DefaultRegisterer. This ensures metrics are garbage collected
// when the server stops, which is critical for applications that reinitialize
// on configuration changes.
//
// Parameters:
//   - addr: TCP address to listen on (e.g., ":9090" or "localhost:9090")
//   - registry: The Prometheus registry to serve (use prometheus.NewRegistry())
//
// Example:
//
//	registry := prometheus.NewRegistry()  // Instance-based, not global!
//	server := metrics.NewServer(":9090", registry)
//	go server.Start(ctx)
func NewServer(addr string, registry prometheus.Gatherer) *Server {
	logger := slog.Default().With("component", "metrics-server")

	s := &Server{
		addr:     addr,
		registry: registry,
		logger:   logger,
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	}))
	mux.HandleFunc("/", s.handleRoot)

	s.server = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return s
}

// Start starts the HTTP server and blocks until the context is cancelled.
//
// This method should typically be run in a goroutine:
//
//	go server.Start(ctx)
//
// The server performs graceful shutdown when the context is cancelled,
// waiting for active connections to complete (up to a 10-second timeout).
func (s *Server) Start(ctx context.Context) error {
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

	serverErr := make(chan error, 1)

	// Start server in background
	go func() {
		s.logger.Info("Starting metrics server", "addr", s.addr)

		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			s.logger.Error("Metrics server error", "error", err)
			serverErr <- err
		}
	}()

	// Wait for context cancellation or server error
	select {
	case <-ctx.Done():
		s.logger.Info("Metrics server shutting down", "reason", ctx.Err())

		// Graceful shutdown with timeout
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := s.server.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("Metrics server shutdown error", "error", err)
			return fmt.Errorf("server shutdown failed: %w", err)
		}

		s.logger.Info("Metrics server stopped")
		return nil

	case err := <-serverErr:
		return fmt.Errorf("server error: %w", err)
	}
}

// handleRoot provides a simple landing page with link to metrics endpoint.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Metrics</title></head>
<body>
<h1>Metrics</h1>
<p><a href="/metrics">Prometheus Metrics</a></p>
</body>
</html>
`)
}

// Addr returns the address the server is configured to listen on.
func (s *Server) Addr() string {
	s.addrMu.RLock()
	defer s.addrMu.RUnlock()
	return s.addr
}
