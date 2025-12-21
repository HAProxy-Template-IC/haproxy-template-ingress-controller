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

	"haproxy-template-ic/pkg/dataplane/parser/parserconfig"
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
func NormalizeConfigMetadata(config *parserconfig.StructuredConfig) {
	if config == nil {
		return
	}

	// Normalize main sections
	normalizeFrontendsMetadata(config.Frontends)
	normalizeBackendsMetadata(config.Backends)
	normalizeDefaultsSectionsMetadata(config.Defaults)
	normalizeGlobalMetadata(config.Global)

	// Normalize other top-level sections
	normalizeOtherSectionsMetadata(config)
}

// normalizeFrontendsMetadata normalizes all frontends.
func normalizeFrontendsMetadata(frontends []*models.Frontend) {
	for _, frontend := range frontends {
		normalizeFrontendMetadata(frontend)
	}
}

// normalizeBackendsMetadata normalizes all backends.
func normalizeBackendsMetadata(backends []*models.Backend) {
	for _, backend := range backends {
		normalizeBackendMetadata(backend)
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

// normalizeOtherSectionsMetadata normalizes miscellaneous sections.
func normalizeOtherSectionsMetadata(config *parserconfig.StructuredConfig) {
	normalizePeersMetadata(config.Peers)
	normalizeResolversMetadata(config.Resolvers)
	normalizeMailersMetadata(config.Mailers)
	normalizeCachesMetadata(config.Caches)
	normalizeRingsMetadata(config.Rings)
	normalizeUserlistsMetadata(config.Userlists)
	normalizeProgramsMetadata(config.Programs)
	normalizeFCGIAppsMetadata(config.FCGIApps)
	normalizeCrtStoresMetadata(config.CrtStores)
	normalizeAcmeProvidersMetadata(config.AcmeProviders)
}

func normalizePeersMetadata(peers []*models.PeerSection) {
	for _, peer := range peers {
		if peer != nil {
			for _, entry := range peer.PeerEntries {
				entry.Metadata = NormalizeMetadata(entry.Metadata)
			}
		}
	}
}

func normalizeResolversMetadata(resolvers []*models.Resolver) {
	for _, resolver := range resolvers {
		if resolver != nil {
			resolver.Metadata = NormalizeMetadata(resolver.Metadata)
			for name := range resolver.Nameservers {
				ns := resolver.Nameservers[name]
				ns.Metadata = NormalizeMetadata(ns.Metadata)
				resolver.Nameservers[name] = ns
			}
		}
	}
}

func normalizeMailersMetadata(mailers []*models.MailersSection) {
	for _, mailer := range mailers {
		if mailer != nil {
			mailer.Metadata = NormalizeMetadata(mailer.Metadata)
			for _, entry := range mailer.MailerEntries {
				entry.Metadata = NormalizeMetadata(entry.Metadata)
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

func normalizeUserlistsMetadata(userlists []*models.Userlist) {
	for _, userlist := range userlists {
		if userlist != nil {
			userlist.Metadata = NormalizeMetadata(userlist.Metadata)
			for _, user := range userlist.Users {
				user.Metadata = NormalizeMetadata(user.Metadata)
			}
			for _, group := range userlist.Groups {
				group.Metadata = NormalizeMetadata(group.Metadata)
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

// normalizeFrontendMetadata normalizes all Metadata fields in a Frontend and its nested collections.
func normalizeFrontendMetadata(f *models.Frontend) {
	if f == nil {
		return
	}

	// Frontend itself
	f.Metadata = NormalizeMetadata(f.Metadata)

	// Binds - use indexing to avoid copying on each iteration
	for name := range f.Binds {
		bind := f.Binds[name]
		bind.Metadata = NormalizeMetadata(bind.Metadata)
		f.Binds[name] = bind
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

// normalizeBackendMetadata normalizes all Metadata fields in a Backend and its nested collections.
func normalizeBackendMetadata(b *models.Backend) {
	if b == nil {
		return
	}

	// Backend itself
	b.Metadata = NormalizeMetadata(b.Metadata)

	// Servers - use indexing to avoid copying on each iteration
	for name := range b.Servers {
		server := b.Servers[name]
		server.Metadata = NormalizeMetadata(server.Metadata)
		b.Servers[name] = server
	}

	// Server templates - use indexing to avoid copying on each iteration
	for name := range b.ServerTemplates {
		template := b.ServerTemplates[name]
		template.Metadata = NormalizeMetadata(template.Metadata)
		b.ServerTemplates[name] = template
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
