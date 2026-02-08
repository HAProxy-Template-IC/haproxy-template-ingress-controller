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

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections/executors"
)

// NewUserCreate creates an operation to create a user in a userlist.
func NewUserCreate(userlistName string, user *models.User) Operation {
	return NewContainerChildOp(
		OperationCreate,
		"user",
		PriorityUser,
		userlistName,
		user,
		IdentityUser,
		UserName,
		executors.UserCreate(userlistName),
		DescribeContainerChild(OperationCreate, "user", user.Username, "userlist", userlistName),
	)
}

// NewUserUpdate creates an operation to update a user in a userlist.
func NewUserUpdate(userlistName string, user *models.User) Operation {
	return NewContainerChildOp(
		OperationUpdate,
		"user",
		PriorityUser,
		userlistName,
		user,
		IdentityUser,
		UserName,
		executors.UserUpdate(userlistName),
		DescribeContainerChild(OperationUpdate, "user", user.Username, "userlist", userlistName),
	)
}

// NewUserDelete creates an operation to delete a user from a userlist.
func NewUserDelete(userlistName string, user *models.User) Operation {
	return NewContainerChildOp(
		OperationDelete,
		"user",
		PriorityUser,
		userlistName,
		user,
		NilUser,
		UserName,
		executors.UserDelete(userlistName),
		DescribeContainerChild(OperationDelete, "user", user.Username, "userlist", userlistName),
	)
}

// NewMailerEntryCreate creates an operation to create a mailer entry.
func NewMailerEntryCreate(mailersName string, entry *models.MailerEntry) Operation {
	return NewContainerChildOp(
		OperationCreate,
		"mailer_entry",
		PriorityMailerEntry,
		mailersName,
		entry,
		IdentityMailerEntry,
		MailerEntryName,
		executors.MailerEntryCreate(mailersName),
		DescribeContainerChild(OperationCreate, "mailer entry", entry.Name, "mailers section", mailersName),
	)
}

// NewMailerEntryUpdate creates an operation to update a mailer entry.
func NewMailerEntryUpdate(mailersName string, entry *models.MailerEntry) Operation {
	return NewContainerChildOp(
		OperationUpdate,
		"mailer_entry",
		PriorityMailerEntry,
		mailersName,
		entry,
		IdentityMailerEntry,
		MailerEntryName,
		executors.MailerEntryUpdate(mailersName),
		DescribeContainerChild(OperationUpdate, "mailer entry", entry.Name, "mailers section", mailersName),
	)
}

// NewMailerEntryDelete creates an operation to delete a mailer entry.
func NewMailerEntryDelete(mailersName string, entry *models.MailerEntry) Operation {
	return NewContainerChildOp(
		OperationDelete,
		"mailer_entry",
		PriorityMailerEntry,
		mailersName,
		entry,
		NilMailerEntry,
		MailerEntryName,
		executors.MailerEntryDelete(mailersName),
		DescribeContainerChild(OperationDelete, "mailer entry", entry.Name, "mailers section", mailersName),
	)
}

// NewPeerEntryCreate creates an operation to create a peer entry.
func NewPeerEntryCreate(peerSectionName string, entry *models.PeerEntry) Operation {
	return NewContainerChildOp(
		OperationCreate,
		"peer_entry",
		PriorityPeerEntry,
		peerSectionName,
		entry,
		IdentityPeerEntry,
		PeerEntryName,
		executors.PeerEntryCreate(peerSectionName),
		DescribeContainerChild(OperationCreate, "peer entry", entry.Name, "peer section", peerSectionName),
	)
}

// NewPeerEntryUpdate creates an operation to update a peer entry.
func NewPeerEntryUpdate(peerSectionName string, entry *models.PeerEntry) Operation {
	return NewContainerChildOp(
		OperationUpdate,
		"peer_entry",
		PriorityPeerEntry,
		peerSectionName,
		entry,
		IdentityPeerEntry,
		PeerEntryName,
		executors.PeerEntryUpdate(peerSectionName),
		DescribeContainerChild(OperationUpdate, "peer entry", entry.Name, "peer section", peerSectionName),
	)
}

// NewPeerEntryDelete creates an operation to delete a peer entry.
func NewPeerEntryDelete(peerSectionName string, entry *models.PeerEntry) Operation {
	return NewContainerChildOp(
		OperationDelete,
		"peer_entry",
		PriorityPeerEntry,
		peerSectionName,
		entry,
		NilPeerEntry,
		PeerEntryName,
		executors.PeerEntryDelete(peerSectionName),
		DescribeContainerChild(OperationDelete, "peer entry", entry.Name, "peer section", peerSectionName),
	)
}

// NewNameserverCreate creates an operation to create a nameserver in a resolver.
func NewNameserverCreate(resolverName string, nameserver *models.Nameserver) Operation {
	return NewContainerChildOp(
		OperationCreate,
		"nameserver",
		PriorityNameserver,
		resolverName,
		nameserver,
		IdentityNameserver,
		NameserverName,
		executors.NameserverCreate(resolverName),
		DescribeContainerChild(OperationCreate, "nameserver", nameserver.Name, "resolvers section", resolverName),
	)
}

// NewNameserverUpdate creates an operation to update a nameserver in a resolver.
func NewNameserverUpdate(resolverName string, nameserver *models.Nameserver) Operation {
	return NewContainerChildOp(
		OperationUpdate,
		"nameserver",
		PriorityNameserver,
		resolverName,
		nameserver,
		IdentityNameserver,
		NameserverName,
		executors.NameserverUpdate(resolverName),
		DescribeContainerChild(OperationUpdate, "nameserver", nameserver.Name, "resolvers section", resolverName),
	)
}

// NewNameserverDelete creates an operation to delete a nameserver from a resolver.
func NewNameserverDelete(resolverName string, nameserver *models.Nameserver) Operation {
	return NewContainerChildOp(
		OperationDelete,
		"nameserver",
		PriorityNameserver,
		resolverName,
		nameserver,
		NilNameserver,
		NameserverName,
		executors.NameserverDelete(resolverName),
		DescribeContainerChild(OperationDelete, "nameserver", nameserver.Name, "resolvers section", resolverName),
	)
}

// NewCacheCreate creates an operation to create a cache section.
func NewCacheCreate(cache *models.Cache) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"cache",
		PriorityCache,
		cache,
		IdentityCache,
		CacheName,
		executors.CacheCreate(),
		DescribeTopLevel(OperationCreate, "cache", CacheName(cache)),
	)
}

// NewCacheUpdate creates an operation to update a cache section.
func NewCacheUpdate(cache *models.Cache) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"cache",
		PriorityCache,
		cache,
		IdentityCache,
		CacheName,
		executors.CacheUpdate(),
		DescribeTopLevel(OperationUpdate, "cache", CacheName(cache)),
	)
}

// NewCacheDelete creates an operation to delete a cache section.
func NewCacheDelete(cache *models.Cache) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"cache",
		PriorityCache,
		cache,
		NilCache,
		CacheName,
		executors.CacheDelete(),
		DescribeTopLevel(OperationDelete, "cache", CacheName(cache)),
	)
}

// NewHTTPErrorsSectionCreate creates an operation to create an http-errors section.
func NewHTTPErrorsSectionCreate(section *models.HTTPErrorsSection) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"http_errors",
		PriorityHTTPErrors,
		section,
		IdentityHTTPErrorsSection,
		HTTPErrorsSectionName,
		executors.HTTPErrorsSectionCreate(),
		DescribeTopLevel(OperationCreate, "http-errors section", section.Name),
	)
}

// NewHTTPErrorsSectionUpdate creates an operation to update an http-errors section.
func NewHTTPErrorsSectionUpdate(section *models.HTTPErrorsSection) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"http_errors",
		PriorityHTTPErrors,
		section,
		IdentityHTTPErrorsSection,
		HTTPErrorsSectionName,
		executors.HTTPErrorsSectionUpdate(),
		DescribeTopLevel(OperationUpdate, "http-errors section", section.Name),
	)
}

// NewHTTPErrorsSectionDelete creates an operation to delete an http-errors section.
func NewHTTPErrorsSectionDelete(section *models.HTTPErrorsSection) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"http_errors",
		PriorityHTTPErrors,
		section,
		NilHTTPErrorsSection,
		HTTPErrorsSectionName,
		executors.HTTPErrorsSectionDelete(),
		DescribeTopLevel(OperationDelete, "http-errors section", section.Name),
	)
}

// NewMailersSectionCreate creates an operation to create a mailers section.
func NewMailersSectionCreate(section *models.MailersSection) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"mailers",
		PriorityMailers,
		section,
		IdentityMailersSection,
		MailersSectionName,
		executors.MailersSectionCreate(),
		DescribeTopLevel(OperationCreate, "mailers", section.Name),
	)
}

// NewMailersSectionUpdate creates an operation to update a mailers section.
func NewMailersSectionUpdate(section *models.MailersSection) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"mailers",
		PriorityMailers,
		section,
		IdentityMailersSection,
		MailersSectionName,
		executors.MailersSectionUpdate(),
		DescribeTopLevel(OperationUpdate, "mailers", section.Name),
	)
}

// NewMailersSectionDelete creates an operation to delete a mailers section.
func NewMailersSectionDelete(section *models.MailersSection) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"mailers",
		PriorityMailers,
		section,
		NilMailersSection,
		MailersSectionName,
		executors.MailersSectionDelete(),
		DescribeTopLevel(OperationDelete, "mailers", section.Name),
	)
}

// PeerSection Factory Functions
// Note: Update operations return an error as the API doesn't support direct updates.

// NewPeerSectionCreate creates an operation to create a peer section.
func NewPeerSectionCreate(section *models.PeerSection) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"peers",
		PriorityPeer,
		section,
		IdentityPeerSection,
		PeerSectionName,
		executors.PeerSectionCreate(),
		DescribeTopLevel(OperationCreate, "peer section", section.Name),
	)
}

// NewPeerSectionUpdate creates an operation to update a peer section.
// Note: This operation will fail at execution time as the HAProxy Dataplane API
// does not support updating peer sections directly.
func NewPeerSectionUpdate(section *models.PeerSection) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"peers",
		PriorityPeer,
		section,
		IdentityPeerSection,
		PeerSectionName,
		executors.PeerSectionUpdate(),
		DescribeTopLevel(OperationUpdate, "peer section", section.Name),
	)
}

// NewPeerSectionDelete creates an operation to delete a peer section.
func NewPeerSectionDelete(section *models.PeerSection) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"peers",
		PriorityPeer,
		section,
		NilPeerSection,
		PeerSectionName,
		executors.PeerSectionDelete(),
		DescribeTopLevel(OperationDelete, "peer section", section.Name),
	)
}

// NewProgramCreate creates an operation to create a program section.
func NewProgramCreate(program *models.Program) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"program",
		PriorityProgram,
		program,
		IdentityProgram,
		ProgramName,
		executors.ProgramCreate(),
		DescribeTopLevel(OperationCreate, "program", program.Name),
	)
}

// NewProgramUpdate creates an operation to update a program section.
func NewProgramUpdate(program *models.Program) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"program",
		PriorityProgram,
		program,
		IdentityProgram,
		ProgramName,
		executors.ProgramUpdate(),
		DescribeTopLevel(OperationUpdate, "program", program.Name),
	)
}

// NewProgramDelete creates an operation to delete a program section.
func NewProgramDelete(program *models.Program) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"program",
		PriorityProgram,
		program,
		NilProgram,
		ProgramName,
		executors.ProgramDelete(),
		DescribeTopLevel(OperationDelete, "program", program.Name),
	)
}

// NewResolverCreate creates an operation to create a resolver section.
func NewResolverCreate(resolver *models.Resolver) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"resolver",
		PriorityResolver,
		resolver,
		IdentityResolver,
		ResolverName,
		executors.ResolverCreate(),
		DescribeTopLevel(OperationCreate, "resolver", resolver.Name),
	)
}

// NewResolverUpdate creates an operation to update a resolver section.
func NewResolverUpdate(resolver *models.Resolver) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"resolver",
		PriorityResolver,
		resolver,
		IdentityResolver,
		ResolverName,
		executors.ResolverUpdate(),
		DescribeTopLevel(OperationUpdate, "resolver", resolver.Name),
	)
}

// NewResolverDelete creates an operation to delete a resolver section.
func NewResolverDelete(resolver *models.Resolver) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"resolver",
		PriorityResolver,
		resolver,
		NilResolver,
		ResolverName,
		executors.ResolverDelete(),
		DescribeTopLevel(OperationDelete, "resolver", resolver.Name),
	)
}

// NewRingCreate creates an operation to create a ring section.
func NewRingCreate(ring *models.Ring) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"ring",
		PriorityRing,
		ring,
		IdentityRing,
		RingName,
		executors.RingCreate(),
		DescribeTopLevel(OperationCreate, "ring", ring.Name),
	)
}

// NewRingUpdate creates an operation to update a ring section.
func NewRingUpdate(ring *models.Ring) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"ring",
		PriorityRing,
		ring,
		IdentityRing,
		RingName,
		executors.RingUpdate(),
		DescribeTopLevel(OperationUpdate, "ring", ring.Name),
	)
}

// NewRingDelete creates an operation to delete a ring section.
func NewRingDelete(ring *models.Ring) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"ring",
		PriorityRing,
		ring,
		NilRing,
		RingName,
		executors.RingDelete(),
		DescribeTopLevel(OperationDelete, "ring", ring.Name),
	)
}

// NewCrtStoreCreate creates an operation to create a crt-store section.
func NewCrtStoreCreate(crtStore *models.CrtStore) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"crt_store",
		PriorityCrtStore,
		crtStore,
		IdentityCrtStore,
		CrtStoreName,
		executors.CrtStoreCreate(),
		DescribeTopLevel(OperationCreate, "crt-store", crtStore.Name),
	)
}

// NewCrtStoreUpdate creates an operation to update a crt-store section.
func NewCrtStoreUpdate(crtStore *models.CrtStore) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"crt_store",
		PriorityCrtStore,
		crtStore,
		IdentityCrtStore,
		CrtStoreName,
		executors.CrtStoreUpdate(),
		DescribeTopLevel(OperationUpdate, "crt-store", crtStore.Name),
	)
}

// NewCrtStoreDelete creates an operation to delete a crt-store section.
func NewCrtStoreDelete(crtStore *models.CrtStore) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"crt_store",
		PriorityCrtStore,
		crtStore,
		NilCrtStore,
		CrtStoreName,
		executors.CrtStoreDelete(),
		DescribeTopLevel(OperationDelete, "crt-store", crtStore.Name),
	)
}

// NewUserlistCreate creates an operation to create a userlist section.
func NewUserlistCreate(userlist *models.Userlist) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"userlist",
		PriorityUserlist,
		userlist,
		IdentityUserlist,
		UserlistName,
		executors.UserlistCreate(),
		DescribeTopLevel(OperationCreate, "userlist", userlist.Name),
	)
}

// NewUserlistDelete creates an operation to delete a userlist section.
func NewUserlistDelete(userlist *models.Userlist) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"userlist",
		PriorityUserlist,
		userlist,
		NilUserlist,
		UserlistName,
		executors.UserlistDelete(),
		DescribeTopLevel(OperationDelete, "userlist", userlist.Name),
	)
}

// NewFCGIAppCreate creates an operation to create a fcgi-app section.
func NewFCGIAppCreate(fcgiApp *models.FCGIApp) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"fcgi_app",
		PriorityFCGIApp,
		fcgiApp,
		IdentityFCGIApp,
		FCGIAppName,
		executors.FCGIAppCreate(),
		DescribeTopLevel(OperationCreate, "fcgi-app", fcgiApp.Name),
	)
}

// NewFCGIAppUpdate creates an operation to update a fcgi-app section.
func NewFCGIAppUpdate(fcgiApp *models.FCGIApp) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"fcgi_app",
		PriorityFCGIApp,
		fcgiApp,
		IdentityFCGIApp,
		FCGIAppName,
		executors.FCGIAppUpdate(),
		DescribeTopLevel(OperationUpdate, "fcgi-app", fcgiApp.Name),
	)
}

// NewFCGIAppDelete creates an operation to delete a fcgi-app section.
func NewFCGIAppDelete(fcgiApp *models.FCGIApp) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"fcgi_app",
		PriorityFCGIApp,
		fcgiApp,
		NilFCGIApp,
		FCGIAppName,
		executors.FCGIAppDelete(),
		DescribeTopLevel(OperationDelete, "fcgi-app", fcgiApp.Name),
	)
}

// NewLogProfileCreate creates an operation to create a log-profile section.
// Log profiles are only available in HAProxy DataPlane API v3.1+.
func NewLogProfileCreate(logProfile *models.LogProfile) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"log_profile",
		PriorityLogProfile,
		logProfile,
		IdentityLogProfile,
		LogProfileName,
		executors.LogProfileCreate(),
		DescribeTopLevel(OperationCreate, "log-profile", logProfile.Name),
	)
}

// NewLogProfileUpdate creates an operation to update a log-profile section.
// Log profiles are only available in HAProxy DataPlane API v3.1+.
func NewLogProfileUpdate(logProfile *models.LogProfile) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"log_profile",
		PriorityLogProfile,
		logProfile,
		IdentityLogProfile,
		LogProfileName,
		executors.LogProfileUpdate(),
		DescribeTopLevel(OperationUpdate, "log-profile", logProfile.Name),
	)
}

// NewLogProfileDelete creates an operation to delete a log-profile section.
// Log profiles are only available in HAProxy DataPlane API v3.1+.
func NewLogProfileDelete(logProfile *models.LogProfile) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"log_profile",
		PriorityLogProfile,
		logProfile,
		NilLogProfile,
		LogProfileName,
		executors.LogProfileDelete(),
		DescribeTopLevel(OperationDelete, "log-profile", logProfile.Name),
	)
}

// NewTracesUpdate creates an operation to update the traces section.
// The traces section is a singleton - it can be created or replaced.
// Traces configuration is only available in HAProxy DataPlane API v3.1+.
func NewTracesUpdate(traces *models.Traces) Operation {
	return NewSingletonOp(
		OperationUpdate,
		"traces",
		PriorityTraces,
		traces,
		IdentityTraces,
		executors.TracesUpdate(),
		func() string { return "Update traces section" },
	)
}

// NewAcmeProviderCreate creates an operation to create an acme section.
// ACME providers are only available in HAProxy DataPlane API v3.2+.
func NewAcmeProviderCreate(acmeProvider *models.AcmeProvider) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"acme_provider",
		PriorityAcmeProvider,
		acmeProvider,
		IdentityAcmeProvider,
		AcmeProviderName,
		executors.AcmeProviderCreate(),
		DescribeTopLevel(OperationCreate, "acme-provider", acmeProvider.Name),
	)
}

// NewAcmeProviderUpdate creates an operation to update an acme section.
// ACME providers are only available in HAProxy DataPlane API v3.2+.
func NewAcmeProviderUpdate(acmeProvider *models.AcmeProvider) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"acme_provider",
		PriorityAcmeProvider,
		acmeProvider,
		IdentityAcmeProvider,
		AcmeProviderName,
		executors.AcmeProviderUpdate(),
		DescribeTopLevel(OperationUpdate, "acme-provider", acmeProvider.Name),
	)
}

// NewAcmeProviderDelete creates an operation to delete an acme section.
// ACME providers are only available in HAProxy DataPlane API v3.2+.
func NewAcmeProviderDelete(acmeProvider *models.AcmeProvider) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"acme_provider",
		PriorityAcmeProvider,
		acmeProvider,
		NilAcmeProvider,
		AcmeProviderName,
		executors.AcmeProviderDelete(),
		DescribeTopLevel(OperationDelete, "acme-provider", acmeProvider.Name),
	)
}
