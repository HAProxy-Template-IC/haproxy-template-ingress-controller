// Package currentconfigstore provides a utility component for caching the parsed
// current HAProxy configuration from the HAProxyCfg CRD.
//
// This is a utility component that can be called directly without events.
// It follows the codebase's utility component pattern for infrastructure concerns.
package currentconfigstore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/compression"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// Store holds the parsed current HAProxy configuration.
// This is a utility component that can be called directly without events.
type Store struct {
	mu            sync.RWMutex
	currentConfig *parserconfig.StructuredConfig
	// contentHash is SHA-256 of the config text alone — the identity that decides
	// whether a re-parse is needed. spec.checksum covers the auxiliary files too, so
	// it changes on map-file churn that leaves the config byte-identical.
	contentHash    string
	lastChecksum   string // Last seen spec.checksum, to skip decompression on an exact repeat
	lastGeneration int64  // Last seen metadata.generation for fast spec-change detection
	logger         *slog.Logger
}

// New creates a new CurrentConfigStore.
func New(logger *slog.Logger) (*Store, error) {
	return &Store{
		logger: logger.With("component", "currentconfigstore"),
	}, nil
}

// Get returns the current parsed config (may be nil).
func (s *Store) Get() *parserconfig.StructuredConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentConfig
}

// clear resets the stored config and hash.
func (s *Store) clear(reason string) {
	s.mu.Lock()
	s.currentConfig = nil
	s.contentHash = ""
	s.lastChecksum = ""
	s.mu.Unlock()
	s.logger.Debug(reason)
}

// Update parses and stores the config from an unstructured HAProxyCfg resource.
// Pass nil to clear the stored config.
func (s *Store) Update(resource any) {
	// Handle both untyped nil and typed nil (e.g., (*unstructured.Unstructured)(nil))
	if resource == nil {
		s.clear("current config cleared (no HAProxyCfg)")
		return
	}

	u, ok := resource.(*unstructured.Unstructured)
	if !ok {
		s.logger.Warn("Unexpected resource type", "type", fmt.Sprintf("%T", resource))
		return
	}

	// Handle typed nil - when interface has type but nil concrete value
	if u == nil {
		s.clear("current config cleared (typed nil HAProxyCfg)")
		return
	}

	content, found, err := unstructured.NestedString(u.Object, "spec", "content")
	if err != nil {
		s.logger.Debug("Failed to extract spec.content", "error", err)
	}
	if !found || content == "" {
		s.clear("HAProxyCfg has no content")
		return
	}

	s.updateWithContent(u, content)
}

// updateWithContent handles the content parsing and caching logic.
func (s *Store) updateWithContent(u *unstructured.Unstructured, content string) {
	generation := u.GetGeneration()
	if s.skipByGeneration(generation) {
		return
	}

	specChecksum, _, _ := unstructured.NestedString(u.Object, "spec", "checksum")
	if s.skipByChecksum(specChecksum, generation) {
		return
	}

	isCompressed, _, _ := unstructured.NestedBool(u.Object, "spec", "compressed")
	if isCompressed {
		decompressed, err := compression.Decompress(content)
		if err != nil {
			s.logger.Warn("Failed to decompress current config", "error", err)
			return
		}
		content = decompressed
	}

	// Hash the config text itself. spec.checksum cannot stand in for this: it covers the
	// auxiliary files too (dataplane.ComputeContentChecksum), so endpoint churn rewriting a
	// map file bumps it while the config stays byte-identical — and a re-parse of an
	// unchanged config costs tens of MB of retained heap at a few hundred routes.
	hash := sha256.Sum256([]byte(content))
	hashStr := hex.EncodeToString(hash[:])
	if s.skipByContentHash(hashStr, specChecksum, generation) {
		return
	}

	// Constructed per parse and discarded. A reused parser keeps client-native's
	// internal maps and slices sized for the largest configuration it ever saw —
	// ~200 MB resident here after one 1000-route churn, against a live
	// configuration of 70 KiB — because neither shrinks. Construction is 89µs
	// against a 44ms parse.
	p, err := parser.New()
	if err != nil {
		s.logger.Warn("Failed to create parser for current config", "error", err)
		return
	}
	parsed, err := p.ParseFromString(content)
	if err != nil {
		s.logger.Warn("Failed to parse current config", "error", err)
		return
	}

	s.mu.Lock()
	s.currentConfig = parsed
	s.contentHash = hashStr
	s.lastChecksum = specChecksum
	s.lastGeneration = generation
	s.mu.Unlock()
	s.logger.Debug("Current config updated", "backends", len(parsed.Backends), "generation", generation)
}

// skipByGeneration reports whether the spec is unchanged. The HAProxyCfg CRD has a status
// subresource, so metadata.generation only moves on spec writes; frequent status-only
// updates are discarded here before decompressing or hashing.
func (s *Store) skipByGeneration(generation int64) bool {
	if generation <= 0 {
		return false
	}
	s.mu.RLock()
	match := generation == s.lastGeneration && s.currentConfig != nil
	s.mu.RUnlock()
	if match {
		s.logger.Debug("Current config unchanged (generation match), skipping parse",
			"generation", generation)
	}
	return match
}

// skipByChecksum reports whether spec.checksum proves nothing changed. It covers the
// configuration AND its auxiliary files, so a match rules out any difference below. The
// converse does not hold, so a mismatch falls through to the content hash instead of
// deciding anything.
func (s *Store) skipByChecksum(specChecksum string, generation int64) bool {
	if specChecksum == "" {
		return false
	}
	s.mu.RLock()
	match := s.lastChecksum == specChecksum && s.currentConfig != nil
	s.mu.RUnlock()
	if !match {
		return false
	}
	s.mu.Lock()
	s.lastGeneration = generation
	s.mu.Unlock()
	s.logger.Debug("Current config unchanged (spec.checksum match), skipping decompression",
		"generation", generation)
	return true
}

// skipByContentHash reports whether the configuration text is byte-identical to the parsed
// one, recording the new checksum and generation so later repeats hit the cheaper gates.
func (s *Store) skipByContentHash(hashStr, specChecksum string, generation int64) bool {
	s.mu.RLock()
	match := s.contentHash == hashStr && s.currentConfig != nil
	s.mu.RUnlock()
	if !match {
		return false
	}
	s.mu.Lock()
	s.lastChecksum = specChecksum
	s.lastGeneration = generation
	s.mu.Unlock()
	s.logger.Debug("Current config unchanged (content hash match), skipping parse",
		"generation", generation)
	return true
}
