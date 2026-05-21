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

// Package schemafetcher fetches OpenAPI v3 schemas for each watched
// Kubernetes resource and hands them to pkg/k8s/typegen for
// runtime type generation. It's the I/O side of the typed-watched-
// resources pipeline: pure schema acquisition with no logic about
// how the schemas get used afterwards.
//
// # Where schemas live
//
//   - Built-in resources (Service, Ingress, ConfigMap, EndpointSlice, …):
//     the cluster's aggregated OpenAPI v3 endpoint at /openapi/v3,
//     fronted by client-go's [openapi3.Root].
//
//   - Custom resources (every Gateway-API CRD, BackendTLSPolicy, …):
//     the CRD object itself, at .spec.versions[].schema.openAPIV3Schema.
//     Reading via the apiextensions client is significantly cheaper
//     than the aggregated OpenAPI v3 (per-CRD GET vs. cluster-wide
//     pull) and works even when the CRD lives in a group the
//     aggregated endpoint can't serve.
//
// # Error contract
//
// Schema fetch failures return ErrSchemaNotAvailable wrapping the
// underlying cause (RBAC denial, network error, not-found, …). The
// production bootstrap caller is fail-closed: any per-resource
// ErrSchemaNotAvailable aborts iteration startup with a hard error
// naming the failing resource, so operators see the problem in pod
// status. Template authors using typed access (gw.Spec.X) need the
// guarantee that every declared resource resolved to its real
// schema, so silent degradation to envelope-only typing isn't
// acceptable — see typebootstrap.Bootstrap for the policy.
//
// # Why an interface
//
// The interface exists for testability, not for runtime polymorphism.
// Unit tests stand up a [MapFetcher] with a pre-built schema map; the
// production wiring uses [NewClusterFetcher]. There is no event-driven
// fetcher and no pull-from-disk variant — when those become necessary,
// they slot in as new implementations.
package schemafetcher
