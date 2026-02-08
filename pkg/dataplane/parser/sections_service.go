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

package parser

import (
	"log/slog"

	parser "github.com/haproxytech/client-native/v6/config-parser"
	"github.com/haproxytech/client-native/v6/configuration"
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

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
