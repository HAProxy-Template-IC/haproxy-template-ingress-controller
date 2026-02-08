// Package parser provides HAProxy configuration parsing using client-native library.
//
// This package wraps the haproxytech/client-native parser to parse HAProxy
// configurations from strings (in-memory, no disk I/O) into structured representations
// suitable for comparison and API operations.
//
// Semantic validation (checking resource availability, directive compatibility, etc.)
// is NOT performed here - that is handled by the external haproxy binary in later stages.
package parser

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	parser "github.com/haproxytech/client-native/v6/config-parser"
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// parserMutex protects against concurrent calls to the client-native parser.
//
// WORKAROUND: The upstream client-native library has a package-level global variable
// (config-parser/parser.go:65 "var DefaultSectionName") that is written during parsing
// without synchronization. This causes data races when multiple parsers are used concurrently.
//
// This mutex serializes all parsing operations to prevent the race condition.
// See: https://github.com/haproxytech/client-native/blob/v6.2.5/config-parser/parser.go#L65
//
// PERFORMANCE IMPACT: This mutex serializes ALL parsing operations across the entire
// controller, including concurrent webhook validations and reconciliations. In high-load
// scenarios with many concurrent validations, this can become a bottleneck.
//
// STATUS (checked 2025-12-06): Issue still exists in client-native v6.2.5. The global
// variable has a //nolint:gochecknoglobals comment indicating awareness but no fix.
// Consider checking for updates in newer versions or filing an upstream issue.
var parserMutex sync.Mutex

// ParsedConfigCacheSize defines the number of parsed configurations to cache.
// This value balances memory usage with cache hit rate:
//   - 1 slot for desired config (rendered template)
//   - 2 slots for current configs (one per HAProxy pod)
//   - 1 extra slot for rolling update transitions
//
// With 4 slots at ~34 MB each, the cache uses ~136 MB instead of ~544 MB with 16 slots.
// LRU eviction ensures the most recently used configs are retained.
const ParsedConfigCacheSize = 4

// cacheSlot holds a single cached parsed configuration.
type cacheSlot struct {
	hash   string
	config *StructuredConfig
}

// parsedConfigCache provides a content-based LRU cache for parsed configurations.
// This dramatically reduces allocations when the same configuration is parsed
// multiple times (e.g., when syncing to N endpoints with the same desired config).
//
// The cache uses a map for O(1) lookups and a slice for LRU ordering.
// When the cache is full, the oldest entry (front of slice) is evicted.
//
// IMPORTANT: The cached StructuredConfig is already normalized (metadata fields
// flattened) and MUST NOT be mutated. Callers should not call NormalizeConfigMetadata
// on cached configs - they are already normalized during caching.
type parsedConfigCache struct {
	mu        sync.Mutex
	entries   map[string]*cacheSlot // Hash -> entry
	order     []string              // LRU order: oldest first, newest at end
	maxSize   int
	hitCount  atomic.Int64
	missCount atomic.Int64
}

var configCache = &parsedConfigCache{
	entries: make(map[string]*cacheSlot, ParsedConfigCacheSize),
	order:   make([]string, 0, ParsedConfigCacheSize),
	maxSize: ParsedConfigCacheSize,
}

// get returns a cached parsed config if the hash exists, nil otherwise.
// On cache hit, moves the entry to the end of the LRU order (most recently used).
func (c *parsedConfigCache) get(hash string) *StructuredConfig {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[hash]
	if !ok || entry.config == nil {
		return nil
	}

	// Move to end of order (most recently used)
	c.moveToEnd(hash)
	c.hitCount.Add(1)
	return entry.config
}

// moveToEnd moves the given hash to the end of the LRU order.
// Must be called with c.mu held.
func (c *parsedConfigCache) moveToEnd(hash string) {
	// Find and remove from current position
	for i, h := range c.order {
		if h == hash {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	// Append to end (most recently used)
	c.order = append(c.order, hash)
}

// set stores a parsed config in the cache, evicting the least recently used entry if full.
func (c *parsedConfigCache) set(hash string, config *StructuredConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if this hash already exists (update in place)
	if _, exists := c.entries[hash]; exists {
		c.entries[hash].config = config
		c.moveToEnd(hash)
		return
	}

	// Evict oldest if full
	if len(c.entries) >= c.maxSize {
		oldest := c.order[0]
		delete(c.entries, oldest)
		c.order = c.order[1:]
	}

	// Add new entry
	c.entries[hash] = &cacheSlot{hash: hash, config: config}
	c.order = append(c.order, hash)
}

// CacheStats returns the current cache hit/miss statistics.
// Useful for debugging and metrics.
func CacheStats() (hits, misses int64) {
	return configCache.hitCount.Load(), configCache.missCount.Load()
}

// hashConfig computes a SHA256 hash of the configuration string.
func hashConfig(config string) string {
	h := sha256.Sum256([]byte(config))
	return hex.EncodeToString(h[:])
}

// Parser wraps client-native's config-parser for parsing HAProxy configurations.
type Parser struct {
	parser parser.Parser
}

// StructuredConfig is a type alias for types.StructuredConfig.
// This alias is provided for backward compatibility with existing code.
// New code should import from haptic/pkg/dataplane/parser/parserconfig.
type StructuredConfig = parserconfig.StructuredConfig

// New creates a new Parser instance.
//
// The parser uses client-native's config-parser which provides robust parsing
// of HAProxy configuration syntax without requiring file I/O.
func New() (*Parser, error) {
	p, err := parser.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create parser: %w", err)
	}
	return &Parser{
		parser: p,
	}, nil
}

// ParseFromString parses an HAProxy configuration string into a structured representation.
//
// The configuration string should contain valid HAProxy configuration syntax.
// Returns a StructuredConfig containing all parsed sections (global, defaults,
// frontends, backends, etc.) suitable for comparison and synchronization.
//
// Syntax validation is performed as part of parsing - any syntax errors will be returned.
// Semantic validation (resource availability, directive compatibility) is performed
// by HAProxy via the Dataplane API during configuration application.
//
// Example:
//
//	config := `
//	global
//	    daemon
//	defaults
//	    mode http
//	backend web
//	    balance roundrobin
//	    server srv1 192.168.1.10:80
//	`
//	parser, _ := parser.New()
//	structured, err := parser.ParseFromString(config)
func (p *Parser) ParseFromString(config string) (*StructuredConfig, error) {
	if config == "" {
		return nil, fmt.Errorf("configuration string is empty")
	}

	// Check cache first (fast path - no mutex needed for cache check)
	// This dramatically reduces allocations when syncing the same desired config
	// to multiple endpoints.
	hash := hashConfig(config)
	if cached := configCache.get(hash); cached != nil {
		return cached, nil
	}

	// Lock to prevent concurrent access to client-native parser
	// (protects against upstream race condition in DefaultSectionName global variable)
	parserMutex.Lock()
	defer parserMutex.Unlock()

	// Double-check cache after acquiring lock (another goroutine may have parsed)
	if cached := configCache.get(hash); cached != nil {
		return cached, nil
	}

	// Parse directly from string - NO file I/O
	// This keeps all config data in memory as required
	// Syntax validation happens automatically during parsing
	if err := p.parser.Process(strings.NewReader(config)); err != nil {
		return nil, fmt.Errorf("failed to parse configuration: %w", err)
	}

	// Extract structured configuration from parser
	conf, err := p.extractConfiguration()
	if err != nil {
		return nil, fmt.Errorf("failed to extract configuration: %w", err)
	}

	// Normalize metadata before caching to ensure cached configs are immutable.
	// This prevents data races when multiple goroutines retrieve the same cached
	// config and try to normalize it concurrently.
	NormalizeConfigMetadata(conf)

	// Cache the result for future requests with the same config
	configCache.set(hash, conf)
	configCache.missCount.Add(1)

	return conf, nil
}

// extractConfiguration builds a StructuredConfig from the parsed data.
//
// This reads all sections (global, defaults, frontends, backends, etc.)
// from the client-native parser and assembles them into a complete
// configuration structure.
//
// Note: This extracts the parsed structure but does NOT validate semantics.
// The config-parser only ensures syntax correctness.
func (p *Parser) extractConfiguration() (*StructuredConfig, error) {
	conf := &StructuredConfig{
		// Initialize pointer-based indexes for zero-copy iteration
		ServerIndex:         make(map[string]map[string]*models.Server),
		ServerTemplateIndex: make(map[string]map[string]*models.ServerTemplate),
		BindIndex:           make(map[string]map[string]*models.Bind),
		PeerEntryIndex:      make(map[string]map[string]*models.PeerEntry),
		NameserverIndex:     make(map[string]map[string]*models.Nameserver),
		MailerEntryIndex:    make(map[string]map[string]*models.MailerEntry),
		UserIndex:           make(map[string]map[string]*models.User),
		GroupIndex:          make(map[string]map[string]*models.Group),
	}

	// Extract core sections (global, defaults, frontends, backends)
	if err := p.extractCoreSections(conf); err != nil {
		return nil, err
	}

	// Extract peer and service discovery sections (peers, resolvers, mailers)
	p.extractPeerAndDiscoverySections(conf)

	// Extract service sections (caches, rings, http-errors, userlists)
	if err := p.extractServiceSections(conf); err != nil {
		return nil, err
	}

	// Extract program and application sections (programs, log-forwards, fcgi-apps, crt-stores)
	if err := p.extractProgramSections(conf); err != nil {
		return nil, err
	}

	// Extract observability sections (log-profiles, traces) - v3.1+ features
	if err := p.extractObservabilitySections(conf); err != nil {
		return nil, err
	}

	// Extract certificate automation sections (acme) - v3.2+ features
	if err := p.extractCertificateSections(conf); err != nil {
		return nil, err
	}

	return conf, nil
}

// extractCoreSections extracts core HAProxy sections (global, defaults, frontends, backends).
func (p *Parser) extractCoreSections(conf *StructuredConfig) error {
	global, err := p.extractGlobal()
	if err != nil {
		return fmt.Errorf("failed to extract global section: %w", err)
	}
	conf.Global = global

	defaults, err := p.extractDefaults()
	if err != nil {
		return fmt.Errorf("failed to extract defaults sections: %w", err)
	}
	conf.Defaults = defaults

	p.extractFrontendsWithIndexes(conf)
	p.extractBackendsWithIndexes(conf)

	return nil
}

// extractPeerAndDiscoverySections extracts peer and service discovery sections
// and builds pointer indexes for nested entries.
func (p *Parser) extractPeerAndDiscoverySections(conf *StructuredConfig) {
	p.extractPeersWithIndexes(conf)
	p.extractResolversWithIndexes(conf)
	p.extractMailersWithIndexes(conf)
}

// extractServiceSections extracts service sections (caches, rings, http-errors, userlists).
func (p *Parser) extractServiceSections(conf *StructuredConfig) error {
	caches, err := p.extractCaches()
	if err != nil {
		return fmt.Errorf("failed to extract caches: %w", err)
	}
	conf.Caches = caches

	rings, err := p.extractRings()
	if err != nil {
		return fmt.Errorf("failed to extract rings: %w", err)
	}
	conf.Rings = rings

	httpErrors, err := p.extractHTTPErrors()
	if err != nil {
		return fmt.Errorf("failed to extract http-errors: %w", err)
	}
	conf.HTTPErrors = httpErrors

	p.extractUserlistsWithIndexes(conf)

	return nil
}

// extractProgramSections extracts program and application sections.
func (p *Parser) extractProgramSections(conf *StructuredConfig) error {
	programs, err := p.extractPrograms()
	if err != nil {
		return fmt.Errorf("failed to extract programs: %w", err)
	}
	conf.Programs = programs

	logForwards, err := p.extractLogForwards()
	if err != nil {
		return fmt.Errorf("failed to extract log-forwards: %w", err)
	}
	conf.LogForwards = logForwards

	fcgiApps, err := p.extractFCGIApps()
	if err != nil {
		return fmt.Errorf("failed to extract fcgi-apps: %w", err)
	}
	conf.FCGIApps = fcgiApps

	crtStores, err := p.extractCrtStores()
	if err != nil {
		return fmt.Errorf("failed to extract crt-stores: %w", err)
	}
	conf.CrtStores = crtStores

	return nil
}

// extractObservabilitySections extracts observability sections (log-profiles, traces).
// These are v3.1+ features for advanced logging and request tracing.
func (p *Parser) extractObservabilitySections(conf *StructuredConfig) error {
	logProfiles, err := p.extractLogProfiles()
	if err != nil {
		return fmt.Errorf("failed to extract log-profiles: %w", err)
	}
	conf.LogProfiles = logProfiles

	conf.Traces = p.extractTraces()

	return nil
}

// extractCertificateSections extracts certificate automation sections (acme).
// These are v3.2+ features for ACME/Let's Encrypt certificate automation.
func (p *Parser) extractCertificateSections(conf *StructuredConfig) error {
	acmeProviders, err := p.extractAcmeProviders()
	if err != nil {
		return fmt.Errorf("failed to extract acme providers: %w", err)
	}
	conf.AcmeProviders = acmeProviders

	return nil
}
