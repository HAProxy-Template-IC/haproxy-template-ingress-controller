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
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// NormalizeConfigMetadata normalizes all Metadata fields in a StructuredConfig
// from nested API format to flat client-native format.
//
// This ensures consistent format for comparison regardless of where config was parsed from.
// When the Dataplane API stores configs with metadata, it converts flat format to nested
// JSON. When we parse that config back, the metadata comes back in nested format, but
// the desired config (from template) has flat format. This mismatch causes false positive
// updates during comparison.
//
// This function walks all models in the config and normalizes their Metadata fields.
// It uses pointer indexes for zero-copy iteration over nested elements.
func NormalizeConfigMetadata(config *parserconfig.StructuredConfig) {
	if config == nil {
		return
	}

	// Normalize main sections using pointer indexes
	normalizeFrontendsMetadataWithIndexes(config.Frontends, config.BindIndex)
	normalizeBackendsMetadataWithIndexes(config.Backends, config.ServerIndex, config.ServerTemplateIndex)
	normalizeDefaultsSectionsMetadata(config.Defaults)
	normalizeGlobalMetadata(config.Global)

	// Normalize other top-level sections using pointer indexes
	normalizeOtherSectionsMetadataWithIndexes(config)
}

// normalizeFrontendsMetadataWithIndexes normalizes all frontends using pointer indexes.
func normalizeFrontendsMetadataWithIndexes(frontends []*models.Frontend, bindIndex map[string]map[string]*models.Bind) {
	for _, frontend := range frontends {
		normalizeFrontendMetadataWithIndex(frontend, bindIndex)
	}
}

// normalizeBackendsMetadataWithIndexes normalizes all backends using pointer indexes.
func normalizeBackendsMetadataWithIndexes(backends []*models.Backend, serverIndex map[string]map[string]*models.Server, serverTemplateIndex map[string]map[string]*models.ServerTemplate) {
	for _, backend := range backends {
		normalizeBackendMetadataWithIndexes(backend, serverIndex, serverTemplateIndex)
	}
}

// normalizeDefaultsSectionsMetadata normalizes all defaults sections.
func normalizeDefaultsSectionsMetadata(defaults []*models.Defaults) {
	for _, d := range defaults {
		normalizeDefaultsMetadata(d)
	}
}

// normalizeGlobalMetadata normalizes the global section.
func normalizeGlobalMetadata(global *models.Global) {
	if global == nil {
		return
	}
	for _, logTarget := range global.LogTargetList {
		logTarget.Metadata = NormalizeMetadata(logTarget.Metadata)
	}
}

// normalizeOtherSectionsMetadataWithIndexes normalizes miscellaneous sections using pointer indexes.
func normalizeOtherSectionsMetadataWithIndexes(config *parserconfig.StructuredConfig) {
	normalizePeersMetadataWithIndex(config.Peers, config.PeerEntryIndex)
	normalizeResolversMetadataWithIndex(config.Resolvers, config.NameserverIndex)
	normalizeMailersMetadataWithIndex(config.Mailers, config.MailerEntryIndex)
	normalizeCachesMetadata(config.Caches)
	normalizeRingsMetadata(config.Rings)
	normalizeUserlistsMetadataWithIndexes(config.Userlists, config.UserIndex, config.GroupIndex)
	normalizeProgramsMetadata(config.Programs)
	normalizeFCGIAppsMetadata(config.FCGIApps)
	normalizeCrtStoresMetadata(config.CrtStores)
	normalizeAcmeProvidersMetadata(config.AcmeProviders)
}

func normalizePeersMetadataWithIndex(peers []*models.PeerSection, peerEntryIndex map[string]map[string]*models.PeerEntry) {
	for _, peer := range peers {
		if peer != nil {
			// Use pointer index for zero-copy iteration
			if entries, ok := peerEntryIndex[peer.Name]; ok {
				for _, entry := range entries {
					entry.Metadata = NormalizeMetadata(entry.Metadata)
				}
			}
		}
	}
}

func normalizeResolversMetadataWithIndex(resolvers []*models.Resolver, nameserverIndex map[string]map[string]*models.Nameserver) {
	for _, resolver := range resolvers {
		if resolver != nil {
			resolver.Metadata = NormalizeMetadata(resolver.Metadata)
			// Use pointer index for zero-copy iteration
			if nameservers, ok := nameserverIndex[resolver.Name]; ok {
				for _, ns := range nameservers {
					ns.Metadata = NormalizeMetadata(ns.Metadata)
				}
			}
		}
	}
}

func normalizeMailersMetadataWithIndex(mailers []*models.MailersSection, mailerEntryIndex map[string]map[string]*models.MailerEntry) {
	for _, mailer := range mailers {
		if mailer != nil {
			mailer.Metadata = NormalizeMetadata(mailer.Metadata)
			// Use pointer index for zero-copy iteration
			if entries, ok := mailerEntryIndex[mailer.Name]; ok {
				for _, entry := range entries {
					entry.Metadata = NormalizeMetadata(entry.Metadata)
				}
			}
		}
	}
}

func normalizeCachesMetadata(caches []*models.Cache) {
	for _, cache := range caches {
		if cache != nil {
			cache.Metadata = NormalizeMetadata(cache.Metadata)
		}
	}
}

func normalizeRingsMetadata(rings []*models.Ring) {
	for _, ring := range rings {
		if ring != nil {
			ring.Metadata = NormalizeMetadata(ring.Metadata)
		}
	}
}

func normalizeUserlistsMetadataWithIndexes(userlists []*models.Userlist, userIndex map[string]map[string]*models.User, groupIndex map[string]map[string]*models.Group) {
	for _, userlist := range userlists {
		if userlist != nil {
			userlist.Metadata = NormalizeMetadata(userlist.Metadata)
			// Use pointer index for zero-copy iteration
			if users, ok := userIndex[userlist.Name]; ok {
				for _, user := range users {
					user.Metadata = NormalizeMetadata(user.Metadata)
				}
			}
			// Use pointer index for zero-copy iteration
			if groups, ok := groupIndex[userlist.Name]; ok {
				for _, group := range groups {
					group.Metadata = NormalizeMetadata(group.Metadata)
				}
			}
		}
	}
}

func normalizeProgramsMetadata(programs []*models.Program) {
	for _, program := range programs {
		if program != nil {
			program.Metadata = NormalizeMetadata(program.Metadata)
		}
	}
}

func normalizeFCGIAppsMetadata(fcgiApps []*models.FCGIApp) {
	for _, fcgiApp := range fcgiApps {
		if fcgiApp != nil {
			fcgiApp.Metadata = NormalizeMetadata(fcgiApp.Metadata)
		}
	}
}

func normalizeCrtStoresMetadata(crtStores []*models.CrtStore) {
	for _, crtStore := range crtStores {
		if crtStore != nil {
			crtStore.Metadata = NormalizeMetadata(crtStore.Metadata)
			for _, crtLoad := range crtStore.Loads {
				crtLoad.Metadata = NormalizeMetadata(crtLoad.Metadata)
			}
		}
	}
}

func normalizeAcmeProvidersMetadata(acmeProviders []*models.AcmeProvider) {
	for _, acmeProvider := range acmeProviders {
		if acmeProvider != nil {
			acmeProvider.Metadata = NormalizeMetadata(acmeProvider.Metadata)
		}
	}
}

// normalizeFrontendMetadataWithIndex normalizes all Metadata fields in a Frontend and its nested collections.
// Uses pointer index for binds to avoid copying large structs.
func normalizeFrontendMetadataWithIndex(f *models.Frontend, bindIndex map[string]map[string]*models.Bind) {
	if f == nil {
		return
	}

	// Frontend itself
	f.Metadata = NormalizeMetadata(f.Metadata)

	// Binds - use pointer index for zero-copy iteration
	if binds, ok := bindIndex[f.Name]; ok {
		for _, bind := range binds {
			bind.Metadata = NormalizeMetadata(bind.Metadata)
		}
	}

	// ACLs
	for _, acl := range f.ACLList {
		acl.Metadata = NormalizeMetadata(acl.Metadata)
	}

	// Backend switching rules
	for _, rule := range f.BackendSwitchingRuleList {
		rule.Metadata = NormalizeMetadata(rule.Metadata)
	}

	// Captures
	for _, capture := range f.CaptureList {
		capture.Metadata = NormalizeMetadata(capture.Metadata)
	}

	// Filters
	for _, filter := range f.FilterList {
		filter.Metadata = NormalizeMetadata(filter.Metadata)
	}

	// HTTP after response rules
	for _, rule := range f.HTTPAfterResponseRuleList {
		rule.Metadata = NormalizeMetadata(rule.Metadata)
	}

	// HTTP error rules
	for _, rule := range f.HTTPErrorRuleList {
		rule.Metadata = NormalizeMetadata(rule.Metadata)
	}

	// HTTP request rules
	for _, rule := range f.HTTPRequestRuleList {
		rule.Metadata = NormalizeMetadata(rule.Metadata)
	}

	// HTTP response rules
	for _, rule := range f.HTTPResponseRuleList {
		rule.Metadata = NormalizeMetadata(rule.Metadata)
	}

	// Log targets
	for _, logTarget := range f.LogTargetList {
		logTarget.Metadata = NormalizeMetadata(logTarget.Metadata)
	}

	// QUIC initial rules
	for _, rule := range f.QUICInitialRuleList {
		rule.Metadata = NormalizeMetadata(rule.Metadata)
	}

	// SSL front uses
	for _, sslFrontUse := range f.SSLFrontUses {
		sslFrontUse.Metadata = NormalizeMetadata(sslFrontUse.Metadata)
	}

	// TCP request rules
	for _, rule := range f.TCPRequestRuleList {
		rule.Metadata = NormalizeMetadata(rule.Metadata)
	}
}

// normalizeBackendMetadataWithIndexes normalizes all Metadata fields in a Backend and its nested collections.
// Uses pointer indexes for servers and server templates to avoid copying large structs.
func normalizeBackendMetadataWithIndexes(b *models.Backend, serverIndex map[string]map[string]*models.Server, serverTemplateIndex map[string]map[string]*models.ServerTemplate) {
	if b == nil {
		return
	}

	// Backend itself
	b.Metadata = NormalizeMetadata(b.Metadata)

	// Servers - use pointer index for zero-copy iteration
	if servers, ok := serverIndex[b.Name]; ok {
		for _, server := range servers {
			server.Metadata = NormalizeMetadata(server.Metadata)
		}
	}

	// Server templates - use pointer index for zero-copy iteration
	if templates, ok := serverTemplateIndex[b.Name]; ok {
		for _, template := range templates {
			template.Metadata = NormalizeMetadata(template.Metadata)
		}
	}

	// ACLs
	for _, acl := range b.ACLList {
		acl.Metadata = NormalizeMetadata(acl.Metadata)
	}

	// Filters
	for _, filter := range b.FilterList {
		filter.Metadata = NormalizeMetadata(filter.Metadata)
	}

	// HTTP after response rules
	for _, rule := range b.HTTPAfterResponseRuleList {
		rule.Metadata = NormalizeMetadata(rule.Metadata)
	}

	// HTTP checks
	for _, check := range b.HTTPCheckList {
		check.Metadata = NormalizeMetadata(check.Metadata)
	}

	// HTTP error rules
	for _, rule := range b.HTTPErrorRuleList {
		rule.Metadata = NormalizeMetadata(rule.Metadata)
	}

	// HTTP request rules
	for _, rule := range b.HTTPRequestRuleList {
		rule.Metadata = NormalizeMetadata(rule.Metadata)
	}

	// HTTP response rules
	for _, rule := range b.HTTPResponseRuleList {
		rule.Metadata = NormalizeMetadata(rule.Metadata)
	}

	// Log targets
	for _, logTarget := range b.LogTargetList {
		logTarget.Metadata = NormalizeMetadata(logTarget.Metadata)
	}

	// Server switching rules
	for _, rule := range b.ServerSwitchingRuleList {
		rule.Metadata = NormalizeMetadata(rule.Metadata)
	}

	// Stick rules
	for _, rule := range b.StickRuleList {
		rule.Metadata = NormalizeMetadata(rule.Metadata)
	}

	// TCP checks
	for _, check := range b.TCPCheckRuleList {
		check.Metadata = NormalizeMetadata(check.Metadata)
	}

	// TCP request rules
	for _, rule := range b.TCPRequestRuleList {
		rule.Metadata = NormalizeMetadata(rule.Metadata)
	}

	// TCP response rules
	for _, rule := range b.TCPResponseRuleList {
		rule.Metadata = NormalizeMetadata(rule.Metadata)
	}
}

// normalizeDefaultsMetadata normalizes all Metadata fields in a Defaults section.
func normalizeDefaultsMetadata(d *models.Defaults) {
	if d == nil {
		return
	}

	// Defaults itself
	d.Metadata = NormalizeMetadata(d.Metadata)

	// Log targets
	for _, logTarget := range d.LogTargetList {
		logTarget.Metadata = NormalizeMetadata(logTarget.Metadata)
	}

	// HTTP error rules
	for _, rule := range d.HTTPErrorRuleList {
		rule.Metadata = NormalizeMetadata(rule.Metadata)
	}
}
