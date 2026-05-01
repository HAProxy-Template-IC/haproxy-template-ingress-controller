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

// Package devassets exposes the dev-environment YAML manifests as embedded
// byte slices so they can be applied programmatically by Go test suites
// (notably tests/e2e) without relying on filesystem layout or external
// scripts.
//
// The same files are still consumed by scripts/start-dev-env.sh via path
// access, so this package is a non-disruptive addition: a single source of
// truth for both the developer's interactive dev loop and the automated
// e2e suite.
//
// What is intentionally not embedded:
//   - ingress-demo.yaml and gateway-demo.yaml — these are demo manifests
//     for the dev loop. The e2e suite uses per-test isolation, building
//     its own Ingress/HTTPRoute resources via Go fixture builders.
//   - haproxy-production.yaml — production values, not a dev-env fixture.
//   - generate-webhook-certs.sh — shell script, not applied to clusters.
package devassets

import _ "embed"

// AuthServerYAML deploys the auth-server fixture used by the SPOA hub
// external-auth plugin.
//
//go:embed auth-server.yaml
var AuthServerYAML []byte

// EchoServerYAML deploys the echo-server fixture, the standard upstream
// for routing tests. Mirrors the inline heredoc in
// scripts/start-dev-env.sh; the script could switch to applying this
// file directly later for a single source of truth.
//
//go:embed echo-server.yaml
var EchoServerYAML []byte

// BlocklistServerYAML deploys the blocklist-server fixture used by the
// HTTP store demo (templates fetch a blocklist over HTTP and gate
// requests by header value).
//
//go:embed blocklist-server.yaml
var BlocklistServerYAML []byte

// HAProxyDemoBackendYAML deploys haproxy-demo-backend, the upstream used
// by Ingress demo manifests for slot-allocation and scaling tests.
//
//go:embed haproxy-demo-backend.yaml
var HAProxyDemoBackendYAML []byte

// HAProxyTestBackendYAML deploys haproxy-test-backend, an additional
// upstream used by selected route tests.
//
//go:embed haproxy-test-backend.yaml
var HAProxyTestBackendYAML []byte

// DevValuesYAML is the helm values file used for the dev install. The
// e2e suite layers its own overrides on top of this (e.g., image tag).
//
//go:embed dev-values.yaml
var DevValuesYAML []byte

// KindConfigYAML is the kind cluster configuration with the
// extraPortMappings the chart's NodePort services expect (30080 HTTP,
// 30443 HTTPS, 30404 stats). The cluster `name:` field is overridden at
// runtime by the kind provider's Create call, so the same config works
// for both the dev cluster (haptic-dev) and the e2e cluster (haptic-e2e).
//
//go:embed kind-config.yaml
var KindConfigYAML []byte
