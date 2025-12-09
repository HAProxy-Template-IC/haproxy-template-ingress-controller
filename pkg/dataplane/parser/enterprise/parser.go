// Package enterprise provides HAProxy Enterprise Edition configuration parsing.
//
// This package implements a hybrid parser for HAProxy EE configurations that:
// 1. Uses client-native's parser for complete CE section extraction (global, defaults, frontends, backends, etc.)
// 2. Uses a custom line-by-line reader for EE-specific sections (waf-global, waf-profile, captcha, etc.)
// 3. Uses wrapper parsers to intercept EE directives in CE sections (filter waf, http-request waf-evaluate)
//
// This hybrid approach maximizes code reuse from client-native while adding EE support.
//
// Usage:
//
//	// Choose parser based on HAProxy version
//	if isEnterpriseEdition(haproxyVersion) {
//	    parser := enterprise.NewParser()
//	    config, err := parser.ParseFromString(configString)
//	} else {
//	    parser, _ := parser.New()
//	    config, err := parser.ParseFromString(configString)
//	}
package enterprise

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"

	clientparser "github.com/haproxytech/client-native/v6/config-parser"
	"github.com/haproxytech/client-native/v6/configuration"
	"github.com/haproxytech/client-native/v6/models"

	"haproxy-template-ic/pkg/dataplane/parser/enterprise/parsers"
	"haproxy-template-ic/pkg/dataplane/parser/parserconfig"
	v32ee "haproxy-template-ic/pkg/generated/dataplaneapi/v32ee"
)

// parserMutex protects against concurrent parsing.
// The enterprise parser shares some client-native parsers which have global state.
var parserMutex sync.Mutex

// Parser is the HAProxy Enterprise Edition configuration parser.
// It uses a hybrid approach:
// 1. client-native parser for complete CE section extraction (global, defaults, frontends, backends, etc.)
// 2. Custom EE reader for EE-specific sections (waf-global, waf-profile, captcha, etc.)
// 3. Custom EE reader for EE directives within CE sections (filter waf, http-request waf-evaluate, etc.)
type Parser struct {
	factory  ParserFactory
	ceParser clientparser.Parser
}

// NewParser creates a new Enterprise Edition parser.
// Returns an error if the underlying client-native parser cannot be initialized.
func NewParser() (*Parser, error) {
	ceParser, err := clientparser.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create client-native parser: %w", err)
	}

	return &Parser{
		factory:  NewDefaultFactory(),
		ceParser: ceParser,
	}, nil
}

// StructuredConfig is the canonical configuration type.
// This is a type alias to parserconfig.StructuredConfig for convenience.
type StructuredConfig = parserconfig.StructuredConfig

// EEFrontendData is a type alias for parserconfig.EEFrontendData.
type EEFrontendData = parserconfig.EEFrontendData

// EEBackendData is a type alias for parserconfig.EEBackendData.
type EEBackendData = parserconfig.EEBackendData

// ParseFromString parses an HAProxy EE configuration string into a structured representation.
//
// This uses a hybrid approach:
// 1. client-native parser processes CE sections (complete field extraction)
// 2. EE reader processes EE-specific sections (waf-global, waf-profile, etc.)
// 3. EE reader captures EE directives within CE sections (filter waf, http-request waf-evaluate).
func (p *Parser) ParseFromString(config string) (*StructuredConfig, error) {
	if config == "" {
		return nil, fmt.Errorf("configuration string is empty")
	}

	parserMutex.Lock()
	defer parserMutex.Unlock()

	// Step 1: Parse with client-native parser for complete CE section extraction
	// This handles all CE directives with full field extraction
	if err := p.ceParser.Process(strings.NewReader(config)); err != nil {
		// Log but continue - EE configs may contain directives client-native doesn't understand
		slog.Debug("client-native parser warning (continuing with EE parser)",
			"error", err)
	}

	// Step 2: Parse with EE reader for EE-specific sections and directives
	reader := NewReader(p.factory)
	if err := reader.ProcessString(config); err != nil {
		return nil, fmt.Errorf("failed to parse EE configuration: %w", err)
	}

	// Step 3: Extract structured configuration from both parsers
	return p.extractConfiguration(reader.GetParsers())
}

// extractConfiguration builds a StructuredConfig from parsed data.
// It combines CE sections from client-native parser with EE sections from EE reader.
func (p *Parser) extractConfiguration(parsed *ConfiguredParsers) (*StructuredConfig, error) {
	conf := &StructuredConfig{
		EEFrontends: make(map[string]*parserconfig.EEFrontendData),
		EEBackends:  make(map[string]*parserconfig.EEBackendData),
	}

	// Extract CE sections from client-native parser (complete field extraction)
	if err := p.extractCESections(conf); err != nil {
		return nil, fmt.Errorf("failed to extract CE sections: %w", err)
	}

	// Extract EE standalone sections from EE reader
	p.extractEEStandaloneSections(parsed, conf)

	// Extract EE directives from CE sections (filter waf, http-request waf-evaluate, etc.)
	// These are captured by wrapper parsers in the EE reader
	p.extractEEDirectivesFromCESections(parsed, conf)

	return conf, nil
}

// extractCESections extracts standard CE sections from the client-native parser.
// This uses client-native's configuration.Parse* functions for complete field extraction,
// ensuring feature parity with the CE parser.
func (p *Parser) extractCESections(conf *StructuredConfig) error {
	// Extract global
	global, err := p.extractGlobal()
	if err != nil {
		return fmt.Errorf("failed to extract global section: %w", err)
	}
	conf.Global = global

	// Extract defaults
	defaults, err := p.extractDefaults()
	if err != nil {
		return fmt.Errorf("failed to extract defaults sections: %w", err)
	}
	conf.Defaults = defaults

	// Extract frontends
	frontends, err := p.extractFrontends()
	if err != nil {
		return fmt.Errorf("failed to extract frontends: %w", err)
	}
	conf.Frontends = frontends

	// Extract backends
	backends, err := p.extractBackends()
	if err != nil {
		return fmt.Errorf("failed to extract backends: %w", err)
	}
	conf.Backends = backends

	// Extract peer and service discovery sections
	if err := p.extractPeerAndDiscoverySections(conf); err != nil {
		return err
	}

	// Extract service sections
	if err := p.extractServiceSections(conf); err != nil {
		return err
	}

	// Extract program sections
	if err := p.extractProgramSections(conf); err != nil {
		return err
	}

	// Extract observability sections (v3.1+ features)
	if err := p.extractObservabilitySections(conf); err != nil {
		return err
	}

	// Extract certificate sections (v3.2+ features)
	if err := p.extractCertificateSections(conf); err != nil {
		return err
	}

	return nil
}

// extractGlobal extracts the global section using client-native's ParseGlobalSection.
// This handles ALL global fields (100+ fields) automatically.
func (p *Parser) extractGlobal() (*models.Global, error) {
	global, err := configuration.ParseGlobalSection(p.ceParser)
	if err != nil {
		return nil, fmt.Errorf("failed to parse global section: %w", err)
	}

	// Parse log targets separately (nested structure)
	logTargets, err := configuration.ParseLogTargets(string(clientparser.Global), "", p.ceParser)
	if err == nil {
		global.LogTargetList = logTargets
	}

	return global, nil
}

// extractDefaults extracts all defaults sections using client-native's ParseSection.
// This handles ALL defaults fields (60+ fields) automatically.
func (p *Parser) extractDefaults() ([]*models.Defaults, error) {
	sections, err := p.ceParser.SectionsGet(clientparser.Defaults)
	if err != nil {
		return nil, err
	}

	defaults := make([]*models.Defaults, 0, len(sections))
	for _, sectionName := range sections {
		def := &models.Defaults{}

		if err := configuration.ParseSection(&def.DefaultsBase, clientparser.Defaults, sectionName, p.ceParser); err != nil {
			slog.Warn("Failed to parse defaults section", "section", sectionName, "error", err)
			continue
		}
		def.Name = sectionName

		// Parse log targets
		logTargets, err := configuration.ParseLogTargets(string(clientparser.Defaults), sectionName, p.ceParser)
		if err == nil {
			def.LogTargetList = logTargets
		}

		// Parse QUIC initial rules (v3.2+ feature)
		def.QUICInitialRuleList, _ = configuration.ParseQUICInitialRules(string(clientparser.Defaults), sectionName, p.ceParser)

		defaults = append(defaults, def)
	}

	return defaults, nil
}

// extractFrontends extracts all frontend sections using client-native's Parse* functions.
// This handles ALL frontend fields (80+ fields) and nested structures.
func (p *Parser) extractFrontends() ([]*models.Frontend, error) {
	sections, err := p.ceParser.SectionsGet(clientparser.Frontends)
	if err != nil {
		return nil, err
	}

	frontends := make([]*models.Frontend, 0, len(sections))
	for _, sectionName := range sections {
		fe := &models.Frontend{}

		if err := configuration.ParseSection(&fe.FrontendBase, clientparser.Frontends, sectionName, p.ceParser); err != nil {
			slog.Warn("Failed to parse frontend section", "section", sectionName, "error", err)
			continue
		}
		fe.Name = sectionName

		// Parse nested structures
		fe.ACLList, _ = configuration.ParseACLs(clientparser.Frontends, sectionName, p.ceParser)

		binds, _ := configuration.ParseBinds(string(clientparser.Frontends), sectionName, p.ceParser)
		if binds != nil {
			fe.Binds = make(map[string]models.Bind)
			for _, bind := range binds {
				if bind != nil {
					fe.Binds[bind.Name] = *bind
				}
			}
		}

		fe.HTTPRequestRuleList, _ = configuration.ParseHTTPRequestRules(string(clientparser.Frontends), sectionName, p.ceParser)
		fe.HTTPResponseRuleList, _ = configuration.ParseHTTPResponseRules(string(clientparser.Frontends), sectionName, p.ceParser)
		fe.TCPRequestRuleList, _ = configuration.ParseTCPRequestRules(string(clientparser.Frontends), sectionName, p.ceParser)
		fe.HTTPAfterResponseRuleList, _ = configuration.ParseHTTPAfterRules(string(clientparser.Frontends), sectionName, p.ceParser)
		fe.HTTPErrorRuleList, _ = configuration.ParseHTTPErrorRules(string(clientparser.Frontends), sectionName, p.ceParser)
		fe.FilterList, _ = configuration.ParseFilters(string(clientparser.Frontends), sectionName, p.ceParser)
		fe.LogTargetList, _ = configuration.ParseLogTargets(string(clientparser.Frontends), sectionName, p.ceParser)
		fe.BackendSwitchingRuleList, _ = configuration.ParseBackendSwitchingRules(sectionName, p.ceParser)
		fe.CaptureList, _ = configuration.ParseDeclareCaptures(sectionName, p.ceParser)
		fe.QUICInitialRuleList, _ = configuration.ParseQUICInitialRules(string(clientparser.Frontends), sectionName, p.ceParser)

		frontends = append(frontends, fe)
	}

	return frontends, nil
}

// extractBackends extracts all backend sections using client-native's Parse* functions.
// This handles ALL backend fields (100+ fields) and nested structures.
func (p *Parser) extractBackends() ([]*models.Backend, error) {
	sections, err := p.ceParser.SectionsGet(clientparser.Backends)
	if err != nil {
		return nil, err
	}

	backends := make([]*models.Backend, 0, len(sections))
	for _, sectionName := range sections {
		be := &models.Backend{}

		if err := configuration.ParseSection(&be.BackendBase, clientparser.Backends, sectionName, p.ceParser); err != nil {
			slog.Warn("Failed to parse backend section", "section", sectionName, "error", err)
			continue
		}
		be.Name = sectionName

		// Parse nested structures
		p.parseBackendNestedStructures(sectionName, be)

		backends = append(backends, be)
	}

	return backends, nil
}

// parseBackendNestedStructures parses all nested structures for a backend.
func (p *Parser) parseBackendNestedStructures(sectionName string, be *models.Backend) {
	// Parse ACLs and servers
	be.ACLList, _ = configuration.ParseACLs(clientparser.Backends, sectionName, p.ceParser)

	servers, _ := configuration.ParseServers(string(clientparser.Backends), sectionName, p.ceParser)
	if servers != nil {
		be.Servers = make(map[string]models.Server)
		for _, server := range servers {
			if server != nil {
				be.Servers[server.Name] = *server
			}
		}
	}

	// Parse HTTP/TCP rules
	be.HTTPRequestRuleList, _ = configuration.ParseHTTPRequestRules(string(clientparser.Backends), sectionName, p.ceParser)
	be.HTTPResponseRuleList, _ = configuration.ParseHTTPResponseRules(string(clientparser.Backends), sectionName, p.ceParser)
	be.TCPRequestRuleList, _ = configuration.ParseTCPRequestRules(string(clientparser.Backends), sectionName, p.ceParser)
	be.TCPResponseRuleList, _ = configuration.ParseTCPResponseRules(string(clientparser.Backends), sectionName, p.ceParser)
	be.HTTPAfterResponseRuleList, _ = configuration.ParseHTTPAfterRules(string(clientparser.Backends), sectionName, p.ceParser)
	be.HTTPErrorRuleList, _ = configuration.ParseHTTPErrorRules(string(clientparser.Backends), sectionName, p.ceParser)
	be.ServerSwitchingRuleList, _ = configuration.ParseServerSwitchingRules(sectionName, p.ceParser)
	be.StickRuleList, _ = configuration.ParseStickRules(sectionName, p.ceParser)

	// Parse filters, log targets, and checks
	be.FilterList, _ = configuration.ParseFilters(string(clientparser.Backends), sectionName, p.ceParser)
	be.LogTargetList, _ = configuration.ParseLogTargets(string(clientparser.Backends), sectionName, p.ceParser)
	be.HTTPCheckList, _ = configuration.ParseHTTPChecks(string(clientparser.Backends), sectionName, p.ceParser)
	be.TCPCheckRuleList, _ = configuration.ParseTCPChecks(string(clientparser.Backends), sectionName, p.ceParser)

	// Parse server templates
	serverTemplates, _ := configuration.ParseServerTemplates(sectionName, p.ceParser)
	if serverTemplates != nil {
		be.ServerTemplates = make(map[string]models.ServerTemplate)
		for _, template := range serverTemplates {
			if template != nil {
				be.ServerTemplates[template.Prefix] = *template
			}
		}
	}
}

// extractPeerAndDiscoverySections extracts peer and service discovery sections.
func (p *Parser) extractPeerAndDiscoverySections(conf *StructuredConfig) error {
	peers, err := p.extractPeers()
	if err != nil {
		return fmt.Errorf("failed to extract peers: %w", err)
	}
	conf.Peers = peers

	resolvers, err := p.extractResolvers()
	if err != nil {
		return fmt.Errorf("failed to extract resolvers: %w", err)
	}
	conf.Resolvers = resolvers

	mailers, err := p.extractMailers()
	if err != nil {
		return fmt.Errorf("failed to extract mailers: %w", err)
	}
	conf.Mailers = mailers

	return nil
}

// extractPeers extracts all peers sections.
func (p *Parser) extractPeers() ([]*models.PeerSection, error) {
	sections, err := p.ceParser.SectionsGet(clientparser.Peers)
	if err != nil {
		return nil, err
	}

	peers := make([]*models.PeerSection, 0, len(sections))
	for _, sectionName := range sections {
		peer := &models.PeerSection{}

		if err := configuration.ParseSection(peer, clientparser.Peers, sectionName, p.ceParser); err != nil {
			slog.Warn("Failed to parse peers section", "section", sectionName, "error", err)
			continue
		}
		peer.Name = sectionName

		peerEntries, _ := configuration.ParsePeerEntries(sectionName, p.ceParser)
		if peerEntries != nil {
			peer.PeerEntries = make(map[string]models.PeerEntry)
			for _, entry := range peerEntries {
				if entry != nil {
					peer.PeerEntries[entry.Name] = *entry
				}
			}
		}

		peers = append(peers, peer)
	}

	return peers, nil
}

// extractResolvers extracts all resolvers sections.
func (p *Parser) extractResolvers() ([]*models.Resolver, error) {
	sections, err := p.ceParser.SectionsGet(clientparser.Resolvers)
	if err != nil {
		return nil, err
	}

	resolvers := make([]*models.Resolver, 0, len(sections))
	for _, sectionName := range sections {
		resolver := &models.Resolver{}
		resolver.Name = sectionName

		if err := configuration.ParseResolverSection(p.ceParser, resolver); err != nil {
			slog.Warn("Failed to parse resolvers section", "section", sectionName, "error", err)
			continue
		}

		nameservers, _ := configuration.ParseNameservers(sectionName, p.ceParser)
		if nameservers != nil {
			resolver.Nameservers = make(map[string]models.Nameserver)
			for _, ns := range nameservers {
				if ns != nil {
					resolver.Nameservers[ns.Name] = *ns
				}
			}
		}

		resolvers = append(resolvers, resolver)
	}

	return resolvers, nil
}

// extractMailers extracts all mailers sections.
func (p *Parser) extractMailers() ([]*models.MailersSection, error) {
	sections, err := p.ceParser.SectionsGet(clientparser.Mailers)
	if err != nil {
		return nil, err
	}

	mailers := make([]*models.MailersSection, 0, len(sections))
	for _, sectionName := range sections {
		mailer := &models.MailersSection{}
		mailer.Name = sectionName

		if err := configuration.ParseMailersSection(p.ceParser, mailer); err != nil {
			slog.Warn("Failed to parse mailers section", "section", sectionName, "error", err)
			continue
		}

		mailerEntries, _ := configuration.ParseMailerEntries(sectionName, p.ceParser)
		if mailerEntries != nil {
			mailer.MailerEntries = make(map[string]models.MailerEntry)
			for _, entry := range mailerEntries {
				if entry != nil {
					mailer.MailerEntries[entry.Name] = *entry
				}
			}
		}

		mailers = append(mailers, mailer)
	}

	return mailers, nil
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

	userlists, err := p.extractUserlists()
	if err != nil {
		return fmt.Errorf("failed to extract userlists: %w", err)
	}
	conf.Userlists = userlists

	return nil
}

// extractCaches extracts all cache sections.
func (p *Parser) extractCaches() ([]*models.Cache, error) {
	sections, err := p.ceParser.SectionsGet(clientparser.Cache)
	if err != nil {
		return nil, err
	}

	caches := make([]*models.Cache, 0, len(sections))
	for _, sectionName := range sections {
		cache := &models.Cache{}
		name := sectionName
		cache.Name = &name

		if err := configuration.ParseCacheSection(p.ceParser, cache); err != nil {
			slog.Warn("Failed to parse cache section", "section", sectionName, "error", err)
			continue
		}

		caches = append(caches, cache)
	}

	return caches, nil
}

// extractRings extracts all ring sections.
func (p *Parser) extractRings() ([]*models.Ring, error) {
	sections, err := p.ceParser.SectionsGet(clientparser.Ring)
	if err != nil {
		return nil, err
	}

	rings := make([]*models.Ring, 0, len(sections))
	for _, sectionName := range sections {
		ring := &models.Ring{}
		ring.Name = sectionName

		if err := configuration.ParseRingSection(p.ceParser, ring); err != nil {
			slog.Warn("Failed to parse ring section", "section", sectionName, "error", err)
			continue
		}

		rings = append(rings, ring)
	}

	return rings, nil
}

// extractHTTPErrors extracts all http-errors sections.
func (p *Parser) extractHTTPErrors() ([]*models.HTTPErrorsSection, error) {
	sections, err := p.ceParser.SectionsGet(clientparser.HTTPErrors)
	if err != nil {
		return nil, err
	}

	httpErrors := make([]*models.HTTPErrorsSection, 0, len(sections))
	for _, sectionName := range sections {
		httpError, err := configuration.ParseHTTPErrorsSection(p.ceParser, sectionName)
		if err != nil {
			continue
		}

		httpErrors = append(httpErrors, httpError)
	}

	return httpErrors, nil
}

// extractUserlists extracts all userlist sections.
func (p *Parser) extractUserlists() ([]*models.Userlist, error) {
	sections, err := p.ceParser.SectionsGet(clientparser.UserList)
	if err != nil {
		return nil, err
	}

	userlists := make([]*models.Userlist, 0, len(sections))
	for _, sectionName := range sections {
		userlist, err := p.extractSingleUserlist(sectionName)
		if err != nil {
			slog.Warn("Failed to parse userlist section", "section", sectionName, "error", err)
			continue
		}
		userlists = append(userlists, userlist)
	}

	return userlists, nil
}

// extractSingleUserlist extracts a single userlist section by name.
func (p *Parser) extractSingleUserlist(sectionName string) (*models.Userlist, error) {
	userlist := &models.Userlist{}
	userlist.Name = sectionName

	if err := configuration.ParseSection(&userlist.UserlistBase, clientparser.UserList, sectionName, p.ceParser); err != nil {
		return nil, err
	}

	p.populateUserlistUsers(userlist, sectionName)
	p.populateUserlistGroups(userlist, sectionName)

	return userlist, nil
}

// populateUserlistUsers parses and populates users in a userlist.
func (p *Parser) populateUserlistUsers(userlist *models.Userlist, sectionName string) {
	users, err := configuration.ParseUsers(sectionName, p.ceParser)
	if err != nil || users == nil {
		return
	}
	userlist.Users = make(map[string]models.User)
	for _, user := range users {
		if user != nil && user.Username != "" {
			userlist.Users[user.Username] = *user
		}
	}
}

// populateUserlistGroups parses and populates groups in a userlist.
func (p *Parser) populateUserlistGroups(userlist *models.Userlist, sectionName string) {
	groups, err := configuration.ParseGroups(sectionName, p.ceParser)
	if err != nil || groups == nil {
		return
	}
	userlist.Groups = make(map[string]models.Group)
	for _, group := range groups {
		if group != nil && group.Name != "" {
			userlist.Groups[group.Name] = *group
		}
	}
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

// extractPrograms extracts all program sections.
func (p *Parser) extractPrograms() ([]*models.Program, error) {
	sections, err := p.ceParser.SectionsGet(clientparser.Program)
	if err != nil {
		return nil, err
	}

	programs := make([]*models.Program, 0, len(sections))
	for _, sectionName := range sections {
		program, err := configuration.ParseProgram(p.ceParser, sectionName)
		if err != nil {
			slog.Warn("Failed to parse program section", "section", sectionName, "error", err)
			continue
		}

		programs = append(programs, program)
	}

	return programs, nil
}

// extractLogForwards extracts all log-forward sections.
func (p *Parser) extractLogForwards() ([]*models.LogForward, error) {
	sections, err := p.ceParser.SectionsGet(clientparser.LogForward)
	if err != nil {
		return nil, err
	}

	logForwards := make([]*models.LogForward, 0, len(sections))
	for _, sectionName := range sections {
		logForward := &models.LogForward{
			LogForwardBase: models.LogForwardBase{Name: sectionName},
		}
		if err := configuration.ParseLogForward(p.ceParser, logForward); err != nil {
			slog.Warn("Failed to parse log-forward section", "section", sectionName, "error", err)
			continue
		}

		logForwards = append(logForwards, logForward)
	}

	return logForwards, nil
}

// extractFCGIApps extracts all fcgi-app sections.
func (p *Parser) extractFCGIApps() ([]*models.FCGIApp, error) {
	sections, err := p.ceParser.SectionsGet(clientparser.FCGIApp)
	if err != nil {
		return nil, err
	}

	fcgiApps := make([]*models.FCGIApp, 0, len(sections))
	for _, sectionName := range sections {
		fcgiApp, err := configuration.ParseFCGIApp(p.ceParser, sectionName)
		if err != nil {
			slog.Warn("Failed to parse fcgi-app section", "section", sectionName, "error", err)
			continue
		}

		fcgiApps = append(fcgiApps, fcgiApp)
	}

	return fcgiApps, nil
}

// extractCrtStores extracts all crt-store sections.
func (p *Parser) extractCrtStores() ([]*models.CrtStore, error) {
	sections, err := p.ceParser.SectionsGet(clientparser.CrtStore)
	if err != nil {
		return nil, err
	}

	crtStores := make([]*models.CrtStore, 0, len(sections))
	for _, sectionName := range sections {
		crtStore, err := configuration.ParseCrtStore(p.ceParser, sectionName)
		if err != nil {
			slog.Warn("Failed to parse crt-store section", "section", sectionName, "error", err)
			continue
		}

		crtStores = append(crtStores, crtStore)
	}

	return crtStores, nil
}

// extractObservabilitySections extracts observability sections (log-profiles, traces).
func (p *Parser) extractObservabilitySections(conf *StructuredConfig) error {
	logProfiles, err := p.extractLogProfiles()
	if err != nil {
		return fmt.Errorf("failed to extract log-profiles: %w", err)
	}
	conf.LogProfiles = logProfiles

	conf.Traces = p.extractTraces()

	return nil
}

// extractLogProfiles extracts all log-profile sections.
func (p *Parser) extractLogProfiles() ([]*models.LogProfile, error) {
	sections, err := p.ceParser.SectionsGet(clientparser.LogProfile)
	if err != nil {
		return nil, err
	}

	logProfiles := make([]*models.LogProfile, 0, len(sections))
	for _, sectionName := range sections {
		logProfile, err := configuration.ParseLogProfile(p.ceParser, sectionName)
		if err != nil {
			slog.Warn("Failed to parse log-profile section", "section", sectionName, "error", err)
			continue
		}

		logProfiles = append(logProfiles, logProfile)
	}

	return logProfiles, nil
}

// extractTraces extracts the traces section (singleton).
func (p *Parser) extractTraces() *models.Traces {
	if !p.ceParser.SectionExists(clientparser.Traces, clientparser.TracesSectionName) {
		return nil
	}

	traces, err := configuration.ParseTraces(p.ceParser)
	if err != nil {
		slog.Warn("Failed to parse traces section", "error", err)
		return nil
	}

	return traces
}

// extractCertificateSections extracts certificate automation sections (acme).
func (p *Parser) extractCertificateSections(conf *StructuredConfig) error {
	acmeProviders, err := p.extractAcmeProviders()
	if err != nil {
		return fmt.Errorf("failed to extract acme providers: %w", err)
	}
	conf.AcmeProviders = acmeProviders

	return nil
}

// extractAcmeProviders extracts all acme sections.
func (p *Parser) extractAcmeProviders() ([]*models.AcmeProvider, error) {
	sections, err := p.ceParser.SectionsGet(clientparser.Acme)
	if err != nil {
		return nil, err
	}

	acmeProviders := make([]*models.AcmeProvider, 0, len(sections))
	for _, sectionName := range sections {
		acmeProvider, err := configuration.ParseAcmeProvider(p.ceParser, sectionName)
		if err != nil {
			slog.Warn("Failed to parse acme section", "section", sectionName, "error", err)
			continue
		}

		acmeProviders = append(acmeProviders, acmeProvider)
	}

	return acmeProviders, nil
}

// extractEEStandaloneSections extracts EE standalone sections (udp-lb, waf-global, etc.).
func (p *Parser) extractEEStandaloneSections(parsed *ConfiguredParsers, conf *StructuredConfig) {
	// Extract WAF Global
	if parsed.WAFGlobal != nil {
		conf.WAFGlobal = p.extractWAFGlobal(parsed.WAFGlobal)
	}

	// Extract WAF Profiles
	for name, wafParsers := range parsed.WAFProfile {
		profile := p.extractWAFProfile(name, wafParsers)
		if profile != nil {
			conf.WAFProfiles = append(conf.WAFProfiles, profile)
		}
	}

	// Extract BotMgmt Profiles
	for name, botParsers := range parsed.BotMgmtProfile {
		profile := p.extractBotMgmtProfile(name, botParsers)
		if profile != nil {
			conf.BotMgmtProfiles = append(conf.BotMgmtProfiles, profile)
		}
	}

	// Extract Captchas
	for name, captchaParsers := range parsed.Captcha {
		captcha := p.extractCaptcha(name, captchaParsers)
		if captcha != nil {
			conf.Captchas = append(conf.Captchas, captcha)
		}
	}

	// Extract UDP LBs
	for name, udpParsers := range parsed.UDPLB {
		udplb := p.extractUDPLB(name, udpParsers)
		if udplb != nil {
			conf.UDPLBs = append(conf.UDPLBs, udplb)
		}
	}
}

// extractWAFGlobal extracts the waf-global section.
func (p *Parser) extractWAFGlobal(wafParsers *clientparser.Parsers) *v32ee.WafGlobal {
	return extractWAFGlobalFields(wafParsers)
}

// extractWAFProfile extracts a waf-profile section.
func (p *Parser) extractWAFProfile(name string, wafParsers *clientparser.Parsers) *v32ee.WafProfile {
	return extractWAFProfileFields(name, wafParsers)
}

// extractBotMgmtProfile extracts a botmgmt-profile section.
func (p *Parser) extractBotMgmtProfile(name string, botParsers *clientparser.Parsers) *v32ee.BotmgmtProfile {
	return extractBotMgmtProfileFields(name, botParsers)
}

// extractCaptcha extracts a captcha section.
func (p *Parser) extractCaptcha(name string, captchaParsers *clientparser.Parsers) *v32ee.Captcha {
	return extractCaptchaFields(name, captchaParsers)
}

// extractUDPLB extracts a udp-lb section.
func (p *Parser) extractUDPLB(name string, udpParsers *clientparser.Parsers) *v32ee.UdpLbBase {
	return extractUDPLBFields(name, udpParsers)
}

// extractEEDirectivesFromCESections extracts EE-specific directives from CE sections.
// This captures filter waf/botmgmt, http-request waf-evaluate/botmgmt-evaluate, etc.
func (p *Parser) extractEEDirectivesFromCESections(parsed *ConfiguredParsers, conf *StructuredConfig) {
	// Extract EE directives from global
	if parsed.Global != nil {
		if eeData := p.extractEEGlobalData(parsed.Global); eeData != nil {
			conf.EEGlobal = eeData
		}
	}

	// Extract EE directives from frontends
	for name, feParsers := range parsed.Frontend {
		if eeData := p.extractEEFrontendData(feParsers); eeData != nil {
			conf.EEFrontends[name] = eeData
		}
	}

	// Extract EE directives from backends
	for name, beParsers := range parsed.Backend {
		if eeData := p.extractEEBackendData(beParsers); eeData != nil {
			conf.EEBackends[name] = eeData
		}
	}
}

// extractEEGlobalData extracts EE-specific directives from global section.
func (p *Parser) extractEEGlobalData(globalParsers *clientparser.Parsers) *parserconfig.EEGlobalData {
	if globalParsers == nil || globalParsers.Parsers == nil {
		return nil
	}

	parser, ok := globalParsers.Parsers["global-ee"]
	if !ok {
		return nil
	}

	eeParser, ok := parser.(*parsers.GlobalEE)
	if !ok {
		return nil
	}

	directives := eeParser.GetDirectives()
	if len(directives) == 0 {
		return nil
	}

	return &parserconfig.EEGlobalData{
		Directives: convertEEGlobalDirectives(directives),
	}
}

// extractEEFrontendData extracts EE-specific directives from a frontend section.
func (p *Parser) extractEEFrontendData(feParsers *clientparser.Parsers) *EEFrontendData {
	if feParsers == nil || feParsers.Parsers == nil {
		return nil
	}

	data := &EEFrontendData{}

	// Extract EE filters
	if parser, ok := feParsers.Parsers["filter"]; ok {
		if filterParser, ok := parser.(*parsers.Filters); ok {
			data.Filters = filterParser.GetEEFilters()
		}
	}

	// Extract EE HTTP request actions
	if parser, ok := feParsers.Parsers["http-request"]; ok {
		if httpParser, ok := parser.(*parsers.HTTPRequests); ok {
			data.HTTPRequests = httpParser.GetEEActions()
		}
	}

	if len(data.Filters) == 0 && len(data.HTTPRequests) == 0 {
		return nil
	}

	return data
}

// extractEEBackendData extracts EE-specific directives from a backend section.
func (p *Parser) extractEEBackendData(beParsers *clientparser.Parsers) *EEBackendData {
	if beParsers == nil || beParsers.Parsers == nil {
		return nil
	}

	data := &EEBackendData{}

	// Extract EE filters
	if parser, ok := beParsers.Parsers["filter"]; ok {
		if filterParser, ok := parser.(*parsers.Filters); ok {
			data.Filters = filterParser.GetEEFilters()
		}
	}

	// Extract EE HTTP request actions
	if parser, ok := beParsers.Parsers["http-request"]; ok {
		if httpParser, ok := parser.(*parsers.HTTPRequests); ok {
			data.HTTPRequests = httpParser.GetEEActions()
		}
	}

	if len(data.Filters) == 0 && len(data.HTTPRequests) == 0 {
		return nil
	}

	return data
}

// convertEEGlobalDirectives converts parser directives to parserconfig directives.
// Since both are type aliases to parserconfig.EEGlobalDirective, this is a direct return.
func convertEEGlobalDirectives(directives []*parsers.EEGlobalDirective) []*parserconfig.EEGlobalDirective {
	// Both types are aliases to parserconfig.EEGlobalDirective, so they're identical
	return directives
}
