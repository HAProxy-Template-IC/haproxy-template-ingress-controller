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

package pluggablevalidator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// ManagerConfig captures one entry from `spec.validators` on
// HAProxyTemplateConfig. Validation of these values (RFC 1123 name,
// absolute socket path, positive timeout) is performed by the CRD's
// OpenAPI schema before this struct is built; the Manager treats the
// fields as already-clean.
type ManagerConfig struct {
	// Name is the operator-facing validator identifier.
	Name string
	// SocketPath is the absolute filesystem path to the validator's unix
	// domain socket.
	SocketPath string
	// Plugins lists the `[plugins.params.<name>]` subtree names this
	// validator handles. Empty means "validate the whole hub TOML"
	// (the validator decides what to do with the full config).
	Plugins []string
	// Timeout is the per-call deadline. Zero falls back to DefaultTimeout.
	Timeout time.Duration
}

// Manager is the controller-side entry point for the validator-sidecar
// feature. It owns one Client per configured validator plus a shared
// content-hash result cache. Concurrent calls are safe — Clients each
// open their own connection per call (see wire-protocol contract).
//
// Usage from the admission webhook (added in a follow-up MR):
//
//	resp, err := mgr.Validate(ctx, "coraza", &pv.Request{...})
//	if err != nil { ... } // misuse only — transport failures are in resp
//	for _, d := range resp.Errors { ... } // surface in admission denial
//
// Construction validates that validator names are unique. Configs with
// duplicate names cause New to fail; the CRD's OpenAPI schema enforces
// uniqueness too, so this is a defensive check rather than a primary
// validation surface.
type Manager struct {
	logger  *slog.Logger
	clients map[string]*Client
	cache   *ResultCache
	configs []ManagerConfig // preserved for Healthy() iteration order
}

// NewManager builds a Manager from the parsed `spec.validators` slice.
// A zero-length slice produces a no-op Manager whose Validate always
// returns an error (no validators configured). The cache is created
// internally with DefaultCacheCapacity.
func NewManager(logger *slog.Logger, configs []ManagerConfig) (*Manager, error) {
	if logger == nil {
		logger = slog.Default()
	}
	clients := make(map[string]*Client, len(configs))
	for _, cfg := range configs {
		if cfg.Name == "" {
			return nil, errors.New("validator config: empty name")
		}
		if cfg.SocketPath == "" {
			return nil, fmt.Errorf("validator %q: empty socketPath", cfg.Name)
		}
		if _, exists := clients[cfg.Name]; exists {
			return nil, fmt.Errorf("validator %q: duplicate name", cfg.Name)
		}
		clients[cfg.Name] = NewClient(cfg.Name, cfg.SocketPath, cfg.Timeout)
	}
	return &Manager{
		logger:  logger.With(slog.String("component", "pluggablevalidator")),
		clients: clients,
		cache:   NewResultCache(DefaultCacheCapacity),
		configs: append([]ManagerConfig(nil), configs...),
	}, nil
}

// Configured reports whether any validators are registered. Callers
// (webhook, /healthz) skip the dispatch when no validators are
// configured.
func (m *Manager) Configured() bool {
	return len(m.clients) > 0
}

// Names returns the validator names in the order they were registered.
// For deterministic iteration in callers that need a stable order
// (e.g., /healthz output formatting).
func (m *Manager) Names() []string {
	out := make([]string, 0, len(m.configs))
	for _, c := range m.configs {
		out = append(out, c.Name)
	}
	return out
}

// PluginsFor returns the plugin subtree list configured for the given
// validator, or nil if the validator is unknown. Used by the webhook
// (in a follow-up MR) to slice the rendered hub TOML before sending.
func (m *Manager) PluginsFor(name string) []string {
	for _, c := range m.configs {
		if c.Name == name {
			return append([]string(nil), c.Plugins...)
		}
	}
	return nil
}

// Validate dispatches a request to the named validator and returns the
// resulting Response. On cache hit, the round-trip is skipped. Returns
// (nil, error) only when the caller misuses the API (unknown validator
// name, nil request); transport / protocol failures are surfaced as a
// ProtocolError Response with a non-nil return value.
func (m *Manager) Validate(ctx context.Context, validatorName string, req *Request) (*Response, error) {
	if req == nil {
		return nil, errors.New("manager.Validate: nil request")
	}
	client, ok := m.clients[validatorName]
	if !ok {
		return nil, fmt.Errorf("manager.Validate: unknown validator %q", validatorName)
	}

	// Build cache key from the wire-encoded body so the cache reflects
	// what the server would actually see. The encoder will be called
	// again inside Client.Validate; that's two marshal calls per cache
	// miss, which is negligible relative to a network round-trip.
	cacheKey, err := buildCacheKey(validatorName, req)
	if err != nil {
		return ProtocolError(fmt.Sprintf(
			"validator %q: build cache key: %v", validatorName, err,
		)), nil
	}
	if cached, hit := m.cache.Get(cacheKey); hit {
		m.logger.Debug("cache hit", slog.String("validator", validatorName))
		return cached, nil
	}

	resp, err := client.Validate(ctx, req)
	if err != nil {
		// Misuse path (nil request); we already guarded above so this
		// shouldn't fire. Surface defensively.
		return ProtocolError(fmt.Sprintf(
			"validator %q: %v", validatorName, err,
		)), nil
	}

	// Cache real validator responses, including warning/error ones —
	// those are deterministic functions of the input under the wire-
	// protocol's purity contract, and that includes plugin panics or
	// file-level errors that legitimately use `path: ""`. Do NOT
	// cache transport failures (connect refused, decode failure)
	// because the validator may be in a transient bad state and we
	// want subsequent calls to retry. The synthetic marker on the
	// Response distinguishes the two cases out-of-band so we don't
	// have to inspect diagnostic paths.
	if resp.IsSynthetic() {
		m.logger.Debug("transport failure (not cached)",
			slog.String("validator", validatorName))
		return resp, nil
	}
	m.cache.Put(cacheKey, resp)
	return resp, nil
}

// Healthy reports whether every configured validator socket passes
// HealthCheck. Returns (true, nil) when all are healthy; otherwise
// returns (false, failures) where each failure entry is
// "<validator-name>: <reason>". Iteration order matches Names().
//
// Designed for /healthz injection — sub-millisecond happy path so it
// can run on every Kubernetes probe interval.
func (m *Manager) Healthy() (ok bool, failures []string) {
	if len(m.configs) == 0 {
		return true, nil
	}
	failures = make([]string, 0, len(m.configs))
	for _, c := range m.configs {
		if err := HealthCheck(c.SocketPath); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", c.Name, err))
		}
	}
	if len(failures) > 0 {
		return false, failures
	}
	return true, nil
}

// buildCacheKey marshals the request body to bytes and derives the
// content-hash key. Returns an error if the request can't be encoded
// (oversized payload, etc.) — the caller surfaces it as a protocol
// error.
func buildCacheKey(validatorName string, req *Request) (CacheKey, error) {
	// Reuse the encoder's invariant checks via a simulated write to
	// /dev/null-like sink; we only need the body bytes for hashing.
	// Build a minimal canonical form by JSON-marshaling the request
	// directly (the wire body is the same JSON; we drop the length
	// prefix because it's not part of the request identity).
	body, err := marshalRequest(req)
	if err != nil {
		return CacheKey{}, err
	}
	return NewCacheKey(validatorName, body), nil
}
