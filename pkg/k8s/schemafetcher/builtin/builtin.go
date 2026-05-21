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

// Package builtin ships a small set of OpenAPI v3 schemas embedded into the
// controller binary, for use in environments where no API server is reachable
// — primarily `haptic-controller validate` running against a CRD file on
// disk. The schemas are trimmed copies of the upstream definitions (Gateway
// API, core types) covering just the subtree the chart's bundled libraries
// touch, so typed access in templates compiles offline against the same
// shape the production OpenAPI path produces.
//
// # Why hand-trimmed embedded schemas
//
// The alternatives were considered and rejected:
//
//   - Reflecting Go types from k8s.io/api / sigs.k8s.io/gateway-api into
//     spec.Schema: would auto-stay-in-sync but pulls every API package into
//     the controller binary and changes shape with every Go-side rename.
//     The schemas this file embeds are stable across many API minor versions.
//
//   - Embedding upstream OpenAPI JSON verbatim: each Gateway API release
//     ships ~3000 lines of schema. Verbatim copies turn this directory
//     into a noise generator that grows by megabytes every quarter.
//
//   - Generating types from the user's offline test fixtures: data-driven
//     types diverge from the cluster's actual schema, so templates that
//     compile against the offline shape may not compile in production.
//
// The hand-trimmed approach matches what `pkg/k8s/typegen/realschema_test.go`
// already did for testing — we lift the same idea into a production code
// path so the validate CLI can offer chart authors typed access without
// requiring a live cluster.
//
// # Adding a new schema
//
// Drop a new JSON file in this directory named
// `<group>-<version>-<Kind>.json` (use `core` for the empty group; `/`
// replaced with `-`). The embed directive picks it up automatically. The
// JSON must be a kube-openapi v3 schema (the shape under
// `openAPIV3Schema` on a CRD). Keep the schema trimmed to the fields the
// chart's bundled libraries reference — every extra field is one more
// thing to maintain when the upstream type evolves.
package builtin

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kube-openapi/pkg/validation/spec"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/schemafetcher"
)

//go:embed *.json
var schemasFS embed.FS

// NewFetcher returns a [schemafetcher.MapFetcher] populated with every
// embedded schema in this package. The returned fetcher is safe for
// concurrent use and never reaches the network — it's the
// production schemafetcher-compatible drop-in for offline modes.
//
// An error here means the build has a corrupt embedded JSON file
// (which would be caught by the package test); callers can treat it
// as a programming error and fail-loud rather than fail-open.
func NewFetcher() (*schemafetcher.MapFetcher, error) {
	entries, err := schemasFS.ReadDir(".")
	if err != nil {
		return nil, fmt.Errorf("listing embedded schemas: %w", err)
	}
	seed := make(map[schema.GroupVersionKind]*spec.Schema, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		gvk, err := parseFilename(e.Name())
		if err != nil {
			return nil, fmt.Errorf("schema %s: %w", e.Name(), err)
		}
		raw, err := schemasFS.ReadFile(e.Name())
		if err != nil {
			return nil, fmt.Errorf("reading embedded schema %s: %w", e.Name(), err)
		}
		var sch spec.Schema
		if err := json.Unmarshal(raw, &sch); err != nil {
			return nil, fmt.Errorf("parsing embedded schema %s: %w", e.Name(), err)
		}
		seed[gvk] = &sch
	}
	return schemafetcher.NewMapFetcher(seed), nil
}

// parseFilename turns `<group>-<version>-<Kind>.json` back into a GVK.
// Uses `core` for the empty group (the canonical wire form `""` doesn't
// survive a filename), and replaces `-` in the group with `.` (Gateway
// API's group is `gateway.networking.k8s.io`; on disk it lives as
// `gateway-networking-k8s-io-v1-Gateway.json`).
//
// The format is intentionally crude — the trade-off is readability of
// the directory listing vs. a more bullet-proof manifest. Since this
// directory is small and reviewers see filenames directly in PRs, the
// crude form wins.
func parseFilename(name string) (schema.GroupVersionKind, error) {
	base := strings.TrimSuffix(name, ".json")
	// Find the LAST hyphen — everything after is the Kind. Then the
	// hyphen before that bounds the version. Whatever's left is the
	// group, with hyphens turned back into dots.
	lastDash := strings.LastIndexByte(base, '-')
	if lastDash <= 0 {
		return schema.GroupVersionKind{}, fmt.Errorf("malformed filename %q (no '-' before Kind)", name)
	}
	kind := base[lastDash+1:]
	rest := base[:lastDash]
	versionDash := strings.LastIndexByte(rest, '-')
	if versionDash < 0 {
		return schema.GroupVersionKind{}, fmt.Errorf("malformed filename %q (no '-' before version)", name)
	}
	version := rest[versionDash+1:]
	groupRaw := rest[:versionDash]
	group := strings.ReplaceAll(groupRaw, "-", ".")
	if group == "core" {
		group = "" // canonical wire form for the core API group
	}
	return schema.GroupVersionKind{Group: group, Version: version, Kind: kind}, nil
}
