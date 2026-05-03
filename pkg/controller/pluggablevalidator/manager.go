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
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// DefaultMaxParallelDispatch caps the number of concurrent
// (validator, file) round-trips ValidateAll runs in parallel.
// Per-validator connection-pool ceilings still apply, so this
// is a top-level safety net rather than a primary throttle —
// a render with 200 files spread across 3 validators won't
// spawn 600 goroutines all racing for socket time. Sized for
// typical reconciliation bursts.
const DefaultMaxParallelDispatch = 16

// ManagerConfig captures one entry from `spec.validators` on
// HAProxyTemplateConfig. Validation of these values (RFC 1123 name,
// absolute socket path, valid globs, positive timeout) is performed
// by the CRD's OpenAPI schema before this struct is built; the
// Manager treats the fields as already-clean.
type ManagerConfig struct {
	// Name is the operator-facing validator identifier.
	Name string
	// SocketPath is the absolute filesystem path to the validator's
	// unix domain socket.
	SocketPath string
	// Files is the list of glob patterns matched against rendered
	// file paths to decide which files to send to this validator.
	// Patterns follow Go's `path/filepath.Match` rules; absolute
	// paths only.
	Files []string
	// Timeout is the per-call deadline for one (file, validator)
	// request-response cycle. Zero falls back to DefaultTimeout.
	Timeout time.Duration
	// MaxConnections caps the controller-side pool to this
	// validator's socket. Zero falls back to DefaultMaxConnections.
	MaxConnections int
}

// Manager is the controller-side entry point for the validator-
// sidecar feature. It owns one Client per configured validator plus
// a shared content-hash result cache. Concurrent calls are safe;
// per-validator pools handle in-process parallelism.
//
// Routing model: the webhook hands the Manager every rendered file
// produced by the dry-run. For each (file, validator) pair where
// the validator's globs match the file's path, the Manager either
// returns the cached Response or sends a single-file request frame
// over the socket. All resulting diagnostics are concatenated and
// returned as one slice. The webhook surfaces the warnings via
// `AdmissionResponse.Warnings` (kubectl prints them as soft
// warnings) and the errors as the admission denial reason.
//
// Construction validates that validator names are unique. Configs
// with duplicate names cause New to fail; the CRD's OpenAPI schema
// enforces uniqueness too, so this is a defensive check rather than
// a primary validation surface.
type Manager struct {
	logger  *slog.Logger
	clients map[string]*Client
	cache   *ResultCache
	configs []ManagerConfig // preserved for Healthy() iteration order
}

// NewManager builds a Manager from the parsed `spec.validators`
// slice. A zero-length slice produces a no-op Manager whose
// Configured() returns false.
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
		if len(cfg.Files) == 0 {
			return nil, fmt.Errorf("validator %q: empty files glob list", cfg.Name)
		}
		if _, exists := clients[cfg.Name]; exists {
			return nil, fmt.Errorf("validator %q: duplicate name", cfg.Name)
		}
		// Validate every glob is well-formed by trying to match an
		// arbitrary string. filepath.Match returns ErrBadPattern
		// for syntactically broken globs; the actual match result
		// is irrelevant.
		for _, g := range cfg.Files {
			if _, err := filepath.Match(g, "/probe"); err != nil {
				return nil, fmt.Errorf("validator %q: invalid file glob %q: %w", cfg.Name, g, err)
			}
		}
		clients[cfg.Name] = NewClient(cfg.Name, cfg.SocketPath, cfg.Timeout, cfg.MaxConnections)
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

// Names returns the validator names in the order they were
// registered. For deterministic iteration in callers that need a
// stable order (e.g., /healthz output formatting).
func (m *Manager) Names() []string {
	out := make([]string, 0, len(m.configs))
	for _, c := range m.configs {
		out = append(out, c.Name)
	}
	return out
}

// FilesFor returns the glob list configured for the given
// validator, or nil if the validator is unknown. For tests and
// debug introspection.
func (m *Manager) FilesFor(name string) []string {
	for _, c := range m.configs {
		if c.Name == name {
			return append([]string(nil), c.Files...)
		}
	}
	return nil
}

// ValidationOutcome bundles the warnings + errors collected across
// all (file, validator) round-trips for one ValidateAll call. The
// caller maps these to the admission webhook's response shape:
// warnings → `AdmissionResponse.Warnings`, errors → admission
// denial reason. A non-nil ValidationOutcome with zero entries in
// both lists is the equivalent of `result: "valid"`.
type ValidationOutcome struct {
	Warnings []Diagnostic
	Errors   []Diagnostic
}

// Result computes the aggregate result string from the populated
// lists. Mirrors the wire-protocol's per-response `result` field
// computation but at the cross-validator aggregate level.
func (o *ValidationOutcome) Result() Result {
	if len(o.Errors) > 0 {
		return ResultError
	}
	if len(o.Warnings) > 0 {
		return ResultWarning
	}
	return ResultValid
}

// dispatchTask captures one (validator, file) round-trip the
// fan-out scheduler needs to run.
type dispatchTask struct {
	validatorName string
	client        *Client
	file          File
}

// ValidateAll fans the rendered files out to every configured
// validator whose globs match. Tasks run in parallel, bounded by
// DefaultMaxParallelDispatch — independent (validator, file) pairs
// are independent work units (different sockets, different
// content, no shared state) so there's no benefit to running them
// serially. Per-validator connection pools throttle within-
// validator concurrency.
//
// Returns aggregated diagnostics. The returned outcome is always
// non-nil; an empty outcome means nothing matched any glob (or no
// validators are configured). Diagnostics are sorted by
// (path, line, column, validator-name) so output is deterministic
// across runs even though tasks complete out of order.
//
// On transport failure to a single validator, that validator's
// errors are surfaced and the others continue. The caller decides
// whether to treat partial failures as admission denials (typical
// fail-closed behaviour: yes).
func (m *Manager) ValidateAll(ctx context.Context, files []File) *ValidationOutcome {
	out := &ValidationOutcome{}
	if !m.Configured() || len(files) == 0 {
		return out
	}

	// Build the dispatch list. Each (validator, matched-file) pair
	// is one task. Order doesn't matter for correctness; we sort
	// the diagnostics at the end.
	var tasks []dispatchTask
	for _, vcfg := range m.configs {
		client := m.clients[vcfg.Name]
		for _, f := range matchFiles(files, vcfg.Files) {
			tasks = append(tasks, dispatchTask{
				validatorName: vcfg.Name,
				client:        client,
				file:          f,
			})
		}
	}
	if len(tasks) == 0 {
		return out
	}

	// Cap concurrency to avoid spawning hundreds of goroutines on
	// pathological renders. Per-validator connection pools further
	// throttle the effective concurrency against any single
	// validator.
	concurrency := DefaultMaxParallelDispatch
	if concurrency > len(tasks) {
		concurrency = len(tasks)
	}
	sem := make(chan struct{}, concurrency)

	var (
		mu       sync.Mutex
		warnings []Diagnostic
		errs     []Diagnostic
		wg       sync.WaitGroup
	)
	for _, task := range tasks {
		// Honor context cancellation while waiting for a slot —
		// otherwise a cancelled call still iterates through every
		// pending (validator, file) pair before returning, each
		// failing fast on the cancelled context. For pathological
		// renders with hundreds of pairs that's avoidable latency.
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			// Stop dispatching. Already-spawned goroutines run to
			// completion (they observe ctx.Done() inside
			// client.Validate and bail with a synthetic error).
			goto wait
		}
		wg.Add(1)
		go func(task dispatchTask) {
			defer wg.Done()
			defer func() { <-sem }()
			resp := m.validateOne(ctx, task.client, task.validatorName, task.file)
			mu.Lock()
			warnings = append(warnings, resp.Warnings...)
			errs = append(errs, resp.Errors...)
			mu.Unlock()
		}(task)
	}
wait:
	wg.Wait()

	sortDiagnostics(warnings)
	sortDiagnostics(errs)
	out.Warnings = warnings
	out.Errors = errs
	return out
}

// sortDiagnostics sorts in place by (path, line, column, message)
// so concurrent dispatch produces deterministic output for tests
// and deterministic admission denial messages for operators
// looking at `kubectl apply` errors twice in a row.
func sortDiagnostics(diags []Diagnostic) {
	sort.Slice(diags, func(i, j int) bool {
		a, b := diags[i], diags[j]
		switch {
		case a.Path != b.Path:
			return a.Path < b.Path
		case a.Line != b.Line:
			return a.Line < b.Line
		case a.Column != b.Column:
			return a.Column < b.Column
		default:
			return a.Message < b.Message
		}
	})
}

// validateOne dispatches a single file to a validator with cache
// hit-skip. Used internally by ValidateAll.
func (m *Manager) validateOne(ctx context.Context, client *Client, validatorName string, file File) *Response {
	key := NewCacheKey(validatorName, file.Path, []byte(file.Content))
	if cached, hit := m.cache.Get(key); hit {
		m.logger.Debug("cache hit",
			slog.String("validator", validatorName),
			slog.String("path", file.Path))
		return cached
	}

	req := &Request{
		ProtocolVersion: ProtocolVersion,
		Files:           []File{file},
	}
	resp, err := client.Validate(ctx, req)
	if err != nil {
		// Misuse path (nil request); we don't get here in normal
		// use since we just built the request. Surface defensively.
		return ProtocolError(fmt.Sprintf(
			"validator %q: %v", validatorName, err,
		))
	}

	// Cache real validator responses, including warning/error ones
	// — those are deterministic functions of the input under the
	// wire-protocol's purity contract. Do NOT cache transport
	// failures (synthetic ProtocolError responses) so a transient
	// sidecar outage doesn't poison subsequent admissions.
	if resp.IsSynthetic() {
		m.logger.Debug("transport failure (not cached)",
			slog.String("validator", validatorName),
			slog.String("path", file.Path))
		return resp
	}
	m.cache.Put(key, resp)
	return resp
}

// matchFiles returns the subset of `files` whose path matches any
// of `globs` (Go filepath.Match semantics). Order is preserved so
// downstream output is stable across calls. A file matching
// multiple globs is included once (first match wins).
func matchFiles(files []File, globs []string) []File {
	if len(files) == 0 || len(globs) == 0 {
		return nil
	}
	matched := make([]File, 0, len(files))
	for _, f := range files {
		for _, g := range globs {
			ok, err := filepath.Match(g, f.Path)
			if err != nil || !ok {
				continue
			}
			matched = append(matched, f)
			break
		}
	}
	return matched
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

// Close shuts every per-validator pool. Used during iteration
// teardown.
func (m *Manager) Close() {
	for _, c := range m.clients {
		c.Close()
	}
}
