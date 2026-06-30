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
	CacheOps        = NewTopLevelCRUD("cache", "cache", cacheNameFn)
	HTTPErrorsOps   = NewTopLevelCRUD("http_errors", "http-errors section", httpErrorsSectionName)
	MailersOps      = NewTopLevelCRUD("mailers", "mailers", mailersSectionName)
	PeerSectionOps  = NewTopLevelCRUD("peers", "peer section", peerSectionName)
	ResolverOps     = NewTopLevelCRUD("resolver", "resolver", resolverNameFn)
	RingOps         = NewTopLevelCRUD("ring", "ring", ringNameFn)
	CrtStoreOps     = NewTopLevelCRUD("crt_store", "crt-store", crtStoreName)
	UserlistOps     = NewTopLevelCRUD("userlist", "userlist", userlistName)
	FcgiAppOps      = NewTopLevelCRUD("fcgi_app", "fcgi-app", fcgiAppName)
	LogProfileOps   = NewTopLevelCRUD("log_profile", "log-profile", logProfileName)
	AcmeProviderOps = NewTopLevelCRUD("acme_provider", "acme-provider", acmeProviderName)
)

// Container-child CRUD builders.
var (
	UserOps        = NewContainerChildCRUD[*models.User]("user", "user", "userlist", userNameFn)
	MailerEntryOps = NewContainerChildCRUD[*models.MailerEntry]("mailer_entry", "mailer entry", "mailers section", mailerEntryName)
	PeerEntryOps   = NewContainerChildCRUD[*models.PeerEntry]("peer_entry", "peer entry", "peer section", peerEntryName)
	NameserverOps  = NewContainerChildCRUD[*models.Nameserver]("nameserver", "nameserver", "resolvers section", nameserverNameFn)
)

// NewTracesUpdate creates an operation to update the traces section.
// The traces section is a singleton - it can be created or replaced.
// Traces configuration is only available in HAProxy DataPlane API v3.1+.
func NewTracesUpdate(_ *models.Traces) Operation {
	return newOp(
		OperationUpdate,
		"traces",
		func() string { return "Update traces section" },
	)
}
