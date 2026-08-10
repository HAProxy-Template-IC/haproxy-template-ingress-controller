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

// Package dataplane synchronizes HAProxy configurations via the Dataplane API:
// it fetches and parses the current config, computes a fine-grained ConfigDiff
// classifying each change as a runtime-eligible server-field update or as
// structural, then pushes the desired config in one raw request — skip-reload
// with an X-Runtime-Actions header when every change is runtime-eligible,
// force-reload otherwise — retrying the connection failures HAProxy's reload
// re-exec causes.
//
//	client, err := dataplane.NewClient(ctx, &dataplane.Endpoint{
//	    URL: "http://haproxy:5555/v3", Username: "admin", Password: "secret",
//	})
//	if err != nil {
//	    return err
//	}
//	defer client.Close()
//
//	result, err := client.Sync(ctx, desiredConfig, nil, nil)
//
// Pass a *SyncOptions to override the defaults (overall timeout, reload
// verification). Sync errors carry a *SyncError with a Stage and actionable
// Hints; see errors.AsType.
package dataplane

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

// Client holds a persistent connection to one HAProxy Dataplane API endpoint
// and is meant to be reused across sync operations.
type Client struct {
	// Endpoint contains connection information
	Endpoint Endpoint

	// orchestrator handles internal sync logic
	orch *orchestrator
}

// NewClient creates a Client for the given endpoint. The endpoint is taken by
// pointer so the controller can mutate the cached version fields on the same
// struct.
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

// Sync applies desiredConfig to this endpoint, running the workflow described
// in the package doc. nil auxFiles and nil opts select the defaults.
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

// SyncRuntimeFast is the runtime-raw apply: a single raw config push of body
// with skip_reload+skip_version, carrying the precomputed render diff's
// runtime `set server` actions (X-Runtime-Actions). No per-pod fetch and no
// reload — cost is O(config size) for the push but independent of the number
// of pods (the diff is computed once and shared across pods).
//
// updates is the precomputed runtime-eligible render diff
// (ComputeRuntimeServerUpdates); body is the config the push writes to disk
// and whose server addresses/states the actions move the live worker to. It
// MUST be derived from the last reload-ACTIVATED config — never a pending
// render with structural content (issue #84, see syncRuntimeRawPush):
//   - the deployer's runtime-raw lane passes the render itself, which is
//     structurally identical to the activated baseline by lane construction;
//   - the scheduler's fast-track subset apply passes the baseline patched with
//     only the runtime-eligible server lines (BuildRuntimeBypassBody).
//
// Reloads are config-driven (no server-state-file — ADR-0011), so the change
// persists across any later structural reload because that deploy re-renders
// the current endpoints.
func (c *Client) SyncRuntimeFast(ctx context.Context, updates *RuntimeServerUpdates, body string, opts *SyncOptions) (*SyncResult, error) {
	if opts == nil {
		opts = DefaultSyncOptions()
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	return c.orch.syncRuntimeRawPush(ctx, body, updates, opts, time.Now())
}
