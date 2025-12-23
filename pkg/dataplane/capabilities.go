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

package dataplane

import "haptic/pkg/dataplane/client"

// Capabilities defines which features are available for a given HAProxy/DataPlane API version.
// This type is re-exported from pkg/dataplane/client for convenience.
type Capabilities = client.Capabilities

// CapabilitiesFromVersion computes capabilities based on a HAProxy version.
// This is used for local HAProxy binary detection (haproxy -v).
//
// Capability thresholds (verified against OpenAPI specs):
//   - SupportsCrtList: v3.2+ (CRT-list storage endpoint)
//   - SupportsMapStorage: v3.0+ (Map file storage endpoint)
//   - SupportsGeneralStorage: v3.0+ (General file storage)
//   - SupportsSslCaFiles: v3.2+ (SSL CA file runtime endpoint)
//   - SupportsSslCrlFiles: v3.2+ (SSL CRL file runtime endpoint)
//   - SupportsLogProfiles: v3.1+ (Log profiles configuration endpoint)
//   - SupportsTraces: v3.1+ (Traces configuration endpoint)
//   - SupportsAcmeProviders: v3.2+ (ACME provider configuration endpoint)
//   - SupportsQUIC: v3.0+ (QUIC/HTTP3 configuration options)
//   - SupportsQUICInitialRules: v3.1+ (QUIC initial rules endpoints)
//   - SupportsHTTP2: v3.0+ (HTTP/2 configuration)
//   - SupportsRuntimeMaps: v3.0+ (Runtime map operations)
//   - SupportsRuntimeServers: v3.0+ (Runtime server operations)
func CapabilitiesFromVersion(v *Version) Capabilities {
	if v == nil {
		return Capabilities{} // All false - safest default
	}

	isV31OrLater := v.Major > 3 || (v.Major == 3 && v.Minor >= 1)
	isV32OrLater := v.Major > 3 || (v.Major == 3 && v.Minor >= 2)

	return Capabilities{
		// Storage capabilities
		SupportsCrtList:        isV32OrLater,
		SupportsMapStorage:     v.Major >= 3,
		SupportsGeneralStorage: v.Major >= 3,
		SupportsSslCaFiles:     isV32OrLater,
		SupportsSslCrlFiles:    isV32OrLater,

		// Configuration capabilities
		SupportsHTTP2:            v.Major >= 3,
		SupportsQUIC:             v.Major >= 3,
		SupportsQUICInitialRules: isV31OrLater,

		// Observability capabilities
		SupportsLogProfiles: isV31OrLater,
		SupportsTraces:      isV31OrLater,

		// Certificate automation capabilities
		SupportsAcmeProviders: isV32OrLater,

		// Runtime capabilities
		SupportsRuntimeMaps:    v.Major >= 3,
		SupportsRuntimeServers: v.Major >= 3,
	}
}
