// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package webhook

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

// multiVersionMapper serves several versions of one kind, the way a cluster
// with Gateway API installed serves HTTPRoute as both v1 and v1beta1.
type multiVersionMapper struct {
	meta.RESTMapper
	versions []string
	err      error
}

func (m multiVersionMapper) RESTMappings(gk schema.GroupKind, _ ...string) ([]*meta.RESTMapping, error) {
	if m.err != nil {
		return nil, m.err
	}
	mappings := make([]*meta.RESTMapping, 0, len(m.versions))
	for _, v := range m.versions {
		mappings = append(mappings, &meta.RESTMapping{
			GroupVersionKind: gk.WithVersion(v),
		})
	}
	return mappings, nil
}

// The chart's ValidatingWebhookConfiguration intercepts every candidate
// apiVersion, so the validator table has to cover every version the cluster
// serves. A gap there is not a missing feature: failurePolicy is Fail, so an
// intercepted version with no validator is denied outright.
func TestServedVersions_CoversEveryVersionTheClusterServes(t *testing.T) {
	c := &Component{
		logger:     testutil.NewTestLogger(),
		restMapper: multiVersionMapper{versions: []string{"v1", "v1beta1"}},
	}

	got := c.servedVersions("gateway.networking.k8s.io", "HTTPRoute", "v1")

	assert.Equal(t, []string{"v1", "v1beta1"}, got,
		"a served version the webhook intercepts must get a validator")
}

// The resolved version comes first and is never duplicated, whichever order
// the mapper reports.
func TestServedVersions_ResolvedFirstAndNotDuplicated(t *testing.T) {
	c := &Component{
		logger:     testutil.NewTestLogger(),
		restMapper: multiVersionMapper{versions: []string{"v1beta1", "v1"}},
	}

	got := c.servedVersions("gateway.networking.k8s.io", "HTTPRoute", "v1beta1")

	assert.Equal(t, []string{"v1beta1", "v1"}, got)
	assert.Len(t, got, 2, "the resolved version must appear exactly once")
}

// A mapper that cannot answer degrades to the previous behaviour — the
// resolved version still gets its validator — rather than losing it.
func TestServedVersions_MapperFailureKeepsResolvedVersion(t *testing.T) {
	c := &Component{
		logger:     testutil.NewTestLogger(),
		restMapper: multiVersionMapper{err: errors.New("no mapping")},
	}

	got := c.servedVersions("gateway.networking.k8s.io", "HTTPRoute", "v1")

	assert.Equal(t, []string{"v1"}, got)
}

// A single-version kind registers exactly one validator.
func TestServedVersions_SingleVersionKind(t *testing.T) {
	c := &Component{
		logger:     testutil.NewTestLogger(),
		restMapper: multiVersionMapper{versions: []string{"v1"}},
	}

	got := c.servedVersions("networking.k8s.io", "Ingress", "v1")

	assert.Equal(t, []string{"v1"}, got)
}
