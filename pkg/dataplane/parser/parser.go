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
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	parser "github.com/haproxytech/client-native/v6/config-parser"
	"github.com/haproxytech/client-native/v6/configuration"
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
// This value is chosen to accommodate concurrent webhook validations (which can
// parse multiple different configs simultaneously) plus normal reconciliation
// (current vs desired config). With 16 slots, even with 10+ concurrent validations,
// the working set should fit in cache.
const ParsedConfigCacheSize = 16

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

// extractGlobal extracts the global section using client-native's ParseGlobalSection.
//
// This automatically handles ALL global fields (100+ fields including maxconn, daemon,
// nbproc, nbthread, pidfile, stats sockets, chroot, user, group, tune options, SSL options,
// performance options, lua options, etc.) and all nested structures (PerformanceOptions,
// TuneOptions, LogTargets, etc.) without manual handling.
func (p *Parser) extractGlobal() (*models.Global, error) {
	// ParseGlobalSection handles the complete Global section including all nested structures
	global, err := configuration.ParseGlobalSection(p.parser)
	if err != nil {
		return nil, fmt.Errorf("failed to parse global section: %w", err)
	}

	// Parse log targets separately (nested structure)
	// Global section has no name (empty string)
	logTargets, err := configuration.ParseLogTargets(string(parser.Global), "", p.parser)
	if err == nil {
		global.LogTargetList = logTargets
	}

	return global, nil
}

// extractDefaults extracts all defaults sections using client-native's ParseSection.
//
// This automatically handles ALL defaults fields (60+ fields including mode, maxconn,
// timeout settings, log settings, options like httplog/dontlognull/forwardfor,
// error handling, compression, etc.) without manual type assertions.
func (p *Parser) extractDefaults() ([]*models.Defaults, error) {
	sections, err := p.parser.SectionsGet(parser.Defaults)
	if err != nil {
		// No defaults sections is valid
		return nil, err
	}

	defaults := make([]*models.Defaults, 0, len(sections))
	for _, sectionName := range sections {
		def := &models.Defaults{}

		// ParseSection handles ALL DefaultsBase fields automatically (60+ fields)
		if err := configuration.ParseSection(&def.DefaultsBase, parser.Defaults, sectionName, p.parser); err != nil {
			slog.Warn("Failed to parse defaults section", "section", sectionName, "error", err)
			continue
		}
		def.Name = sectionName

		// Parse log targets separately (nested structure)
		logTargets, err := configuration.ParseLogTargets(string(parser.Defaults), sectionName, p.parser)
		if err == nil {
			def.LogTargetList = logTargets
		}

		// Parse QUIC initial rules (v3.2+ feature for HTTP/3 support)
		def.QUICInitialRuleList, _ = configuration.ParseQUICInitialRules(string(parser.Defaults), sectionName, p.parser)

		defaults = append(defaults, def)
	}

	return defaults, nil
}

// extractFrontendsWithIndexes extracts all frontend sections and builds pointer indexes.
//
// This automatically handles ALL frontend fields (80+ fields) and nested structures
// (binds, ACLs, HTTP/TCP rules, filters, log targets, etc.) using specialized Parse* helpers.
// Binds are stored in BindIndex for zero-copy iteration during comparison.
func (p *Parser) extractFrontendsWithIndexes(conf *StructuredConfig) {
	sections, err := p.parser.SectionsGet(parser.Frontends)
	if err != nil {
		// No frontends is valid
		return
	}

	frontends := make([]*models.Frontend, 0, len(sections))
	for _, sectionName := range sections {
		fe := &models.Frontend{}

		// ParseSection handles ALL FrontendBase fields automatically (80+ fields:
		// mode, maxconn, default_backend, timeouts, compression, forwardfor, httplog, etc.)
		if err := configuration.ParseSection(&fe.FrontendBase, parser.Frontends, sectionName, p.parser); err != nil {
			slog.Warn("Failed to parse frontend section", "section", sectionName, "error", err)
			continue
		}
		fe.Name = sectionName

		// Parse nested structures using client-native's Parse* helpers
		fe.ACLList, _ = configuration.ParseACLs(parser.Frontends, sectionName, p.parser)

		// Parse binds and build pointer index for zero-copy iteration.
		// ParseBinds returns []*models.Bind - we store pointers directly in the index.
		binds, _ := configuration.ParseBinds(string(parser.Frontends), sectionName, p.parser)
		if binds != nil {
			bindIndex := make(map[string]*models.Bind, len(binds))
			for _, bind := range binds {
				if bind != nil {
					bindIndex[bind.Name] = bind // Store pointer directly, no copy
				}
			}
			conf.BindIndex[sectionName] = bindIndex
		}

		fe.HTTPRequestRuleList, _ = configuration.ParseHTTPRequestRules(string(parser.Frontends), sectionName, p.parser)
		fe.HTTPResponseRuleList, _ = configuration.ParseHTTPResponseRules(string(parser.Frontends), sectionName, p.parser)
		fe.TCPRequestRuleList, _ = configuration.ParseTCPRequestRules(string(parser.Frontends), sectionName, p.parser)
		fe.HTTPAfterResponseRuleList, _ = configuration.ParseHTTPAfterRules(string(parser.Frontends), sectionName, p.parser)
		fe.HTTPErrorRuleList, _ = configuration.ParseHTTPErrorRules(string(parser.Frontends), sectionName, p.parser)
		fe.FilterList, _ = configuration.ParseFilters(string(parser.Frontends), sectionName, p.parser)
		fe.LogTargetList, _ = configuration.ParseLogTargets(string(parser.Frontends), sectionName, p.parser)
		fe.BackendSwitchingRuleList, _ = configuration.ParseBackendSwitchingRules(sectionName, p.parser)
		fe.CaptureList, _ = configuration.ParseDeclareCaptures(sectionName, p.parser)
		// Parse QUIC initial rules (v3.2+ feature for HTTP/3 support)
		fe.QUICInitialRuleList, _ = configuration.ParseQUICInitialRules(string(parser.Frontends), sectionName, p.parser)

		frontends = append(frontends, fe)
	}

	conf.Frontends = frontends
}

// extractBackendsWithIndexes extracts all backend sections and builds pointer indexes.
//
// This automatically handles ALL backend fields (100+ fields) and nested structures
// (servers, ACLs, HTTP/TCP rules, filters, stick rules, health checks, etc.)
// using specialized Parse* helpers.
// Servers and ServerTemplates are stored in pointer indexes for zero-copy iteration.
func (p *Parser) extractBackendsWithIndexes(conf *StructuredConfig) {
	sections, err := p.parser.SectionsGet(parser.Backends)
	if err != nil {
		// No backends is valid
		return
	}

	backends := make([]*models.Backend, 0, len(sections))
	for _, sectionName := range sections {
		be := &models.Backend{}

		// ParseSection handles ALL BackendBase fields automatically (100+ fields:
		// mode, balance, timeouts, cookie, compression, forwardfor, httpchk, etc.)
		if err := configuration.ParseSection(&be.BackendBase, parser.Backends, sectionName, p.parser); err != nil {
			slog.Warn("Failed to parse backend section", "section", sectionName, "error", err)
			continue
		}
		be.Name = sectionName

		// Parse nested structures and build pointer indexes
		p.parseBackendNestedStructuresWithIndexes(sectionName, be, conf)

		backends = append(backends, be)
	}

	conf.Backends = backends
}

// parseBackendNestedStructuresWithIndexes parses all nested structures for a backend
// and builds pointer indexes for servers and server templates.
func (p *Parser) parseBackendNestedStructuresWithIndexes(sectionName string, be *models.Backend, conf *StructuredConfig) {
	// Parse ACLs
	be.ACLList, _ = configuration.ParseACLs(parser.Backends, sectionName, p.parser)

	// Parse servers and build pointer index for zero-copy iteration.
	// ParseServers returns []*models.Server - we store pointers directly in the index.
	servers, _ := configuration.ParseServers(string(parser.Backends), sectionName, p.parser)
	if servers != nil {
		serverIndex := make(map[string]*models.Server, len(servers))
		for _, server := range servers {
			if server != nil {
				serverIndex[server.Name] = server // Store pointer directly, no copy
			}
		}
		conf.ServerIndex[sectionName] = serverIndex
	}

	// Parse HTTP/TCP rules
	p.parseBackendRules(sectionName, be)

	// Parse filters, log targets, and checks
	p.parseBackendFiltersAndChecks(sectionName, be)

	// Parse server templates and build pointer index for zero-copy iteration.
	// ParseServerTemplates returns []*models.ServerTemplate - we store pointers directly.
	serverTemplates, _ := configuration.ParseServerTemplates(sectionName, p.parser)
	if serverTemplates != nil {
		templateIndex := make(map[string]*models.ServerTemplate, len(serverTemplates))
		for _, template := range serverTemplates {
			if template != nil {
				templateIndex[template.Prefix] = template // Store pointer directly, no copy
			}
		}
		conf.ServerTemplateIndex[sectionName] = templateIndex
	}
}

// parseBackendRules parses HTTP and TCP rules for a backend.
func (p *Parser) parseBackendRules(sectionName string, be *models.Backend) {
	be.HTTPRequestRuleList, _ = configuration.ParseHTTPRequestRules(string(parser.Backends), sectionName, p.parser)
	be.HTTPResponseRuleList, _ = configuration.ParseHTTPResponseRules(string(parser.Backends), sectionName, p.parser)
	be.TCPRequestRuleList, _ = configuration.ParseTCPRequestRules(string(parser.Backends), sectionName, p.parser)
	be.TCPResponseRuleList, _ = configuration.ParseTCPResponseRules(string(parser.Backends), sectionName, p.parser)
	be.HTTPAfterResponseRuleList, _ = configuration.ParseHTTPAfterRules(string(parser.Backends), sectionName, p.parser)
	be.HTTPErrorRuleList, _ = configuration.ParseHTTPErrorRules(string(parser.Backends), sectionName, p.parser)
	be.ServerSwitchingRuleList, _ = configuration.ParseServerSwitchingRules(sectionName, p.parser)
	be.StickRuleList, _ = configuration.ParseStickRules(sectionName, p.parser)
}

// parseBackendFiltersAndChecks parses filters, log targets, and health checks for a backend.
func (p *Parser) parseBackendFiltersAndChecks(sectionName string, be *models.Backend) {
	be.FilterList, _ = configuration.ParseFilters(string(parser.Backends), sectionName, p.parser)
	be.LogTargetList, _ = configuration.ParseLogTargets(string(parser.Backends), sectionName, p.parser)
	be.HTTPCheckList, _ = configuration.ParseHTTPChecks(string(parser.Backends), sectionName, p.parser)
	be.TCPCheckRuleList, _ = configuration.ParseTCPChecks(string(parser.Backends), sectionName, p.parser)
}

// extractPeersWithIndexes extracts all peers sections and builds pointer indexes.
func (p *Parser) extractPeersWithIndexes(conf *StructuredConfig) {
	sections, err := p.parser.SectionsGet(parser.Peers)
	if err != nil {
		return
	}

	peers := make([]*models.PeerSection, 0, len(sections))
	for _, sectionName := range sections {
		peer := &models.PeerSection{}

		// ParseSection handles all peer section fields
		if err := configuration.ParseSection(peer, parser.Peers, sectionName, p.parser); err != nil {
			slog.Warn("Failed to parse peers section", "section", sectionName, "error", err)
			continue
		}
		peer.Name = sectionName

		// Parse peer entries and build pointer index for zero-copy iteration.
		// ParsePeerEntries returns []*models.PeerEntry - we store pointers directly.
		peerEntries, _ := configuration.ParsePeerEntries(sectionName, p.parser)
		if peerEntries != nil {
			entryIndex := make(map[string]*models.PeerEntry, len(peerEntries))
			for _, entry := range peerEntries {
				if entry != nil {
					entryIndex[entry.Name] = entry // Store pointer directly, no copy
				}
			}
			conf.PeerEntryIndex[sectionName] = entryIndex
		}

		peers = append(peers, peer)
	}

	conf.Peers = peers
}

// extractResolversWithIndexes extracts all resolvers sections and builds pointer indexes.
func (p *Parser) extractResolversWithIndexes(conf *StructuredConfig) {
	sections, err := p.parser.SectionsGet(parser.Resolvers)
	if err != nil {
		return
	}

	resolvers := make([]*models.Resolver, 0, len(sections))
	for _, sectionName := range sections {
		resolver := &models.Resolver{}
		resolver.Name = sectionName

		// ParseResolverSection handles all resolver fields automatically
		if err := configuration.ParseResolverSection(p.parser, resolver); err != nil {
			slog.Warn("Failed to parse resolvers section", "section", sectionName, "error", err)
			continue
		}

		// Parse nameservers and build pointer index for zero-copy iteration.
		// ParseNameservers returns []*models.Nameserver - we store pointers directly.
		nameservers, _ := configuration.ParseNameservers(sectionName, p.parser)
		if nameservers != nil {
			nsIndex := make(map[string]*models.Nameserver, len(nameservers))
			for _, ns := range nameservers {
				if ns != nil {
					nsIndex[ns.Name] = ns // Store pointer directly, no copy
				}
			}
			conf.NameserverIndex[sectionName] = nsIndex
		}

		resolvers = append(resolvers, resolver)
	}

	conf.Resolvers = resolvers
}

// extractMailersWithIndexes extracts all mailers sections and builds pointer indexes.
func (p *Parser) extractMailersWithIndexes(conf *StructuredConfig) {
	sections, err := p.parser.SectionsGet(parser.Mailers)
	if err != nil {
		return
	}

	mailers := make([]*models.MailersSection, 0, len(sections))
	for _, sectionName := range sections {
		mailer := &models.MailersSection{}
		mailer.Name = sectionName

		// ParseMailersSection handles all mailer fields automatically
		if err := configuration.ParseMailersSection(p.parser, mailer); err != nil {
			slog.Warn("Failed to parse mailers section", "section", sectionName, "error", err)
			continue
		}

		// Parse mailer entries and build pointer index for zero-copy iteration.
		// ParseMailerEntries returns []*models.MailerEntry - we store pointers directly.
		mailerEntries, _ := configuration.ParseMailerEntries(sectionName, p.parser)
		if mailerEntries != nil {
			entryIndex := make(map[string]*models.MailerEntry, len(mailerEntries))
			for _, entry := range mailerEntries {
				if entry != nil {
					entryIndex[entry.Name] = entry // Store pointer directly, no copy
				}
			}
			conf.MailerEntryIndex[sectionName] = entryIndex
		}

		mailers = append(mailers, mailer)
	}

	conf.Mailers = mailers
}

// extractCaches extracts all cache sections using client-native's ParseCacheSection.
func (p *Parser) extractCaches() ([]*models.Cache, error) {
	sections, err := p.parser.SectionsGet(parser.Cache)
	if err != nil {
		return nil, err
	}

	caches := make([]*models.Cache, 0, len(sections))
	for _, sectionName := range sections {
		cache := &models.Cache{}
		name := sectionName
		cache.Name = &name

		// ParseCacheSection handles all cache fields automatically
		if err := configuration.ParseCacheSection(p.parser, cache); err != nil {
			slog.Warn("Failed to parse cache section", "section", sectionName, "error", err)
			continue
		}

		caches = append(caches, cache)
	}

	return caches, nil
}

// extractRings extracts all ring sections using client-native's ParseRingSection.
func (p *Parser) extractRings() ([]*models.Ring, error) {
	sections, err := p.parser.SectionsGet(parser.Ring)
	if err != nil {
		return nil, err
	}

	rings := make([]*models.Ring, 0, len(sections))
	for _, sectionName := range sections {
		ring := &models.Ring{}
		ring.Name = sectionName

		// ParseRingSection handles all ring fields automatically
		if err := configuration.ParseRingSection(p.parser, ring); err != nil {
			slog.Warn("Failed to parse ring section", "section", sectionName, "error", err)
			continue
		}

		rings = append(rings, ring)
	}

	return rings, nil
}

// extractHTTPErrors extracts all http-errors sections using client-native's Parse* functions.
func (p *Parser) extractHTTPErrors() ([]*models.HTTPErrorsSection, error) {
	sections, err := p.parser.SectionsGet(parser.HTTPErrors)
	if err != nil {
		return nil, err
	}

	httpErrors := make([]*models.HTTPErrorsSection, 0, len(sections))
	for _, sectionName := range sections {
		// ParseHTTPErrorsSection handles complete parsing including ErrorFiles
		httpError, err := configuration.ParseHTTPErrorsSection(p.parser, sectionName)
		if err != nil {
			// Log error but continue with other sections
			continue
		}

		httpErrors = append(httpErrors, httpError)
	}

	return httpErrors, nil
}

// extractUserlistsWithIndexes extracts all userlist sections and builds pointer indexes.
// Userlists contain users and groups for authentication.
func (p *Parser) extractUserlistsWithIndexes(conf *StructuredConfig) {
	sections, err := p.parser.SectionsGet(parser.UserList)
	if err != nil {
		return
	}

	userlists := make([]*models.Userlist, 0, len(sections))
	for _, sectionName := range sections {
		userlist := &models.Userlist{}
		userlist.Name = sectionName

		// Parse userlist base section
		if err := configuration.ParseSection(&userlist.UserlistBase, parser.UserList, sectionName, p.parser); err != nil {
			slog.Warn("Failed to parse userlist section", "section", sectionName, "error", err)
			continue
		}

		// Parse users and build pointer index for zero-copy iteration.
		users, _ := configuration.ParseUsers(sectionName, p.parser)
		if userIndex := parserconfig.BuildUserIndex(users); userIndex != nil {
			conf.UserIndex[sectionName] = userIndex
		}

		// Parse groups and build pointer index for zero-copy iteration.
		groups, _ := configuration.ParseGroups(sectionName, p.parser)
		if groupIndex := parserconfig.BuildGroupIndex(groups); groupIndex != nil {
			conf.GroupIndex[sectionName] = groupIndex
		}

		userlists = append(userlists, userlist)
	}

	conf.Userlists = userlists
}

// extractPrograms extracts all program sections using client-native's ParseProgram.
// Programs are external processes managed by HAProxy.
func (p *Parser) extractPrograms() ([]*models.Program, error) {
	sections, err := p.parser.SectionsGet(parser.Program)
	if err != nil {
		return nil, err
	}

	programs := make([]*models.Program, 0, len(sections))
	for _, sectionName := range sections {
		// ParseProgram handles all program fields automatically
		program, err := configuration.ParseProgram(p.parser, sectionName)
		if err != nil {
			slog.Warn("Failed to parse program section", "section", sectionName, "error", err)
			continue
		}

		programs = append(programs, program)
	}

	return programs, nil
}

// extractLogForwards extracts all log-forward sections using client-native's ParseLogForward.
// Log-forwards define log forwarding rules.
func (p *Parser) extractLogForwards() ([]*models.LogForward, error) {
	sections, err := p.parser.SectionsGet(parser.LogForward)
	if err != nil {
		return nil, err
	}

	logForwards := make([]*models.LogForward, 0, len(sections))
	for _, sectionName := range sections {
		// ParseLogForward takes a pointer to fill
		logForward := &models.LogForward{
			LogForwardBase: models.LogForwardBase{Name: sectionName},
		}
		if err := configuration.ParseLogForward(p.parser, logForward); err != nil {
			slog.Warn("Failed to parse log-forward section", "section", sectionName, "error", err)
			continue
		}

		logForwards = append(logForwards, logForward)
	}

	return logForwards, nil
}

// extractFCGIApps extracts all fcgi-app sections using client-native's ParseFCGIApp.
// FCGI apps define FastCGI application configurations.
func (p *Parser) extractFCGIApps() ([]*models.FCGIApp, error) {
	sections, err := p.parser.SectionsGet(parser.FCGIApp)
	if err != nil {
		return nil, err
	}

	fcgiApps := make([]*models.FCGIApp, 0, len(sections))
	for _, sectionName := range sections {
		// ParseFCGIApp handles all fields automatically
		fcgiApp, err := configuration.ParseFCGIApp(p.parser, sectionName)
		if err != nil {
			slog.Warn("Failed to parse fcgi-app section", "section", sectionName, "error", err)
			continue
		}

		fcgiApps = append(fcgiApps, fcgiApp)
	}

	return fcgiApps, nil
}

// extractCrtStores extracts all crt-store sections using client-native's ParseCrtStore.
// Certificate stores define locations for SSL certificates.
func (p *Parser) extractCrtStores() ([]*models.CrtStore, error) {
	sections, err := p.parser.SectionsGet(parser.CrtStore)
	if err != nil {
		return nil, err
	}

	crtStores := make([]*models.CrtStore, 0, len(sections))
	for _, sectionName := range sections {
		// ParseCrtStore handles all fields automatically
		crtStore, err := configuration.ParseCrtStore(p.parser, sectionName)
		if err != nil {
			slog.Warn("Failed to parse crt-store section", "section", sectionName, "error", err)
			continue
		}

		crtStores = append(crtStores, crtStore)
	}

	return crtStores, nil
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

// extractLogProfiles extracts all log-profile sections using client-native's ParseLogProfile.
// Log profiles define logging profiles for one or more steps (v3.1+ feature).
func (p *Parser) extractLogProfiles() ([]*models.LogProfile, error) {
	sections, err := p.parser.SectionsGet(parser.LogProfile)
	if err != nil {
		return nil, err
	}

	logProfiles := make([]*models.LogProfile, 0, len(sections))
	for _, sectionName := range sections {
		// ParseLogProfile handles all log-profile fields automatically
		logProfile, err := configuration.ParseLogProfile(p.parser, sectionName)
		if err != nil {
			slog.Warn("Failed to parse log-profile section", "section", sectionName, "error", err)
			continue
		}

		logProfiles = append(logProfiles, logProfile)
	}

	return logProfiles, nil
}

// extractTraces extracts the traces section using client-native's ParseTraces.
// Traces is a singleton section for request tracing configuration (v3.1+ feature).
// Returns nil when no traces section exists (which is valid - traces is optional).
func (p *Parser) extractTraces() *models.Traces {
	// Traces is a singleton - check if section exists
	if !p.parser.SectionExists(parser.Traces, parser.TracesSectionName) {
		return nil
	}

	// ParseTraces handles all traces fields automatically
	traces, err := configuration.ParseTraces(p.parser)
	if err != nil {
		slog.Warn("Failed to parse traces section", "error", err)
		return nil
	}

	return traces
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

// extractAcmeProviders extracts all acme sections using client-native's ParseAcmeProvider.
// ACME providers define Let's Encrypt/ACME certificate automation configuration (v3.2+ feature).
func (p *Parser) extractAcmeProviders() ([]*models.AcmeProvider, error) {
	sections, err := p.parser.SectionsGet(parser.Acme)
	if err != nil {
		return nil, err
	}

	acmeProviders := make([]*models.AcmeProvider, 0, len(sections))
	for _, sectionName := range sections {
		// ParseAcmeProvider handles all acme fields automatically
		acmeProvider, err := configuration.ParseAcmeProvider(p.parser, sectionName)
		if err != nil {
			slog.Warn("Failed to parse acme section", "section", sectionName, "error", err)
			continue
		}

		acmeProviders = append(acmeProviders, acmeProvider)
	}

	return acmeProviders, nil
}
