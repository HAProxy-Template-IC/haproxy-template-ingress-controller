// Package currentconfigstore provides a utility component for caching the parsed
// current HAProxy configuration from the HAProxyCfg CRD.
//
// This is a utility component that can be called directly without events.
// It follows the codebase's utility component pattern for infrastructure concerns.
package currentconfigstore

import (
	"fmt"
	"log/slog"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// Store holds the parsed current HAProxy configuration.
// This is a utility component that can be called directly without events.
type Store struct {
	mu            sync.RWMutex
	currentConfig *parserconfig.StructuredConfig
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

// Update parses and stores the config from an unstructured HAProxyCfg resource.
// Pass nil to clear the stored config.
func (s *Store) Update(resource interface{}) {
	// Handle both untyped nil and typed nil (e.g., (*unstructured.Unstructured)(nil))
	if resource == nil {
		s.mu.Lock()
		s.currentConfig = nil
		s.mu.Unlock()
		s.logger.Debug("current config cleared (no HAProxyCfg)")
		return
	}

	u, ok := resource.(*unstructured.Unstructured)
	if !ok {
		s.logger.Warn("unexpected resource type", "type", fmt.Sprintf("%T", resource))
		return
	}

	// Handle typed nil - when interface has type but nil concrete value
	if u == nil {
		s.mu.Lock()
		s.currentConfig = nil
		s.mu.Unlock()
		s.logger.Debug("current config cleared (typed nil HAProxyCfg)")
		return
	}

	content, found, err := unstructured.NestedString(u.Object, "spec", "content")
	if err != nil {
		s.logger.Debug("failed to extract spec.content", "error", err)
	}
	if !found || content == "" {
		s.mu.Lock()
		s.currentConfig = nil
		s.mu.Unlock()
		s.logger.Debug("HAProxyCfg has no content")
		return
	}

	parsed, err := s.parser.ParseFromString(content)
	if err != nil {
		s.logger.Warn("failed to parse current config", "error", err)
		return
	}

	s.mu.Lock()
	s.currentConfig = parsed
	s.mu.Unlock()
	s.logger.Debug("current config updated", "backends", len(parsed.Backends))
}
