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

// Package dataplane provides a simple, high-level API for synchronizing HAProxy configurations
// via the Dataplane API.
//
// The library handles all complexity internally:
//   - Fetches current configuration from the Dataplane API
//   - Parses both current and desired configurations
//   - Computes a fine-grained ConfigDiff to classify changes as runtime-eligible
//     server-field updates (weight, address, port, maintenance, agent checks) vs.
//     structural changes
//   - Applies the desired configuration by pushing it in full in a single request
//     (no per-operation transactions): a skip-reload raw push carrying an
//     X-Runtime-Actions header when every change is runtime-eligible, otherwise a
//     force-reload raw push
//   - Retries transient connection failures (the master socket is briefly down
//     while HAProxy re-execs on reload)
//   - Returns detailed results including applied changes and reload information
//
// # Basic Usage (Recommended)
//
// For production use, create a Client to reuse connections across multiple operations:
//
//	endpoint := &dataplane.Endpoint{
//	    URL:      "http://haproxy:5555/v3",
//	    Username: "admin",
//	    Password: "secret",
//	}
//
//	// Create client once, reuse for multiple operations
//	client, err := dataplane.NewClient(context.Background(), endpoint)
//	if err != nil {
//	    slog.Error("Failed to create client", "error", err)
//	    os.Exit(1)
//	}
//	defer client.Close()
//
//	desiredConfig := `
//	global
//	    daemon
//	defaults
//	    mode http
//	    timeout client 30s
//	    timeout server 30s
//	    timeout connect 5s
//	backend web
//	    balance roundrobin
//	    server srv1 192.168.1.10:80 check
//	`
//
//	result, err := client.Sync(ctx, desiredConfig, nil, nil)
//	if err != nil {
//	    slog.Error("Sync failed", "error", err)
//	    os.Exit(1)
//	}
//
//	fmt.Printf("Applied %d operations\n", len(result.AppliedOperations))
//	if result.ReloadTriggered {
//	    fmt.Printf("HAProxy reloaded (ID: %s)\n", result.ReloadID)
//	}
//
// # Simple One-Off Operations
//
// For quick scripts, use the convenience functions (creates client internally):
//
//	result, err := dataplane.Sync(ctx, endpoint, desiredConfig, nil, nil)
//
// # Custom Options
//
// Configure sync behavior with options:
//
//	client, err := dataplane.NewClient(ctx, endpoint)
//	if err != nil {
//	    return err
//	}
//	defer client.Close()
//
//	opts := &dataplane.SyncOptions{
//	    Timeout:                   3 * time.Minute, // Overall timeout
//	    VerifyReload:              true,            // Poll reload status after sync
//	    ReloadVerificationTimeout: 10 * time.Second,
//	}
//
//	result, err := client.Sync(ctx, desiredConfig, nil, opts)
//
// # Error Handling
//
// The library provides detailed, actionable error messages:
//
//	client, err := dataplane.NewClient(ctx, endpoint)
//	if err != nil {
//	    return err
//	}
//	defer client.Close()
//
//	result, err := client.Sync(ctx, desiredConfig, nil, nil)
//	if err != nil {
//	    if syncErr, ok := errors.AsType[*dataplane.SyncError](err); ok {
//	        fmt.Printf("Stage: %s\n", syncErr.Stage)
//	        fmt.Printf("Error: %s\n", syncErr.Message)
//	        for _, hint := range syncErr.Hints {
//	            fmt.Printf("  Hint: %s\n", hint)
//	        }
//	    }
//	}
package dataplane

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

// Client manages a persistent connection to the HAProxy Dataplane API.
// It reuses connections for multiple operations, making it efficient for
// repeated sync operations.
//
// For production use with multiple operations, create a Client explicitly:
//
//	client, err := dataplane.NewClient(ctx, endpoint)
//	if err != nil {
//	    return err
//	}
//	defer client.Close()
//
//	// Reuse client for multiple operations
//	result1, err := client.Sync(ctx, config1, auxFiles1, opts)
//	result2, err := client.Sync(ctx, config2, auxFiles2, opts)
type Client struct {
	// Endpoint contains connection information
	Endpoint Endpoint

	// orchestrator handles internal sync logic
	orch *orchestrator
}

// NewClient creates a new Client for the given endpoint.
// The client reuses connections for multiple operations.
//
// Example:
//
//	// NewClient takes endpoint by pointer (so the controller can mutate
//	// the cached version fields on the same struct).
//	endpoint := &dataplane.Endpoint{
//	    URL:      "http://haproxy:5555/v3",
//	    Username: "admin",
//	    Password: "secret",
//	}
//
//	client, err := dataplane.NewClient(ctx, endpoint)
//	if err != nil {
//	    return fmt.Errorf("creating client: %w", err)
//	}
//	defer client.Close()
//
//	result, err := client.Sync(ctx, desiredConfig, nil, nil)
func NewClient(ctx context.Context, endpoint *Endpoint) (*Client, error) {
	// Create logger with pod context
	logger := slog.Default().With("pod", endpoint.PodName)

	// Create dataplane client
	// Pass cached version info to avoid redundant /v3/info calls
	c, err := client.NewFromEndpoint(ctx, &client.Endpoint{
		URL:                endpoint.URL,
		Username:           endpoint.Username,
		Password:           endpoint.Password,
		PodName:            endpoint.PodName,
		CachedMajorVersion: endpoint.DetectedMajorVersion,
		CachedMinorVersion: endpoint.DetectedMinorVersion,
		CachedFullVersion:  endpoint.DetectedFullVersion,
	}, logger)
	if err != nil {
		return nil, NewConnectionError(endpoint.URL, err)
	}

	// Create orchestrator with the same logger
	orch, err := newOrchestrator(c, logger)
	if err != nil {
		return nil, fmt.Errorf("creating orchestrator: %w", err)
	}

	return &Client{
		Endpoint: *endpoint,
		orch:     orch,
	}, nil
}

// Close releases client resources. The current implementation has no
// background work to clean up, but the method is part of the documented
// API so existing `defer client.Close()` call sites stay valid as the
// client gains owned resources.
func (c *Client) Close() error {
	return nil
}

// Sync synchronizes the desired HAProxy configuration using this client.
//
// This method:
//  1. Fetches the current configuration from the Dataplane API
//  2. Parses both current and desired configurations
//  3. Compares them to compute a fine-grained ConfigDiff, classifying changes
//     as runtime-eligible server-field updates vs. structural changes
//  4. Applies the desired configuration with a single full-config push (no
//     per-operation transactions): a skip-reload raw push carrying
//     X-Runtime-Actions when every change is runtime-eligible, otherwise a
//     force-reload raw push
//  5. Retries transient connection failures across HAProxy's reload re-exec
//  6. Returns detailed results including applied changes and reload information
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - desiredConfig: The desired HAProxy configuration as a string
//   - auxFiles: Auxiliary files to sync (use nil for defaults)
//   - opts: Sync options (use nil for defaults)
//
// Returns:
//   - *SyncResult: Detailed information about the sync operation
//   - error: Detailed error with actionable hints if the sync fails
//
// Example:
//
//	client, err := dataplane.NewClient(ctx, endpoint)
//	if err != nil {
//	    return err
//	}
//	defer client.Close()
//
//	result, err := client.Sync(ctx, desiredConfig, nil, nil)
//	if err != nil {
//	    return fmt.Errorf("sync failed: %w", err)
//	}
//
//	fmt.Printf("Applied %d operations in %v\n", len(result.AppliedOperations), result.Duration)
func (c *Client) Sync(ctx context.Context, desiredConfig string, auxFiles *AuxiliaryFiles, opts *SyncOptions) (*SyncResult, error) {
	// Use default options if none provided
	if opts == nil {
		opts = DefaultSyncOptions()
	}

	// Use default auxiliary files if none provided
	if auxFiles == nil {
		auxFiles = DefaultAuxiliaryFiles()
	}

	// Apply timeout if specified
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// Execute sync
	return c.orch.sync(ctx, desiredConfig, opts, auxFiles)
}

// SyncRuntimeFast is the runtime-raw apply: a single raw config push of the
// DESIRED config body with skip_reload+skip_version, carrying the precomputed
// render diff's runtime `set server` actions (X-Runtime-Actions). No per-pod
// fetch and no reload — cost is O(config size) for the push but independent of
// the number of pods (the diff is computed once and shared across pods).
//
// updates is the precomputed runtime-eligible render diff
// (ComputeRuntimeServerUpdates); desiredConfig is the render whose server
// addresses/states the actions move the live worker to. Callers: the deployer's
// runtime-raw lane (a purely runtime-eligible render — this is the only apply),
// and the scheduler's pre-interval apply (the runtime subset of a STRUCTURAL
// render, applied off the interval-gated path before the gated reload). When the
// body carries structural changes they land on disk un-activated until that
// gated reload — never hidden from a reload indefinitely. Reloads are
// config-driven (no server-state-file — ADR-0011), so the change persists across
// any later structural reload because that deploy re-renders the current endpoints.
func (c *Client) SyncRuntimeFast(ctx context.Context, updates *RuntimeServerUpdates, desiredConfig string, opts *SyncOptions) (*SyncResult, error) {
	if opts == nil {
		opts = DefaultSyncOptions()
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	return c.orch.syncRuntimeRawPush(ctx, desiredConfig, updates, opts, time.Now())
}
