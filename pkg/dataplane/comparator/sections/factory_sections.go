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

package sections

import (
	"github.com/haproxytech/client-native/v6/models"
)

// Top-level CRUD builders for additional sections.
var (
	cacheOps        = NewTopLevelCRUD("cache", "cache", CacheName)
	httpErrorsOps   = NewTopLevelCRUD("http_errors", "http-errors section", HTTPErrorsSectionName)
	mailersOps      = NewTopLevelCRUD("mailers", "mailers", MailersSectionName)
	peerSectionOps  = NewTopLevelCRUD("peers", "peer section", PeerSectionName)
	programOps      = NewTopLevelCRUD("program", "program", ProgramName)
	resolverOps     = NewTopLevelCRUD("resolver", "resolver", ResolverName)
	ringOps         = NewTopLevelCRUD("ring", "ring", RingName)
	crtStoreOps     = NewTopLevelCRUD("crt_store", "crt-store", CrtStoreName)
	userlistOps     = NewTopLevelCRUD("userlist", "userlist", UserlistName)
	fcgiAppOps      = NewTopLevelCRUD("fcgi_app", "fcgi-app", FCGIAppName)
	logProfileOps   = NewTopLevelCRUD("log_profile", "log-profile", LogProfileName)
	acmeProviderOps = NewTopLevelCRUD("acme_provider", "acme-provider", AcmeProviderName)
)

// Container-child CRUD builders.
var (
	userOps        = NewContainerChildCRUD[*models.User]("user", "user", "userlist", UserName)
	mailerEntryOps = NewContainerChildCRUD[*models.MailerEntry]("mailer_entry", "mailer entry", "mailers section", MailerEntryName)
	peerEntryOps   = NewContainerChildCRUD[*models.PeerEntry]("peer_entry", "peer entry", "peer section", PeerEntryName)
	nameserverOps  = NewContainerChildCRUD[*models.Nameserver]("nameserver", "nameserver", "resolvers section", NameserverName)
)

// User factory functions (container children of userlist).

// NewUserCreate creates an operation to create a user in a userlist.
func NewUserCreate(userlistName string, user *models.User) Operation {
	return userOps.Create(userlistName, user)
}

// NewUserUpdate creates an operation to update a user in a userlist.
func NewUserUpdate(userlistName string, user *models.User) Operation {
	return userOps.Update(userlistName, user)
}

// NewUserDelete creates an operation to delete a user from a userlist.
func NewUserDelete(userlistName string, user *models.User) Operation {
	return userOps.Delete(userlistName, user)
}

// MailerEntry factory functions (container children of mailers section).

// NewMailerEntryCreate creates an operation to create a mailer entry.
func NewMailerEntryCreate(mailersName string, entry *models.MailerEntry) Operation {
	return mailerEntryOps.Create(mailersName, entry)
}

// NewMailerEntryUpdate creates an operation to update a mailer entry.
func NewMailerEntryUpdate(mailersName string, entry *models.MailerEntry) Operation {
	return mailerEntryOps.Update(mailersName, entry)
}

// NewMailerEntryDelete creates an operation to delete a mailer entry.
func NewMailerEntryDelete(mailersName string, entry *models.MailerEntry) Operation {
	return mailerEntryOps.Delete(mailersName, entry)
}

// PeerEntry factory functions (container children of peer section).

// NewPeerEntryCreate creates an operation to create a peer entry.
func NewPeerEntryCreate(peerSectionName string, entry *models.PeerEntry) Operation {
	return peerEntryOps.Create(peerSectionName, entry)
}

// NewPeerEntryUpdate creates an operation to update a peer entry.
func NewPeerEntryUpdate(peerSectionName string, entry *models.PeerEntry) Operation {
	return peerEntryOps.Update(peerSectionName, entry)
}

// NewPeerEntryDelete creates an operation to delete a peer entry.
func NewPeerEntryDelete(peerSectionName string, entry *models.PeerEntry) Operation {
	return peerEntryOps.Delete(peerSectionName, entry)
}

// Nameserver factory functions (container children of resolver section).

// NewNameserverCreate creates an operation to create a nameserver in a resolver.
func NewNameserverCreate(resolverName string, nameserver *models.Nameserver) Operation {
	return nameserverOps.Create(resolverName, nameserver)
}

// NewNameserverUpdate creates an operation to update a nameserver in a resolver.
func NewNameserverUpdate(resolverName string, nameserver *models.Nameserver) Operation {
	return nameserverOps.Update(resolverName, nameserver)
}

// NewNameserverDelete creates an operation to delete a nameserver from a resolver.
func NewNameserverDelete(resolverName string, nameserver *models.Nameserver) Operation {
	return nameserverOps.Delete(resolverName, nameserver)
}

// Cache factory functions.

// NewCacheCreate creates an operation to create a cache section.
func NewCacheCreate(cache *models.Cache) Operation { return cacheOps.Create(cache) }

// NewCacheUpdate creates an operation to update a cache section.
func NewCacheUpdate(cache *models.Cache) Operation { return cacheOps.Update(cache) }

// NewCacheDelete creates an operation to delete a cache section.
func NewCacheDelete(cache *models.Cache) Operation { return cacheOps.Delete(cache) }

// HTTPErrorsSection factory functions.

// NewHTTPErrorsSectionCreate creates an operation to create an http-errors section.
func NewHTTPErrorsSectionCreate(section *models.HTTPErrorsSection) Operation {
	return httpErrorsOps.Create(section)
}

// NewHTTPErrorsSectionUpdate creates an operation to update an http-errors section.
func NewHTTPErrorsSectionUpdate(section *models.HTTPErrorsSection) Operation {
	return httpErrorsOps.Update(section)
}

// NewHTTPErrorsSectionDelete creates an operation to delete an http-errors section.
func NewHTTPErrorsSectionDelete(section *models.HTTPErrorsSection) Operation {
	return httpErrorsOps.Delete(section)
}

// MailersSection factory functions.

// NewMailersSectionCreate creates an operation to create a mailers section.
func NewMailersSectionCreate(section *models.MailersSection) Operation {
	return mailersOps.Create(section)
}

// NewMailersSectionUpdate creates an operation to update a mailers section.
func NewMailersSectionUpdate(section *models.MailersSection) Operation {
	return mailersOps.Update(section)
}

// NewMailersSectionDelete creates an operation to delete a mailers section.
func NewMailersSectionDelete(section *models.MailersSection) Operation {
	return mailersOps.Delete(section)
}

// PeerSection factory functions.

// NewPeerSectionCreate creates an operation to create a peer section.
func NewPeerSectionCreate(section *models.PeerSection) Operation {
	return peerSectionOps.Create(section)
}

// NewPeerSectionUpdate creates an operation to update a peer section.
// Note: This operation will fail at execution time as the HAProxy Dataplane API
// does not support updating peer sections directly.
func NewPeerSectionUpdate(section *models.PeerSection) Operation {
	return peerSectionOps.Update(section)
}

// NewPeerSectionDelete creates an operation to delete a peer section.
func NewPeerSectionDelete(section *models.PeerSection) Operation {
	return peerSectionOps.Delete(section)
}

// Program factory functions.

// NewProgramCreate creates an operation to create a program section.
func NewProgramCreate(program *models.Program) Operation { return programOps.Create(program) }

// NewProgramUpdate creates an operation to update a program section.
func NewProgramUpdate(program *models.Program) Operation { return programOps.Update(program) }

// NewProgramDelete creates an operation to delete a program section.
func NewProgramDelete(program *models.Program) Operation { return programOps.Delete(program) }

// Resolver factory functions.

// NewResolverCreate creates an operation to create a resolver section.
func NewResolverCreate(resolver *models.Resolver) Operation { return resolverOps.Create(resolver) }

// NewResolverUpdate creates an operation to update a resolver section.
func NewResolverUpdate(resolver *models.Resolver) Operation { return resolverOps.Update(resolver) }

// NewResolverDelete creates an operation to delete a resolver section.
func NewResolverDelete(resolver *models.Resolver) Operation { return resolverOps.Delete(resolver) }

// Ring factory functions.

// NewRingCreate creates an operation to create a ring section.
func NewRingCreate(ring *models.Ring) Operation { return ringOps.Create(ring) }

// NewRingUpdate creates an operation to update a ring section.
func NewRingUpdate(ring *models.Ring) Operation { return ringOps.Update(ring) }

// NewRingDelete creates an operation to delete a ring section.
func NewRingDelete(ring *models.Ring) Operation { return ringOps.Delete(ring) }

// CrtStore factory functions.

// NewCrtStoreCreate creates an operation to create a crt-store section.
func NewCrtStoreCreate(crtStore *models.CrtStore) Operation { return crtStoreOps.Create(crtStore) }

// NewCrtStoreUpdate creates an operation to update a crt-store section.
func NewCrtStoreUpdate(crtStore *models.CrtStore) Operation { return crtStoreOps.Update(crtStore) }

// NewCrtStoreDelete creates an operation to delete a crt-store section.
func NewCrtStoreDelete(crtStore *models.CrtStore) Operation { return crtStoreOps.Delete(crtStore) }

// Userlist factory functions.

// NewUserlistCreate creates an operation to create a userlist section.
func NewUserlistCreate(userlist *models.Userlist) Operation { return userlistOps.Create(userlist) }

// NewUserlistDelete creates an operation to delete a userlist section.
func NewUserlistDelete(userlist *models.Userlist) Operation { return userlistOps.Delete(userlist) }

// FCGIApp factory functions.

// NewFCGIAppCreate creates an operation to create a fcgi-app section.
func NewFCGIAppCreate(fcgiApp *models.FCGIApp) Operation { return fcgiAppOps.Create(fcgiApp) }

// NewFCGIAppUpdate creates an operation to update a fcgi-app section.
func NewFCGIAppUpdate(fcgiApp *models.FCGIApp) Operation { return fcgiAppOps.Update(fcgiApp) }

// NewFCGIAppDelete creates an operation to delete a fcgi-app section.
func NewFCGIAppDelete(fcgiApp *models.FCGIApp) Operation { return fcgiAppOps.Delete(fcgiApp) }

// LogProfile factory functions (HAProxy DataPlane API v3.1+).

// NewLogProfileCreate creates an operation to create a log-profile section.
func NewLogProfileCreate(logProfile *models.LogProfile) Operation {
	return logProfileOps.Create(logProfile)
}

// NewLogProfileUpdate creates an operation to update a log-profile section.
func NewLogProfileUpdate(logProfile *models.LogProfile) Operation {
	return logProfileOps.Update(logProfile)
}

// NewLogProfileDelete creates an operation to delete a log-profile section.
func NewLogProfileDelete(logProfile *models.LogProfile) Operation {
	return logProfileOps.Delete(logProfile)
}

// NewTracesUpdate creates an operation to update the traces section.
// The traces section is a singleton - it can be created or replaced.
// Traces configuration is only available in HAProxy DataPlane API v3.1+.
func NewTracesUpdate(_ *models.Traces) Operation {
	return NewSingletonOp[*models.Traces](
		OperationUpdate,
		"traces",
		func() string { return "Update traces section" },
	)
}

// AcmeProvider factory functions (HAProxy DataPlane API v3.2+).

// NewAcmeProviderCreate creates an operation to create an acme section.
func NewAcmeProviderCreate(acmeProvider *models.AcmeProvider) Operation {
	return acmeProviderOps.Create(acmeProvider)
}

// NewAcmeProviderUpdate creates an operation to update an acme section.
func NewAcmeProviderUpdate(acmeProvider *models.AcmeProvider) Operation {
	return acmeProviderOps.Update(acmeProvider)
}

// NewAcmeProviderDelete creates an operation to delete an acme section.
func NewAcmeProviderDelete(acmeProvider *models.AcmeProvider) Operation {
	return acmeProviderOps.Delete(acmeProvider)
}
