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

// Package sectionextract extracts standard (Community Edition) HAProxy sections
// from a client-native config-parser into a parserconfig.StructuredConfig.
//
// The logic here is edition- and version-agnostic: it operates purely on the
// config-parser.Parser interface, so both the CE parser (pkg/dataplane/parser)
// and the Enterprise parser (pkg/dataplane/parser/enterprise) share it verbatim
// for their CE-section pass. Enterprise-specific sections and directives layer
// on top in the enterprise package; nothing in this package knows about them.
package sectionextract

import (
	"fmt"
	"log/slog"

	parser "github.com/haproxytech/client-native/v6/config-parser"
	"github.com/haproxytech/client-native/v6/configuration"
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// logSectionParseError logs a warning when a configuration section fails to parse.
func logSectionParseError(sectionType, sectionName string, err error) {
	slog.Warn("Failed to parse section", "type", sectionType, "section", sectionName, "error", err)
}

// All extracts every standard HAProxy section from p into conf, in the order the
// sections must be assembled. conf must already be initialised (its pointer
// indexes allocated) via parserconfig.NewStructuredConfig.
//
// The error-wrapping here is the single source of truth for both parsers: a
// section whose returning-parse fails aborts extraction with a wrapped error,
// while sections parsed best-effort (frontends, backends, peers, resolvers,
// mailers, userlists) log and skip individual failures via logSectionParseError.
func All(p parser.Parser, conf *parserconfig.StructuredConfig) error {
	// Core sections (global, defaults, frontends, backends).
	if err := coreSections(p, conf); err != nil {
		return err
	}

	// Peer and service discovery sections (peers, resolvers, mailers).
	peerAndDiscoverySections(p, conf)

	// Service sections (caches, rings, http-errors, userlists).
	if err := serviceSections(p, conf); err != nil {
		return err
	}

	// Program and application sections (programs, log-forwards, fcgi-apps, crt-stores).
	if err := programSections(p, conf); err != nil {
		return err
	}

	// Observability sections (log-profiles, traces) - v3.1+ features.
	if err := observabilitySections(p, conf); err != nil {
		return err
	}

	// Certificate automation sections (acme) - v3.2+ features.
	return certificateSections(p, conf)
}

// coreSections extracts core HAProxy sections (global, defaults, frontends, backends).
func coreSections(p parser.Parser, conf *parserconfig.StructuredConfig) error {
	global, err := extractGlobal(p)
	if err != nil {
		return fmt.Errorf("extracting global section: %w", err)
	}
	conf.Global = global

	defaults, err := extractDefaults(p)
	if err != nil {
		return fmt.Errorf("extracting defaults sections: %w", err)
	}
	conf.Defaults = defaults

	extractFrontendsWithIndexes(p, conf)
	extractBackendsWithIndexes(p, conf)

	return nil
}

// peerAndDiscoverySections extracts peer and service discovery sections
// (peers, resolvers, mailers) and builds pointer indexes for nested entries.
func peerAndDiscoverySections(p parser.Parser, conf *parserconfig.StructuredConfig) {
	extractPeersWithIndexes(p, conf)
	extractResolversWithIndexes(p, conf)
	extractMailersWithIndexes(p, conf)
}

// serviceSections extracts service sections (caches, rings, http-errors, userlists).
func serviceSections(p parser.Parser, conf *parserconfig.StructuredConfig) error {
	caches, err := extractCaches(p)
	if err != nil {
		return fmt.Errorf("extracting caches: %w", err)
	}
	conf.Caches = caches

	rings, err := extractRings(p)
	if err != nil {
		return fmt.Errorf("extracting rings: %w", err)
	}
	conf.Rings = rings

	httpErrors, err := extractHTTPErrors(p)
	if err != nil {
		return fmt.Errorf("extracting http-errors: %w", err)
	}
	conf.HTTPErrors = httpErrors

	extractUserlistsWithIndexes(p, conf)

	return nil
}

// programSections extracts program and application sections
// (programs, log-forwards, fcgi-apps, crt-stores).
func programSections(p parser.Parser, conf *parserconfig.StructuredConfig) error {
	programs, err := extractPrograms(p)
	if err != nil {
		return fmt.Errorf("extracting programs: %w", err)
	}
	conf.Programs = programs

	logForwards, err := extractLogForwards(p)
	if err != nil {
		return fmt.Errorf("extracting log-forwards: %w", err)
	}
	conf.LogForwards = logForwards

	fcgiApps, err := extractFCGIApps(p)
	if err != nil {
		return fmt.Errorf("extracting fcgi-apps: %w", err)
	}
	conf.FCGIApps = fcgiApps

	crtStores, err := extractCrtStores(p)
	if err != nil {
		return fmt.Errorf("extracting crt-stores: %w", err)
	}
	conf.CrtStores = crtStores

	return nil
}

// observabilitySections extracts observability sections (log-profiles, traces).
// These are v3.1+ features for advanced logging and request tracing.
func observabilitySections(p parser.Parser, conf *parserconfig.StructuredConfig) error {
	logProfiles, err := extractLogProfiles(p)
	if err != nil {
		return fmt.Errorf("extracting log-profiles: %w", err)
	}
	conf.LogProfiles = logProfiles

	conf.Traces = extractTraces(p)

	return nil
}

// certificateSections extracts certificate automation sections (acme).
// These are v3.2+ features for ACME/Let's Encrypt certificate automation.
func certificateSections(p parser.Parser, conf *parserconfig.StructuredConfig) error {
	acmeProviders, err := extractAcmeProviders(p)
	if err != nil {
		return fmt.Errorf("extracting acme providers: %w", err)
	}
	conf.AcmeProviders = acmeProviders

	return nil
}

// extractSectionsByParse iterates each section of the given type and delegates
// to a "returning" client-native parse function (one that builds the model
// itself). Sections whose parse fails are logged via logSectionParseError and
// skipped, matching the original per-section extractor behaviour.
func extractSectionsByParse[T any](
	p parser.Parser,
	sectionType parser.Section,
	label string,
	parse func(parser.Parser, string) (*T, error),
) ([]*T, error) {
	sections, err := p.SectionsGet(sectionType)
	if err != nil {
		return nil, err
	}
	results := make([]*T, 0, len(sections))
	for _, sectionName := range sections {
		item, err := parse(p, sectionName)
		if err != nil {
			logSectionParseError(label, sectionName, err)
			continue
		}
		results = append(results, item)
	}
	return results, nil
}

// extractSectionsByFill iterates each section of the given type and delegates
// to a "filling" client-native parse function (one that mutates a model
// allocated by init). Used when the model carries the section name in a
// pre-set field that the parse function expects already populated.
func extractSectionsByFill[T any](
	p parser.Parser,
	sectionType parser.Section,
	label string,
	init func(name string) *T,
	parse func(parser.Parser, *T) error,
) ([]*T, error) {
	sections, err := p.SectionsGet(sectionType)
	if err != nil {
		return nil, err
	}
	results := make([]*T, 0, len(sections))
	for _, sectionName := range sections {
		item := init(sectionName)
		if err := parse(p, item); err != nil {
			logSectionParseError(label, sectionName, err)
			continue
		}
		results = append(results, item)
	}
	return results, nil
}

// extractGlobal extracts the global section using client-native's ParseGlobalSection.
//
// This automatically handles ALL global fields (100+ fields including maxconn, daemon,
// nbproc, nbthread, pidfile, stats sockets, chroot, user, group, tune options, SSL options,
// performance options, lua options, etc.) and all nested structures (PerformanceOptions,
// TuneOptions, LogTargets, etc.) without manual handling.
func extractGlobal(p parser.Parser) (*models.Global, error) {
	global, err := configuration.ParseGlobalSection(p)
	if err != nil {
		return nil, fmt.Errorf("parsing global section: %w", err)
	}

	// Parse log targets separately (nested structure).
	// Global section has no name (empty string).
	logTargets, err := configuration.ParseLogTargets(string(parser.Global), "", p)
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
func extractDefaults(p parser.Parser) ([]*models.Defaults, error) {
	sections, err := p.SectionsGet(parser.Defaults)
	if err != nil {
		// No defaults sections is valid.
		return nil, err
	}

	defaults := make([]*models.Defaults, 0, len(sections))
	for _, sectionName := range sections {
		def := &models.Defaults{}

		// ParseSection handles ALL DefaultsBase fields automatically (60+ fields).
		if err := configuration.ParseSection(&def.DefaultsBase, parser.Defaults, sectionName, p); err != nil {
			logSectionParseError("defaults", sectionName, err)
			continue
		}
		def.Name = sectionName

		// Parse log targets separately (nested structure).
		logTargets, err := configuration.ParseLogTargets(string(parser.Defaults), sectionName, p)
		if err == nil {
			def.LogTargetList = logTargets
		}

		// Parse QUIC initial rules (v3.2+ feature for HTTP/3 support).
		def.QUICInitialRuleList, _ = configuration.ParseQUICInitialRules(string(parser.Defaults), sectionName, p)

		defaults = append(defaults, def)
	}

	return defaults, nil
}

// extractFrontendsWithIndexes extracts all frontend sections and builds pointer indexes.
//
// This automatically handles ALL frontend fields (80+ fields) and nested structures
// (binds, ACLs, HTTP/TCP rules, filters, log targets, etc.) using specialized Parse* helpers.
// Binds are stored in BindIndex for zero-copy iteration during comparison.
func extractFrontendsWithIndexes(p parser.Parser, conf *parserconfig.StructuredConfig) {
	sections, err := p.SectionsGet(parser.Frontends)
	if err != nil {
		// No frontends is valid.
		return
	}

	frontends := make([]*models.Frontend, 0, len(sections))
	for _, sectionName := range sections {
		fe := &models.Frontend{}

		// ParseSection handles ALL FrontendBase fields automatically (80+ fields:
		// mode, maxconn, default_backend, timeouts, compression, forwardfor, httplog, etc.)
		if err := configuration.ParseSection(&fe.FrontendBase, parser.Frontends, sectionName, p); err != nil {
			logSectionParseError("frontend", sectionName, err)
			continue
		}
		fe.Name = sectionName

		// Parse nested structures using client-native's Parse* helpers.
		fe.ACLList, _ = configuration.ParseACLs(parser.Frontends, sectionName, p)

		// Parse binds and build pointer index for zero-copy iteration.
		binds, _ := configuration.ParseBinds(string(parser.Frontends), sectionName, p)
		conf.BindIndex[sectionName] = parserconfig.BuildPointerIndex(binds, func(b *models.Bind) string { return b.Name })

		fe.HTTPRequestRuleList, _ = configuration.ParseHTTPRequestRules(string(parser.Frontends), sectionName, p)
		fe.HTTPResponseRuleList, _ = configuration.ParseHTTPResponseRules(string(parser.Frontends), sectionName, p)
		fe.TCPRequestRuleList, _ = configuration.ParseTCPRequestRules(string(parser.Frontends), sectionName, p)
		fe.HTTPAfterResponseRuleList, _ = configuration.ParseHTTPAfterRules(string(parser.Frontends), sectionName, p)
		fe.HTTPErrorRuleList, _ = configuration.ParseHTTPErrorRules(string(parser.Frontends), sectionName, p)
		fe.FilterList, _ = configuration.ParseFilters(string(parser.Frontends), sectionName, p)
		fe.LogTargetList, _ = configuration.ParseLogTargets(string(parser.Frontends), sectionName, p)
		fe.BackendSwitchingRuleList, _ = configuration.ParseBackendSwitchingRules(sectionName, p)
		fe.CaptureList, _ = configuration.ParseDeclareCaptures(sectionName, p)
		// Parse QUIC initial rules (v3.2+ feature for HTTP/3 support).
		fe.QUICInitialRuleList, _ = configuration.ParseQUICInitialRules(string(parser.Frontends), sectionName, p)

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
func extractBackendsWithIndexes(p parser.Parser, conf *parserconfig.StructuredConfig) {
	sections, err := p.SectionsGet(parser.Backends)
	if err != nil {
		// No backends is valid.
		return
	}

	backends := make([]*models.Backend, 0, len(sections))
	for _, sectionName := range sections {
		be := &models.Backend{}

		// ParseSection handles ALL BackendBase fields automatically (100+ fields:
		// mode, balance, timeouts, cookie, compression, forwardfor, httpchk, etc.)
		if err := configuration.ParseSection(&be.BackendBase, parser.Backends, sectionName, p); err != nil {
			logSectionParseError("backend", sectionName, err)
			continue
		}
		be.Name = sectionName

		// Parse nested structures and build pointer indexes.
		parseBackendNestedStructuresWithIndexes(p, sectionName, be, conf)

		backends = append(backends, be)
	}

	conf.Backends = backends
}

// parseBackendNestedStructuresWithIndexes parses all nested structures for a backend
// and builds pointer indexes for servers and server templates.
func parseBackendNestedStructuresWithIndexes(p parser.Parser, sectionName string, be *models.Backend, conf *parserconfig.StructuredConfig) {
	// Parse ACLs.
	be.ACLList, _ = configuration.ParseACLs(parser.Backends, sectionName, p)

	// Parse servers and build pointer index for zero-copy iteration.
	servers, _ := configuration.ParseServers(string(parser.Backends), sectionName, p)
	conf.ServerIndex[sectionName] = parserconfig.BuildPointerIndex(servers, func(s *models.Server) string { return s.Name })

	// Parse HTTP/TCP rules.
	parseBackendRules(p, sectionName, be)

	// Parse filters, log targets, and checks.
	parseBackendFiltersAndChecks(p, sectionName, be)

	// Parse server templates and build pointer index for zero-copy iteration.
	serverTemplates, _ := configuration.ParseServerTemplates(sectionName, p)
	conf.ServerTemplateIndex[sectionName] = parserconfig.BuildPointerIndex(serverTemplates, func(t *models.ServerTemplate) string { return t.Prefix })
}

// parseBackendRules parses HTTP and TCP rules for a backend.
func parseBackendRules(p parser.Parser, sectionName string, be *models.Backend) {
	be.HTTPRequestRuleList, _ = configuration.ParseHTTPRequestRules(string(parser.Backends), sectionName, p)
	be.HTTPResponseRuleList, _ = configuration.ParseHTTPResponseRules(string(parser.Backends), sectionName, p)
	be.TCPRequestRuleList, _ = configuration.ParseTCPRequestRules(string(parser.Backends), sectionName, p)
	be.TCPResponseRuleList, _ = configuration.ParseTCPResponseRules(string(parser.Backends), sectionName, p)
	be.HTTPAfterResponseRuleList, _ = configuration.ParseHTTPAfterRules(string(parser.Backends), sectionName, p)
	be.HTTPErrorRuleList, _ = configuration.ParseHTTPErrorRules(string(parser.Backends), sectionName, p)
	be.ServerSwitchingRuleList, _ = configuration.ParseServerSwitchingRules(sectionName, p)
	be.StickRuleList, _ = configuration.ParseStickRules(sectionName, p)
}

// parseBackendFiltersAndChecks parses filters, log targets, and health checks for a backend.
func parseBackendFiltersAndChecks(p parser.Parser, sectionName string, be *models.Backend) {
	be.FilterList, _ = configuration.ParseFilters(string(parser.Backends), sectionName, p)
	be.LogTargetList, _ = configuration.ParseLogTargets(string(parser.Backends), sectionName, p)
	be.HTTPCheckList, _ = configuration.ParseHTTPChecks(string(parser.Backends), sectionName, p)
	be.TCPCheckRuleList, _ = configuration.ParseTCPChecks(string(parser.Backends), sectionName, p)
}

// extractPeersWithIndexes extracts all peers sections and builds pointer indexes.
func extractPeersWithIndexes(p parser.Parser, conf *parserconfig.StructuredConfig) {
	sections, err := p.SectionsGet(parser.Peers)
	if err != nil {
		return
	}

	peers := make([]*models.PeerSection, 0, len(sections))
	for _, sectionName := range sections {
		peer := &models.PeerSection{}

		// ParseSection handles all peer section fields.
		if err := configuration.ParseSection(peer, parser.Peers, sectionName, p); err != nil {
			logSectionParseError("peers", sectionName, err)
			continue
		}
		peer.Name = sectionName

		// Parse peer entries and build pointer index for zero-copy iteration.
		peerEntries, _ := configuration.ParsePeerEntries(sectionName, p)
		conf.PeerEntryIndex[sectionName] = parserconfig.BuildPointerIndex(peerEntries, func(e *models.PeerEntry) string { return e.Name })

		peers = append(peers, peer)
	}

	conf.Peers = peers
}

// extractResolversWithIndexes extracts all resolvers sections and builds pointer indexes.
func extractResolversWithIndexes(p parser.Parser, conf *parserconfig.StructuredConfig) {
	sections, err := p.SectionsGet(parser.Resolvers)
	if err != nil {
		return
	}

	resolvers := make([]*models.Resolver, 0, len(sections))
	for _, sectionName := range sections {
		resolver := &models.Resolver{}
		resolver.Name = sectionName

		// ParseResolverSection handles all resolver fields automatically.
		if err := configuration.ParseResolverSection(p, resolver); err != nil {
			logSectionParseError("resolvers", sectionName, err)
			continue
		}

		// Parse nameservers and build pointer index for zero-copy iteration.
		nameservers, _ := configuration.ParseNameservers(sectionName, p)
		conf.NameserverIndex[sectionName] = parserconfig.BuildPointerIndex(nameservers, func(n *models.Nameserver) string { return n.Name })

		resolvers = append(resolvers, resolver)
	}

	conf.Resolvers = resolvers
}

// extractMailersWithIndexes extracts all mailers sections and builds pointer indexes.
func extractMailersWithIndexes(p parser.Parser, conf *parserconfig.StructuredConfig) {
	sections, err := p.SectionsGet(parser.Mailers)
	if err != nil {
		return
	}

	mailers := make([]*models.MailersSection, 0, len(sections))
	for _, sectionName := range sections {
		mailer := &models.MailersSection{}
		mailer.Name = sectionName

		// ParseMailersSection handles all mailer fields automatically.
		if err := configuration.ParseMailersSection(p, mailer); err != nil {
			logSectionParseError("mailers", sectionName, err)
			continue
		}

		// Parse mailer entries and build pointer index for zero-copy iteration.
		mailerEntries, _ := configuration.ParseMailerEntries(sectionName, p)
		conf.MailerEntryIndex[sectionName] = parserconfig.BuildPointerIndex(mailerEntries, func(e *models.MailerEntry) string { return e.Name })

		mailers = append(mailers, mailer)
	}

	conf.Mailers = mailers
}

// extractCaches extracts all cache sections using client-native's ParseCacheSection.
func extractCaches(p parser.Parser) ([]*models.Cache, error) {
	return extractSectionsByFill(p, parser.Cache, "cache",
		func(name string) *models.Cache {
			return &models.Cache{Name: &name}
		},
		configuration.ParseCacheSection,
	)
}

// extractRings extracts all ring sections using client-native's ParseRingSection.
func extractRings(p parser.Parser) ([]*models.Ring, error) {
	return extractSectionsByFill(p, parser.Ring, "ring",
		func(name string) *models.Ring {
			return &models.Ring{RingBase: models.RingBase{Name: name}}
		},
		configuration.ParseRingSection,
	)
}

// extractHTTPErrors extracts all http-errors sections using client-native's Parse* functions.
func extractHTTPErrors(p parser.Parser) ([]*models.HTTPErrorsSection, error) {
	sections, err := p.SectionsGet(parser.HTTPErrors)
	if err != nil {
		return nil, err
	}

	httpErrors := make([]*models.HTTPErrorsSection, 0, len(sections))
	for _, sectionName := range sections {
		// ParseHTTPErrorsSection handles complete parsing including ErrorFiles.
		httpError, err := configuration.ParseHTTPErrorsSection(p, sectionName)
		if err != nil {
			// Log error but continue with other sections.
			continue
		}

		httpErrors = append(httpErrors, httpError)
	}

	return httpErrors, nil
}

// extractUserlistsWithIndexes extracts all userlist sections and builds pointer indexes.
// Userlists contain users and groups for authentication.
func extractUserlistsWithIndexes(p parser.Parser, conf *parserconfig.StructuredConfig) {
	sections, err := p.SectionsGet(parser.UserList)
	if err != nil {
		return
	}

	userlists := make([]*models.Userlist, 0, len(sections))
	for _, sectionName := range sections {
		userlist := &models.Userlist{}
		userlist.Name = sectionName

		// Parse userlist base section.
		if err := configuration.ParseSection(&userlist.UserlistBase, parser.UserList, sectionName, p); err != nil {
			logSectionParseError("userlist", sectionName, err)
			continue
		}

		// Parse users and build pointer index for zero-copy iteration.
		users, _ := configuration.ParseUsers(sectionName, p)
		if userIndex := parserconfig.BuildUserIndex(users); userIndex != nil {
			conf.UserIndex[sectionName] = userIndex
		}

		// Parse groups and build pointer index for zero-copy iteration.
		groups, _ := configuration.ParseGroups(sectionName, p)
		if groupIndex := parserconfig.BuildGroupIndex(groups); groupIndex != nil {
			conf.GroupIndex[sectionName] = groupIndex
		}

		userlists = append(userlists, userlist)
	}

	conf.Userlists = userlists
}

// extractPrograms extracts all program sections using client-native's ParseProgram.
// Programs are external processes managed by HAProxy.
func extractPrograms(p parser.Parser) ([]*models.Program, error) {
	return extractSectionsByParse(p, parser.Program, "program", configuration.ParseProgram)
}

// extractLogForwards extracts all log-forward sections using client-native's ParseLogForward.
// Log-forwards define log forwarding rules.
func extractLogForwards(p parser.Parser) ([]*models.LogForward, error) {
	return extractSectionsByFill(p, parser.LogForward, "log-forward",
		func(name string) *models.LogForward {
			return &models.LogForward{LogForwardBase: models.LogForwardBase{Name: name}}
		},
		configuration.ParseLogForward,
	)
}

// extractFCGIApps extracts all fcgi-app sections using client-native's ParseFCGIApp.
// FCGI apps define FastCGI application configurations.
func extractFCGIApps(p parser.Parser) ([]*models.FCGIApp, error) {
	return extractSectionsByParse(p, parser.FCGIApp, "fcgi-app", configuration.ParseFCGIApp)
}

// extractCrtStores extracts all crt-store sections using client-native's ParseCrtStore.
// Certificate stores define locations for SSL certificates.
func extractCrtStores(p parser.Parser) ([]*models.CrtStore, error) {
	return extractSectionsByFill(p, parser.CrtStore, "crt-store",
		func(name string) *models.CrtStore {
			return &models.CrtStore{CrtStoreBase: models.CrtStoreBase{Name: name}}
		},
		configuration.ParseCrtStore,
	)
}

// extractLogProfiles extracts all log-profile sections using client-native's ParseLogProfile.
// Log profiles define logging profiles for one or more steps (v3.1+ feature).
func extractLogProfiles(p parser.Parser) ([]*models.LogProfile, error) {
	return extractSectionsByParse(p, parser.LogProfile, "log-profile", configuration.ParseLogProfile)
}

// extractTraces extracts the traces section using client-native's ParseTraces.
// Traces is a singleton section for request tracing configuration (v3.1+ feature).
// Returns nil when no traces section exists (which is valid - traces is optional).
func extractTraces(p parser.Parser) *models.Traces {
	// Traces is a singleton - check if section exists.
	if !p.SectionExists(parser.Traces, parser.TracesSectionName) {
		return nil
	}

	// ParseTraces handles all traces fields automatically.
	traces, err := configuration.ParseTraces(p)
	if err != nil {
		logSectionParseError("traces", "", err)
		return nil
	}

	return traces
}

// extractAcmeProviders extracts all acme sections using client-native's ParseAcmeProvider.
// ACME providers define Let's Encrypt/ACME certificate automation configuration (v3.2+ feature).
func extractAcmeProviders(p parser.Parser) ([]*models.AcmeProvider, error) {
	return extractSectionsByParse(p, parser.Acme, "acme", configuration.ParseAcmeProvider)
}
