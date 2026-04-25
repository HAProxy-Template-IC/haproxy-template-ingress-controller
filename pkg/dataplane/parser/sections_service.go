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
	parser "github.com/haproxytech/client-native/v6/config-parser"
	"github.com/haproxytech/client-native/v6/configuration"
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// extractSectionsByParse iterates each section of the given type and delegates
// to a "returning" client-native parse function (one that builds the model
// itself). Sections whose parse fails are logged via logSectionParseError and
// skipped, matching the original per-section extractor behaviour.
func extractSectionsByParse[T any](
	p *Parser,
	sectionType parser.Section,
	label string,
	parse func(parser.Parser, string) (*T, error),
) ([]*T, error) {
	sections, err := p.parser.SectionsGet(sectionType)
	if err != nil {
		return nil, err
	}
	results := make([]*T, 0, len(sections))
	for _, sectionName := range sections {
		item, err := parse(p.parser, sectionName)
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
	p *Parser,
	sectionType parser.Section,
	label string,
	init func(name string) *T,
	parse func(parser.Parser, *T) error,
) ([]*T, error) {
	sections, err := p.parser.SectionsGet(sectionType)
	if err != nil {
		return nil, err
	}
	results := make([]*T, 0, len(sections))
	for _, sectionName := range sections {
		item := init(sectionName)
		if err := parse(p.parser, item); err != nil {
			logSectionParseError(label, sectionName, err)
			continue
		}
		results = append(results, item)
	}
	return results, nil
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
			logSectionParseError("peers", sectionName, err)
			continue
		}
		peer.Name = sectionName

		// Parse peer entries and build pointer index for zero-copy iteration.
		peerEntries, _ := configuration.ParsePeerEntries(sectionName, p.parser)
		conf.PeerEntryIndex[sectionName] = parserconfig.BuildPointerIndex(peerEntries, func(e *models.PeerEntry) string { return e.Name })

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
			logSectionParseError("resolvers", sectionName, err)
			continue
		}

		// Parse nameservers and build pointer index for zero-copy iteration.
		nameservers, _ := configuration.ParseNameservers(sectionName, p.parser)
		conf.NameserverIndex[sectionName] = parserconfig.BuildPointerIndex(nameservers, func(n *models.Nameserver) string { return n.Name })

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
			logSectionParseError("mailers", sectionName, err)
			continue
		}

		// Parse mailer entries and build pointer index for zero-copy iteration.
		mailerEntries, _ := configuration.ParseMailerEntries(sectionName, p.parser)
		conf.MailerEntryIndex[sectionName] = parserconfig.BuildPointerIndex(mailerEntries, func(e *models.MailerEntry) string { return e.Name })

		mailers = append(mailers, mailer)
	}

	conf.Mailers = mailers
}

// extractCaches extracts all cache sections using client-native's ParseCacheSection.
func (p *Parser) extractCaches() ([]*models.Cache, error) {
	return extractSectionsByFill(p, parser.Cache, "cache",
		func(name string) *models.Cache {
			return &models.Cache{Name: &name}
		},
		configuration.ParseCacheSection,
	)
}

// extractRings extracts all ring sections using client-native's ParseRingSection.
func (p *Parser) extractRings() ([]*models.Ring, error) {
	return extractSectionsByFill(p, parser.Ring, "ring",
		func(name string) *models.Ring {
			return &models.Ring{RingBase: models.RingBase{Name: name}}
		},
		configuration.ParseRingSection,
	)
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
			logSectionParseError("userlist", sectionName, err)
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
	return extractSectionsByParse(p, parser.Program, "program", configuration.ParseProgram)
}

// extractLogForwards extracts all log-forward sections using client-native's ParseLogForward.
// Log-forwards define log forwarding rules.
func (p *Parser) extractLogForwards() ([]*models.LogForward, error) {
	return extractSectionsByFill(p, parser.LogForward, "log-forward",
		func(name string) *models.LogForward {
			return &models.LogForward{LogForwardBase: models.LogForwardBase{Name: name}}
		},
		configuration.ParseLogForward,
	)
}

// extractFCGIApps extracts all fcgi-app sections using client-native's ParseFCGIApp.
// FCGI apps define FastCGI application configurations.
func (p *Parser) extractFCGIApps() ([]*models.FCGIApp, error) {
	return extractSectionsByParse(p, parser.FCGIApp, "fcgi-app", configuration.ParseFCGIApp)
}

// extractCrtStores extracts all crt-store sections using client-native's ParseCrtStore.
// Certificate stores define locations for SSL certificates.
func (p *Parser) extractCrtStores() ([]*models.CrtStore, error) {
	return extractSectionsByFill(p, parser.CrtStore, "crt-store",
		func(name string) *models.CrtStore {
			return &models.CrtStore{CrtStoreBase: models.CrtStoreBase{Name: name}}
		},
		configuration.ParseCrtStore,
	)
}

// extractLogProfiles extracts all log-profile sections using client-native's ParseLogProfile.
// Log profiles define logging profiles for one or more steps (v3.1+ feature).
func (p *Parser) extractLogProfiles() ([]*models.LogProfile, error) {
	return extractSectionsByParse(p, parser.LogProfile, "log-profile", configuration.ParseLogProfile)
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
		logSectionParseError("traces", "", err)
		return nil
	}

	return traces
}

// extractAcmeProviders extracts all acme sections using client-native's ParseAcmeProvider.
// ACME providers define Let's Encrypt/ACME certificate automation configuration (v3.2+ feature).
func (p *Parser) extractAcmeProviders() ([]*models.AcmeProvider, error) {
	return extractSectionsByParse(p, parser.Acme, "acme", configuration.ParseAcmeProvider)
}
