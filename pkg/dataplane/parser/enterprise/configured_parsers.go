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

// singletonSlot binds a singleton section to its cached field and its
// constructor. slot returns the address of the cache field so the lazy
// assignment writes back into the struct.
type singletonSlot struct {
	slot   func(*ConfiguredParsers) **parser.Parsers
	create func(*DefaultFactory) *parser.Parsers
}

// singletonFactories maps each singleton section to its cache slot and
// constructor. Replaces the per-section switch in getSingletonParsers.
var singletonFactories = map[Section]singletonSlot{
	SectionGlobal: {
		slot:   func(c *ConfiguredParsers) **parser.Parsers { return &c.Global },
		create: (*DefaultFactory).CreateGlobalParsers,
	},
	SectionDefaults: {
		slot:   func(c *ConfiguredParsers) **parser.Parsers { return &c.Defaults },
		create: (*DefaultFactory).CreateDefaultsParsers,
	},
	SectionComments: {
		slot:   func(c *ConfiguredParsers) **parser.Parsers { return &c.Comments },
		create: (*DefaultFactory).CreateCommentsParsers,
	},
	SectionWAFGlobal: {
		slot:   func(c *ConfiguredParsers) **parser.Parsers { return &c.WAFGlobal },
		create: (*DefaultFactory).CreateWAFGlobalParsers,
	},
}

// namedSlot binds a named section to its per-name map and its constructor.
type namedSlot struct {
	sectionMap func(*ConfiguredParsers) map[string]*parser.Parsers
	create     func(*DefaultFactory) *parser.Parsers
}

// namedFactories maps each named section (CE and EE) to its per-name cache
// map and constructor. Replaces the per-section switches in
// getCENamedSectionFactory / getEENamedSectionFactory.
var namedFactories = map[Section]namedSlot{
	// CE named sections
	SectionFrontend:   {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.Frontend }, (*DefaultFactory).CreateFrontendParsers},
	SectionBackend:    {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.Backend }, (*DefaultFactory).CreateBackendParsers},
	SectionListen:     {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.Listen }, (*DefaultFactory).CreateListenParsers},
	SectionResolvers:  {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.Resolvers }, (*DefaultFactory).CreateResolversParsers},
	SectionPeers:      {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.Peers }, (*DefaultFactory).CreatePeersParsers},
	SectionMailers:    {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.Mailers }, (*DefaultFactory).CreateMailersParsers},
	SectionCache:      {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.Cache }, (*DefaultFactory).CreateCacheParsers},
	SectionHTTPErrors: {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.HTTPErrors }, (*DefaultFactory).CreateHTTPErrorsParsers},
	SectionRing:       {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.Ring }, (*DefaultFactory).CreateRingParsers},
	SectionLogForward: {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.LogForward }, (*DefaultFactory).CreateLogForwardParsers},
	SectionFCGIApp:    {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.FCGIApp }, (*DefaultFactory).CreateFCGIAppParsers},
	SectionCrtStore:   {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.CrtStore }, (*DefaultFactory).CreateCrtStoreParsers},
	SectionTraces:     {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.Traces }, (*DefaultFactory).CreateTracesParsers},
	SectionLogProfile: {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.LogProfile }, (*DefaultFactory).CreateLogProfileParsers},
	SectionACME:       {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.ACME }, (*DefaultFactory).CreateACMEParsers},
	SectionUserlist:   {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.Userlist }, (*DefaultFactory).CreateUserlistParsers},

	// EE named sections
	SectionWAFProfile:     {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.WAFProfile }, (*DefaultFactory).CreateWAFProfileParsers},
	SectionBotMgmtProfile: {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.BotMgmtProfile }, (*DefaultFactory).CreateBotMgmtProfileParsers},
	SectionCaptcha:        {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.Captcha }, (*DefaultFactory).CreateCaptchaParsers},
	SectionUDPLB:          {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.UDPLB }, (*DefaultFactory).CreateUDPLBParsers},
	SectionDynamicUpdate:  {func(c *ConfiguredParsers) map[string]*parser.Parsers { return c.DynamicUpdate }, (*DefaultFactory).CreateDynamicUpdateParsers},
}

// getSingletonParsers returns parsers for singleton sections (global, defaults, etc.).
func (c *ConfiguredParsers) getSingletonParsers(section Section, factory *DefaultFactory) *parser.Parsers {
	s, ok := singletonFactories[section]
	if !ok {
		return nil
	}
	slot := s.slot(c)
	if *slot == nil {
		*slot = s.create(factory)
	}
	return *slot
}

// GetSectionParsers returns the parser collection for a section.
// For named sections, creates a new collection if it doesn't exist.
func (c *ConfiguredParsers) GetSectionParsers(section Section, name string, factory *DefaultFactory) *parser.Parsers {
	// Try singleton sections first
	if IsSingletonSection(section) || section == SectionComments {
		return c.getSingletonParsers(section, factory)
	}

	// Handle named sections
	s, ok := namedFactories[section]
	if !ok {
		return nil
	}
	return c.getOrCreate(s.sectionMap(c), name, func() *parser.Parsers { return s.create(factory) })
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
