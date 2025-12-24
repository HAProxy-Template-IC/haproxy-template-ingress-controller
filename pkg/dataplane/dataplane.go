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
//   - Generates fine-grained operations to transform current → desired
//   - Executes operations with automatic retry on version conflicts (409 errors)
//   - Falls back to raw config push on non-recoverable errors
//   - Returns detailed results including applied changes and reload information
//
// # Basic Usage (Recommended)
//
// For production use, create a Client to reuse connections across multiple operations:
//
//	endpoint := dataplane.Endpoint{
//	    URL:      "http://haproxy:5555/v2",
//	    Username: "admin",
//	    Password: "secret",
//	}
//
//	// Create client once, reuse for multiple operations
//	client, err := dataplane.NewClient(context.Background(), endpoint)
//	if err != nil {
//	    slog.Error("failed to create client", "error", err)
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
//	    slog.Error("sync failed", "error", err)
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
//	    MaxRetries:      5,                 // Retry 409 conflicts up to 5 times
//	    Timeout:         3 * time.Minute,   // Overall timeout
//	    ContinueOnError: false,             // Stop on first error
//	    FallbackToRaw:   true,              // Fall back to raw push on errors
//	}
//
//	result, err := client.Sync(ctx, desiredConfig, nil, opts)
//
// # Dry Run
//
// Preview changes without applying them:
//
//	client, err := dataplane.NewClient(ctx, endpoint)
//	if err != nil {
//	    return err
//	}
//	defer client.Close()
//
//	diff, err := client.DryRun(ctx, desiredConfig)
//	if err != nil {
//	    slog.Error("dry run failed", "error", err)
//	    os.Exit(1)
//	}
//
//	fmt.Printf("Would apply %d operations:\n", len(diff.PlannedOperations))
//	for _, op := range diff.PlannedOperations {
//	    fmt.Printf("  - %s\n", op.Description)
//	}
//
// # Diff Only
//
// Get detailed diff information:
//
//	client, err := dataplane.NewClient(ctx, endpoint)
//	if err != nil {
//	    return err
//	}
//	defer client.Close()
//
//	diff, err := client.Diff(ctx, desiredConfig)
//	if err != nil {
//	    slog.Error("diff failed", "error", err)
//	    os.Exit(1)
//	}
//
//	fmt.Printf("Backends added: %v\n", diff.Details.BackendsAdded)
//	fmt.Printf("Servers modified: %d\n", len(diff.Details.ServersModified))
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
//	    var syncErr *dataplane.SyncError
//	    if errors.As(err, &syncErr) {
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

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/client"
)

// Client manages a persistent connection to the HAProxy Dataplane API.
// It reuses connections for multiple operations, making it efficient for
// repeated sync operations.
//
// For simple one-off operations, use the package-level convenience functions
// (Sync, DryRun, Diff) which create a client internally.
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
//	endpoint := dataplane.Endpoint{
//	    URL:      "http://haproxy:5555/v2",
//	    Username: "admin",
//	    Password: "secret",
//	}
//
//	client, err := dataplane.NewClient(ctx, endpoint)
//	if err != nil {
//	    return fmt.Errorf("failed to create client: %w", err)
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
		return nil, fmt.Errorf("failed to create orchestrator: %w", err)
	}

	return &Client{
		Endpoint: *endpoint,
		orch:     orch,
	}, nil
}

// Close cleans up client resources.
// Currently a no-op, but provided for future resource cleanup needs.
func (c *Client) Close() error {
	// Future: close HTTP connections, cleanup resources
	return nil
}

// Sync synchronizes the desired HAProxy configuration using this client.
//
// This method:
//  1. Fetches the current configuration from the Dataplane API
//  2. Parses both current and desired configurations
//  3. Compares them to generate fine-grained operations
//  4. Executes operations with automatic retry on 409 version conflicts
//  5. Falls back to raw config push on non-recoverable errors (if enabled)
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

// DryRun previews what changes would be applied without actually applying them.
//
// This method performs all the same steps as Sync except for the actual application:
//  1. Fetches the current configuration from the Dataplane API
//  2. Parses both current and desired configurations
//  3. Compares them to generate a list of planned operations
//  4. Returns the diff without executing any operations
//
// This is useful for:
//   - Previewing changes before applying them
//   - Validating configurations
//   - Understanding what would change
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - desiredConfig: The desired HAProxy configuration as a string
//
// Returns:
//   - *DiffResult: Detailed information about planned changes
//   - error: Error if comparison fails
//
// Example:
//
//	client, err := dataplane.NewClient(ctx, endpoint)
//	if err != nil {
//	    return err
//	}
//	defer client.Close()
//
//	diff, err := client.DryRun(ctx, desiredConfig)
//	if err != nil {
//	    return fmt.Errorf("dry run failed: %w", err)
//	}
//
//	if diff.HasChanges {
//	    fmt.Printf("Would apply %d operations:\n", len(diff.PlannedOperations))
//	    for _, op := range diff.PlannedOperations {
//	        fmt.Printf("  - %s %s %s\n", op.Type, op.Section, op.Resource)
//	    }
//	}
func (c *Client) DryRun(ctx context.Context, desiredConfig string) (*DiffResult, error) {
	return c.orch.diff(ctx, desiredConfig)
}

// Diff compares the current and desired configurations and returns detailed differences.
//
// This is an alias for DryRun - both methods perform the same operation.
// Use whichever name makes more sense in your context.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - desiredConfig: The desired HAProxy configuration as a string
//
// Returns:
//   - *DiffResult: Detailed information about differences
//   - error: Error if comparison fails
//
// Example:
//
//	client, err := dataplane.NewClient(ctx, endpoint)
//	if err != nil {
//	    return err
//	}
//	defer client.Close()
//
//	diff, err := client.Diff(ctx, desiredConfig)
//	if err != nil {
//	    return fmt.Errorf("diff failed: %w", err)
//	}
//
//	fmt.Printf("Backends added: %v\n", diff.Details.BackendsAdded)
//	fmt.Printf("Backends modified: %v\n", diff.Details.BackendsModified)
//	fmt.Printf("Servers deleted: %d total\n", len(diff.Details.ServersDeleted))
func (c *Client) Diff(ctx context.Context, desiredConfig string) (*DiffResult, error) {
	return c.DryRun(ctx, desiredConfig)
}

// Package-level convenience functions for simple one-off operations.
// These create a client internally for each call.
// For multiple operations, create a Client explicitly to reuse connections.

// Sync synchronizes the desired HAProxy configuration to the dataplane endpoint.
//
// This is a convenience function that creates a client internally for one-off operations.
// For production use with multiple operations, create a Client explicitly to reuse connections:
//
//	client, err := dataplane.NewClient(ctx, endpoint)
//	if err != nil {
//	    return err
//	}
//	defer client.Close()
//	result, err := client.Sync(ctx, desiredConfig, auxFiles, opts)
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - endpoint: Dataplane API connection information
//   - desiredConfig: The desired HAProxy configuration as a string
//   - auxFiles: Auxiliary files to sync (use nil for defaults)
//   - opts: Sync options (use nil for defaults)
//
// Returns:
//   - *SyncResult: Detailed information about the sync operation
//   - error: Detailed error with actionable hints if the sync fails
func Sync(ctx context.Context, endpoint *Endpoint, desiredConfig string, auxFiles *AuxiliaryFiles, opts *SyncOptions) (*SyncResult, error) {
	cli, err := NewClient(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	defer cli.Close()

	return cli.Sync(ctx, desiredConfig, auxFiles, opts)
}

// DryRun previews what changes would be applied without actually applying them.
//
// This is a convenience function that creates a client internally for one-off operations.
// For production use with multiple operations, create a Client explicitly.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - endpoint: Dataplane API connection information
//   - desiredConfig: The desired HAProxy configuration as a string
//
// Returns:
//   - *DiffResult: Detailed information about planned changes
//   - error: Error if comparison fails
func DryRun(ctx context.Context, endpoint *Endpoint, desiredConfig string) (*DiffResult, error) {
	cli, err := NewClient(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	defer cli.Close()

	return cli.DryRun(ctx, desiredConfig)
}

// Diff compares the current and desired configurations and returns detailed differences.
//
// This is a convenience function that creates a client internally for one-off operations.
// This is an alias for DryRun. For production use with multiple operations,
// create a Client explicitly.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - endpoint: Dataplane API connection information
//   - desiredConfig: The desired HAProxy configuration as a string
//
// Returns:
//   - *DiffResult: Detailed information about differences
//   - error: Error if comparison fails
func Diff(ctx context.Context, endpoint *Endpoint, desiredConfig string) (*DiffResult, error) {
	return DryRun(ctx, endpoint, desiredConfig)
}
