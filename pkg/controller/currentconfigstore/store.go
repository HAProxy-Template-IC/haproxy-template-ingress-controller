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
	contentHash   string         // Hash of last parsed content to skip redundant parsing
	parser        *parser.Parser // Reused parser instance (DRY)
	logger        *slog.Logger
}

// New creates a new CurrentConfigStore.
func New(logger *slog.Logger) (*Store, error) {
	p, err := parser.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create parser: %w", err)
	}
	return &Store{
		parser: p,
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
	s.mu.Unlock()
	s.logger.Debug(reason)
}

// Update parses and stores the config from an unstructured HAProxyCfg resource.
// Pass nil to clear the stored config.
func (s *Store) Update(resource interface{}) {
	// Handle both untyped nil and typed nil (e.g., (*unstructured.Unstructured)(nil))
	if resource == nil {
		s.clear("current config cleared (no HAProxyCfg)")
		return
	}

	u, ok := resource.(*unstructured.Unstructured)
	if !ok {
		s.logger.Warn("unexpected resource type", "type", fmt.Sprintf("%T", resource))
		return
	}

	// Handle typed nil - when interface has type but nil concrete value
	if u == nil {
		s.clear("current config cleared (typed nil HAProxyCfg)")
		return
	}

	content, found, err := unstructured.NestedString(u.Object, "spec", "content")
	if err != nil {
		s.logger.Debug("failed to extract spec.content", "error", err)
	}
	if !found || content == "" {
		s.clear("HAProxyCfg has no content")
		return
	}

	s.updateWithContent(u, content)
}

// updateWithContent handles the content parsing and caching logic.
func (s *Store) updateWithContent(u *unstructured.Unstructured, content string) {
	// Decompress if needed
	isCompressed, _, _ := unstructured.NestedBool(u.Object, "spec", "compressed")
	if isCompressed {
		decompressed, err := compression.Decompress(content)
		if err != nil {
			s.logger.Warn("failed to decompress current config", "error", err)
			return
		}
		content = decompressed
	}

	// Compute hash to detect content changes and skip redundant parsing.
	// This is critical for performance: HAProxyCfg status updates trigger
	// Update() even when spec.content hasn't changed.
	hash := sha256.Sum256([]byte(content))
	hashStr := hex.EncodeToString(hash[:])

	s.mu.RLock()
	unchanged := s.contentHash == hashStr
	s.mu.RUnlock()

	if unchanged {
		s.logger.Debug("current config unchanged, skipping parse")
		return
	}

	parsed, err := s.parser.ParseFromString(content)
	if err != nil {
		s.logger.Warn("failed to parse current config", "error", err)
		return
	}

	s.mu.Lock()
	s.currentConfig = parsed
	s.contentHash = hashStr
	s.mu.Unlock()
	s.logger.Debug("current config updated", "backends", len(parsed.Backends))
}
