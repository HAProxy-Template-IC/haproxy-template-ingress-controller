// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package dryrunvalidator

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// multiVersionRESTMapper serves one kind under two versions, the way a cluster
// with Gateway API installed serves HTTPRoute as both v1 and v1beta1.
func multiVersionRESTMapper() meta.RESTMapper {
	m := meta.NewDefaultRESTMapper(nil)
	for _, version := range []string{"v1", "v1beta1"} {
		m.AddSpecific(
			schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: version, Kind: "HTTPRoute"},
			schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: version, Resource: "httproutes"},
			schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: version, Resource: "httproute"},
			meta.RESTScopeNamespace,
		)
	}
	return m
}

func httpRouteComponent(t *testing.T) *Component {
	t.Helper()
	aliases, err := buildResourceAliases(map[string]config.WatchedResource{
		"httproutes": {APIVersion: "gateway.networking.k8s.io/v1", Resources: "httproutes"},
	})
	require.NoError(t, err)
	return &Component{
		logger:       slog.Default(),
		restMapper:   multiVersionRESTMapper(),
		aliasesByGVR: aliases,
	}
}

// An object written in a served-but-not-configured apiVersion must map to the
// same watched resource as the configured one.
//
// The chart renders the webhook's rules from the full apiVersions candidate
// list while the effective config keeps only the resolved version, so the
// webhook is handed versions the alias map was never keyed for. With
// failurePolicy: Fail an unmapped version is denied outright — a v1beta1
// HTTPRoute was permanently rejected on a default install while the identical
// v1 object was admitted.
func TestMapGVKToResourceAliases_ResolvesUnconfiguredServedVersion(t *testing.T) {
	c := httpRouteComponent(t)

	aliases, err := c.mapGVKToResourceAliases("gateway.networking.k8s.io/v1beta1.HTTPRoute")

	require.NoError(t, err, "a served version the webhook intercepts must resolve, not deny")
	require.Len(t, aliases, 1)
	assert.Equal(t, "httproutes", aliases[0].name)
}

// The configured version keeps resolving exactly as before — the fallback is
// additive, never a replacement for the direct hit.
func TestMapGVKToResourceAliases_ConfiguredVersionUnchanged(t *testing.T) {
	c := httpRouteComponent(t)

	aliases, err := c.mapGVKToResourceAliases("gateway.networking.k8s.io/v1.HTTPRoute")

	require.NoError(t, err)
	require.Len(t, aliases, 1)
	assert.Equal(t, "httproutes", aliases[0].name)
}

// Matching stays scoped to group AND plural: an unwatched resource is still
// refused, so the fallback cannot turn a denial into a blanket admit.
func TestMapGVKToResourceAliases_UnwatchedResourceStillRefused(t *testing.T) {
	c := httpRouteComponent(t)
	c.restMapper.(*meta.DefaultRESTMapper).AddSpecific(
		schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "TCPRoute"},
		schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "tcproutes"},
		schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "tcproute"},
		meta.RESTScopeNamespace,
	)

	_, err := c.mapGVKToResourceAliases("gateway.networking.k8s.io/v1.TCPRoute")

	require.Error(t, err, "a resource nobody watches must not resolve through the version fallback")
	assert.Contains(t, err.Error(), "unconfigured resource")
}

// Two watched-resource entries may name the same group+plural under different
// apiVersions. Both watch the admitted object, so both alias sets must come
// back — returning whichever the map happened to yield first would silently drop
// one overlay, and pick which one at random.
func TestMapGVKToResourceAliases_UnionsAliasesAcrossVersions(t *testing.T) {
	aliases, err := buildResourceAliases(map[string]config.WatchedResource{
		"routes_v1":      {APIVersion: "gateway.networking.k8s.io/v1", Resources: "httproutes"},
		"routes_v1beta1": {APIVersion: "gateway.networking.k8s.io/v1beta1", Resources: "httproutes"},
	})
	require.NoError(t, err)
	require.Len(t, aliases, 2, "the two entries must occupy distinct GVR keys")

	c := &Component{
		logger:       slog.Default(),
		restMapper:   multiVersionRESTMapper(),
		aliasesByGVR: aliases,
	}

	// A version that IS configured takes the exact-GVR path and sees only its own.
	exact, err := c.mapGVKToResourceAliases("gateway.networking.k8s.io/v1.HTTPRoute")
	require.NoError(t, err)
	assert.Equal(t, []string{"routes_v1"}, aliasNames(exact))

	// An unconfigured-but-served version falls back, and must see BOTH, in a
	// stable order rather than whichever the map iteration yielded.
	c2 := &Component{
		logger:     slog.Default(),
		restMapper: multiVersionRESTMapper(),
		aliasesByGVR: map[schema.GroupVersionResource][]resourceAlias{
			{Group: "gateway.networking.k8s.io", Version: "v1alpha2", Resource: "httproutes"}: aliases[schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}],
			{Group: "gateway.networking.k8s.io", Version: "v1alpha3", Resource: "httproutes"}: aliases[schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1beta1", Resource: "httproutes"}],
		},
	}
	for range 10 {
		got, err := c2.mapGVKToResourceAliases("gateway.networking.k8s.io/v1.HTTPRoute")
		require.NoError(t, err)
		assert.Equal(t, []string{"routes_v1", "routes_v1beta1"}, aliasNames(got),
			"every alias watching this group+plural must be returned, in a stable order")
	}
}

func aliasNames(aliases []resourceAlias) []string {
	names := make([]string, 0, len(aliases))
	for _, a := range aliases {
		names = append(names, a.name)
	}
	return names
}
