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
	lastChecksum   string         // Last seen spec.checksum, to skip decompression on an exact repeat
	lastGeneration int64          // Last seen metadata.generation for fast spec-change detection
	parser         *parser.Parser // Reused parser instance (DRY)
	logger         *slog.Logger

	// offered holds a parse the controller already performed for the exact
	// bytes it published, keyed by their SHA-256. The HAProxyCfg this store
	// watches is written by this controller, so by the time the watch delivers
	// it, the pipeline has usually parsed those bytes already — re-parsing them
	// costs a second full pass and a second retained copy of the same
	// configuration. Keyed by content hash so it can only ever satisfy
	// byte-identical content; anything else falls through to a real parse.
	offeredHash   string
	offeredConfig *parserconfig.StructuredConfig
}

// Offer hands the store a parse of config text the controller is publishing, so
// the watch event for those same bytes does not have to parse them again.
//
// The hash is of the configuration text alone, matching the identity the store
// uses internally. Offering is advisory: a mismatch, or an offer that never
// arrives, simply leaves the normal parse path in place.
func (s *Store) Offer(configText string, parsed *parserconfig.StructuredConfig) {
	if parsed == nil || configText == "" {
		return
	}
	sum := sha256.Sum256([]byte(configText))
	s.mu.Lock()
	s.offeredHash = hex.EncodeToString(sum[:])
	s.offeredConfig = parsed
	s.mu.Unlock()
}

// takeOffered returns a previously offered parse for these exact bytes, if any.
func (s *Store) takeOffered(hash string) *parserconfig.StructuredConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.offeredHash != hash || s.offeredConfig == nil {
		return nil
	}
	parsed := s.offeredConfig
	// Release the reference: the store is about to hold it as currentConfig,
	// and keeping a second pointer would pin the previous generation too.
	s.offeredHash, s.offeredConfig = "", nil
	return parsed
}

// New creates a new CurrentConfigStore.
func New(logger *slog.Logger) (*Store, error) {
	p, err := parser.New()
	if err != nil {
		return nil, fmt.Errorf("creating parser: %w", err)
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
// parseOrAdopt returns the parse for content, preferring one the controller
// already performed for these exact bytes. Returns nil when parsing fails, in
// which case the store keeps its previous configuration.
func (s *Store) parseOrAdopt(hashStr, content string) *parserconfig.StructuredConfig {
	if parsed := s.takeOffered(hashStr); parsed != nil {
		s.logger.Debug("Adopted an already-parsed current config", "bytes", len(content))
		return parsed
	}
	parsed, err := s.parser.ParseFromString(content)
	if err != nil {
		s.logger.Warn("Failed to parse current config", "error", err)
		return nil
	}
	return parsed
}

func (s *Store) updateWithContent(u *unstructured.Unstructured, content string) {
	// Fast path: Check metadata.generation before decompressing or hashing.
	// The HAProxyCfg CRD has status subresource enabled, so metadata.generation
	// only increments on spec changes. Status-only updates (which are frequent)
	// can be skipped entirely if generation hasn't changed.
	generation := u.GetGeneration()
	if generation > 0 {
		s.mu.RLock()
		lastGen := s.lastGeneration
		hasConfig := s.currentConfig != nil
		s.mu.RUnlock()

		if generation == lastGen && hasConfig {
			s.logger.Debug("Current config unchanged (generation match), skipping parse",
				"generation", generation)
			return
		}
	}

	// Fast path: an unchanged spec.checksum proves the config AND its auxiliary files are
	// unchanged, so nothing below can differ. The converse does not hold — see the content
	// hash below — so a mismatch here must fall through rather than decide anything.
	specChecksum, _, _ := unstructured.NestedString(u.Object, "spec", "checksum")
	if specChecksum != "" {
		s.mu.RLock()
		checksumMatch := s.lastChecksum == specChecksum && s.currentConfig != nil
		s.mu.RUnlock()

		if checksumMatch {
			// Content unchanged — update generation without decompressing or parsing
			s.mu.Lock()
			s.lastGeneration = generation
			s.mu.Unlock()
			s.logger.Debug("Current config unchanged (spec.checksum match), skipping decompression",
				"generation", generation)
			return
		}
	}

	// Decompress if needed
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

	s.mu.RLock()
	unchanged := s.contentHash == hashStr && s.currentConfig != nil
	s.mu.RUnlock()

	if unchanged {
		s.mu.Lock()
		s.lastChecksum = specChecksum
		s.lastGeneration = generation
		s.mu.Unlock()
		s.logger.Debug("Current config unchanged (content hash match), skipping parse",
			"generation", generation)
		return
	}

	parsed := s.parseOrAdopt(hashStr, content)
	if parsed == nil {
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
