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
	"fmt"
	"log/slog"

	parser "github.com/haproxytech/client-native/v6/config-parser"
	"github.com/haproxytech/client-native/v6/configuration"
	"github.com/haproxytech/client-native/v6/models"
)

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
