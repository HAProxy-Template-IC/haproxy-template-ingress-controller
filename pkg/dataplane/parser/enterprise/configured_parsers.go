package enterprise

import (
	parser "github.com/haproxytech/client-native/v6/config-parser"
)

// ConfiguredParsers holds parser collections for all HAProxy sections.
// This extends the concept from client-native to include EE sections.
//
// The struct mirrors client-native's internal ConfiguredParsers but adds
// fields for Enterprise Edition sections that client-native doesn't support.
type ConfiguredParsers struct {
	// State is the current section being parsed.
	State Section

	// Active is the parser collection for the current section.
	Active *parser.Parsers

	// SectionName is the name of the current named section.
	SectionName string

	// --- Community Edition Sections (handled by client-native) ---

	// Comments holds comment line parsers.
	Comments *parser.Parsers

	// Global holds parsers for the global section.
	// Extended with EE global parsers (maxmind-load, etc.) via wrapper.
	Global *parser.Parsers

	// Defaults holds parsers for the defaults section.
	Defaults *parser.Parsers

	// Frontend holds per-frontend parser collections.
	// Key is frontend name. Extended with EE parsers (filter waf, http-request waf-evaluate).
	Frontend map[string]*parser.Parsers

	// Backend holds per-backend parser collections.
	// Key is backend name.
	Backend map[string]*parser.Parsers

	// Listen holds per-listen parser collections.
	// Key is listen name.
	Listen map[string]*parser.Parsers

	// Resolvers holds per-resolvers parser collections.
	// Key is resolvers name.
	Resolvers map[string]*parser.Parsers

	// Peers holds per-peers parser collections.
	// Key is peers name.
	Peers map[string]*parser.Parsers

	// Mailers holds per-mailers parser collections.
	// Key is mailers name.
	Mailers map[string]*parser.Parsers

	// Cache holds per-cache parser collections.
	// Key is cache name.
	Cache map[string]*parser.Parsers

	// Program holds per-program parser collections.
	// Key is program name.
	Program map[string]*parser.Parsers

	// HTTPErrors holds per-http-errors parser collections.
	// Key is http-errors name.
	HTTPErrors map[string]*parser.Parsers

	// Ring holds per-ring parser collections.
	// Key is ring name.
	Ring map[string]*parser.Parsers

	// LogForward holds per-log-forward parser collections.
	// Key is log-forward name.
	LogForward map[string]*parser.Parsers

	// FCGIApp holds per-fcgi-app parser collections.
	// Key is fcgi-app name.
	FCGIApp map[string]*parser.Parsers

	// CrtStore holds per-crt-store parser collections.
	// Key is crt-store name.
	CrtStore map[string]*parser.Parsers

	// Traces holds per-traces parser collections.
	// Key is traces name.
	Traces map[string]*parser.Parsers

	// LogProfile holds per-log-profile parser collections.
	// Key is log-profile name.
	LogProfile map[string]*parser.Parsers

	// ACME holds per-acme parser collections.
	// Key is acme name.
	ACME map[string]*parser.Parsers

	// Userlist holds per-userlist parser collections.
	// Key is userlist name.
	Userlist map[string]*parser.Parsers

	// --- Enterprise Edition Sections ---

	// WAFGlobal holds parsers for the waf-global section (singleton).
	WAFGlobal *parser.Parsers

	// WAFProfile holds per-waf-profile parser collections.
	// Key is profile name.
	WAFProfile map[string]*parser.Parsers

	// BotMgmtProfile holds per-botmgmt-profile parser collections.
	// Key is profile name.
	BotMgmtProfile map[string]*parser.Parsers

	// Captcha holds per-captcha parser collections.
	// Key is captcha name.
	Captcha map[string]*parser.Parsers

	// UDPLB holds per-udp-lb parser collections.
	// Key is udp-lb name.
	UDPLB map[string]*parser.Parsers

	// DynamicUpdate holds per-dynamic-update parser collections.
	// Key is dynamic-update name.
	DynamicUpdate map[string]*parser.Parsers
}

// NewConfiguredParsers creates a new ConfiguredParsers with initialized maps.
func NewConfiguredParsers() *ConfiguredParsers {
	return &ConfiguredParsers{
		// CE named sections
		Frontend:   make(map[string]*parser.Parsers),
		Backend:    make(map[string]*parser.Parsers),
		Listen:     make(map[string]*parser.Parsers),
		Resolvers:  make(map[string]*parser.Parsers),
		Peers:      make(map[string]*parser.Parsers),
		Mailers:    make(map[string]*parser.Parsers),
		Cache:      make(map[string]*parser.Parsers),
		Program:    make(map[string]*parser.Parsers),
		HTTPErrors: make(map[string]*parser.Parsers),
		Ring:       make(map[string]*parser.Parsers),
		LogForward: make(map[string]*parser.Parsers),
		FCGIApp:    make(map[string]*parser.Parsers),
		CrtStore:   make(map[string]*parser.Parsers),
		Traces:     make(map[string]*parser.Parsers),
		LogProfile: make(map[string]*parser.Parsers),
		ACME:       make(map[string]*parser.Parsers),
		Userlist:   make(map[string]*parser.Parsers),

		// EE named sections
		WAFProfile:     make(map[string]*parser.Parsers),
		BotMgmtProfile: make(map[string]*parser.Parsers),
		Captcha:        make(map[string]*parser.Parsers),
		UDPLB:          make(map[string]*parser.Parsers),
		DynamicUpdate:  make(map[string]*parser.Parsers),
	}
}

// getSingletonParsers returns parsers for singleton sections (global, defaults, etc.).
func (c *ConfiguredParsers) getSingletonParsers(section Section, factory ParserFactory) *parser.Parsers {
	switch section {
	case SectionGlobal:
		if c.Global == nil {
			c.Global = factory.CreateGlobalParsers()
		}
		return c.Global
	case SectionDefaults:
		if c.Defaults == nil {
			c.Defaults = factory.CreateDefaultsParsers()
		}
		return c.Defaults
	case SectionComments:
		if c.Comments == nil {
			c.Comments = factory.CreateCommentsParsers()
		}
		return c.Comments
	case SectionWAFGlobal:
		if c.WAFGlobal == nil {
			c.WAFGlobal = factory.CreateWAFGlobalParsers()
		}
		return c.WAFGlobal
	default:
		return nil
	}
}

// getCENamedSectionFactory returns the factory function and map for a CE named section.
func (c *ConfiguredParsers) getCENamedSectionFactory(section Section, factory ParserFactory) (sectionMap map[string]*parser.Parsers, createFunc func() *parser.Parsers) {
	switch section {
	case SectionFrontend:
		return c.Frontend, factory.CreateFrontendParsers
	case SectionBackend:
		return c.Backend, factory.CreateBackendParsers
	case SectionListen:
		return c.Listen, factory.CreateListenParsers
	case SectionResolvers:
		return c.Resolvers, factory.CreateResolversParsers
	case SectionPeers:
		return c.Peers, factory.CreatePeersParsers
	case SectionMailers:
		return c.Mailers, factory.CreateMailersParsers
	case SectionCache:
		return c.Cache, factory.CreateCacheParsers
	case SectionProgram:
		return c.Program, factory.CreateProgramParsers
	case SectionHTTPErrors:
		return c.HTTPErrors, factory.CreateHTTPErrorsParsers
	case SectionRing:
		return c.Ring, factory.CreateRingParsers
	case SectionLogForward:
		return c.LogForward, factory.CreateLogForwardParsers
	case SectionFCGIApp:
		return c.FCGIApp, factory.CreateFCGIAppParsers
	case SectionCrtStore:
		return c.CrtStore, factory.CreateCrtStoreParsers
	case SectionTraces:
		return c.Traces, factory.CreateTracesParsers
	case SectionLogProfile:
		return c.LogProfile, factory.CreateLogProfileParsers
	case SectionACME:
		return c.ACME, factory.CreateACMEParsers
	case SectionUserlist:
		return c.Userlist, factory.CreateUserlistParsers
	default:
		return nil, nil
	}
}

// getEENamedSectionFactory returns the factory function and map for an EE named section.
func (c *ConfiguredParsers) getEENamedSectionFactory(section Section, factory ParserFactory) (sectionMap map[string]*parser.Parsers, createFunc func() *parser.Parsers) {
	switch section {
	case SectionWAFProfile:
		return c.WAFProfile, factory.CreateWAFProfileParsers
	case SectionBotMgmtProfile:
		return c.BotMgmtProfile, factory.CreateBotMgmtProfileParsers
	case SectionCaptcha:
		return c.Captcha, factory.CreateCaptchaParsers
	case SectionUDPLB:
		return c.UDPLB, factory.CreateUDPLBParsers
	case SectionDynamicUpdate:
		return c.DynamicUpdate, factory.CreateDynamicUpdateParsers
	default:
		return nil, nil
	}
}

// getNamedSectionFactory returns the factory function and map for a named section.
func (c *ConfiguredParsers) getNamedSectionFactory(section Section, factory ParserFactory) (sectionMap map[string]*parser.Parsers, createFunc func() *parser.Parsers) {
	// Try CE sections first (more common)
	if m, f := c.getCENamedSectionFactory(section, factory); m != nil {
		return m, f
	}
	// Try EE sections
	return c.getEENamedSectionFactory(section, factory)
}

// GetSectionParsers returns the parser collection for a section.
// For named sections, creates a new collection if it doesn't exist.
func (c *ConfiguredParsers) GetSectionParsers(section Section, name string, factory ParserFactory) *parser.Parsers {
	// Try singleton sections first
	if IsSingletonSection(section) || section == SectionComments {
		return c.getSingletonParsers(section, factory)
	}

	// Handle named sections
	m, createFunc := c.getNamedSectionFactory(section, factory)
	if m == nil || createFunc == nil {
		return nil
	}
	return c.getOrCreate(m, name, createFunc)
}

// getOrCreate returns an existing parser collection or creates a new one.
func (c *ConfiguredParsers) getOrCreate(m map[string]*parser.Parsers, name string, create func() *parser.Parsers) *parser.Parsers {
	if p, ok := m[name]; ok {
		return p
	}
	p := create()
	m[name] = p
	return p
}

// SetState sets the current parsing state and active parser collection.
func (c *ConfiguredParsers) SetState(section Section, name string, parsers *parser.Parsers) {
	c.State = section
	c.SectionName = name
	c.Active = parsers
}

// getCESectionMap returns the map for a CE named section type.
func (c *ConfiguredParsers) getCESectionMap(section Section) map[string]*parser.Parsers {
	switch section {
	case SectionFrontend:
		return c.Frontend
	case SectionBackend:
		return c.Backend
	case SectionListen:
		return c.Listen
	case SectionResolvers:
		return c.Resolvers
	case SectionPeers:
		return c.Peers
	case SectionMailers:
		return c.Mailers
	case SectionCache:
		return c.Cache
	case SectionProgram:
		return c.Program
	case SectionHTTPErrors:
		return c.HTTPErrors
	case SectionRing:
		return c.Ring
	case SectionLogForward:
		return c.LogForward
	case SectionFCGIApp:
		return c.FCGIApp
	case SectionCrtStore:
		return c.CrtStore
	case SectionTraces:
		return c.Traces
	case SectionLogProfile:
		return c.LogProfile
	case SectionACME:
		return c.ACME
	case SectionUserlist:
		return c.Userlist
	default:
		return nil
	}
}

// getEESectionMap returns the map for an EE named section type.
func (c *ConfiguredParsers) getEESectionMap(section Section) map[string]*parser.Parsers {
	switch section {
	case SectionWAFProfile:
		return c.WAFProfile
	case SectionBotMgmtProfile:
		return c.BotMgmtProfile
	case SectionCaptcha:
		return c.Captcha
	case SectionUDPLB:
		return c.UDPLB
	case SectionDynamicUpdate:
		return c.DynamicUpdate
	default:
		return nil
	}
}

// getSectionMap returns the map for a named section type.
func (c *ConfiguredParsers) getSectionMap(section Section) map[string]*parser.Parsers {
	// Try CE sections first (more common)
	if m := c.getCESectionMap(section); m != nil {
		return m
	}
	// Try EE sections
	return c.getEESectionMap(section)
}

// GetAllNames returns all section names for a given section type.
func (c *ConfiguredParsers) GetAllNames(section Section) []string {
	m := c.getSectionMap(section)
	if m == nil {
		return nil
	}

	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	return names
}

// ParserFactory is an interface for creating parser collections.
// This allows dependency injection of the actual parser creation logic.
type ParserFactory interface {
	// CE singleton sections
	CreateGlobalParsers() *parser.Parsers
	CreateDefaultsParsers() *parser.Parsers
	CreateCommentsParsers() *parser.Parsers

	// CE named sections
	CreateFrontendParsers() *parser.Parsers
	CreateBackendParsers() *parser.Parsers
	CreateListenParsers() *parser.Parsers
	CreateResolversParsers() *parser.Parsers
	CreatePeersParsers() *parser.Parsers
	CreateMailersParsers() *parser.Parsers
	CreateCacheParsers() *parser.Parsers
	CreateProgramParsers() *parser.Parsers
	CreateHTTPErrorsParsers() *parser.Parsers
	CreateRingParsers() *parser.Parsers
	CreateLogForwardParsers() *parser.Parsers
	CreateFCGIAppParsers() *parser.Parsers
	CreateCrtStoreParsers() *parser.Parsers
	CreateTracesParsers() *parser.Parsers
	CreateLogProfileParsers() *parser.Parsers
	CreateACMEParsers() *parser.Parsers
	CreateUserlistParsers() *parser.Parsers

	// EE singleton sections
	CreateWAFGlobalParsers() *parser.Parsers

	// EE named sections
	CreateWAFProfileParsers() *parser.Parsers
	CreateBotMgmtProfileParsers() *parser.Parsers
	CreateCaptchaParsers() *parser.Parsers
	CreateUDPLBParsers() *parser.Parsers
	CreateDynamicUpdateParsers() *parser.Parsers
}
