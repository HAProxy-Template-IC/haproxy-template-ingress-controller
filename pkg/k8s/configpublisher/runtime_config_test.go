// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package configpublisher

import (
	"testing"

	"github.com/stretchr/testify/assert"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
)

func TestBuildAuxiliaryFileReferences(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		result    *PublishResult
		want      *haproxyv1alpha1.AuxiliaryFileReferences
	}{
		{
			name:      "empty result yields all-nil reference slices (omitempty preserved)",
			namespace: "haptic",
			result:    &PublishResult{},
			want: &haproxyv1alpha1.AuxiliaryFileReferences{
				MapFiles:        nil,
				SSLCertificates: nil,
				GeneralFiles:    nil,
				CRTListFiles:    nil,
			},
		},
		{
			name:      "map file names produce HAProxyMapFile refs in the requested namespace",
			namespace: "haptic",
			result: &PublishResult{
				MapFileNames: []string{"hosts.map", "paths.map"},
			},
			want: &haproxyv1alpha1.AuxiliaryFileReferences{
				MapFiles: []haproxyv1alpha1.ResourceReference{
					{Kind: "HAProxyMapFile", Name: "hosts.map", Namespace: "haptic"},
					{Kind: "HAProxyMapFile", Name: "paths.map", Namespace: "haptic"},
				},
			},
		},
		{
			name:      "secret names produce Secret refs (SSL certificates)",
			namespace: "haptic",
			result: &PublishResult{
				SecretNames: []string{"tls-cert"},
			},
			want: &haproxyv1alpha1.AuxiliaryFileReferences{
				SSLCertificates: []haproxyv1alpha1.ResourceReference{
					{Kind: "Secret", Name: "tls-cert", Namespace: "haptic"},
				},
			},
		},
		{
			name:      "general file names produce HAProxyGeneralFile refs",
			namespace: "haptic",
			result: &PublishResult{
				GeneralFileNames: []string{"500.http"},
			},
			want: &haproxyv1alpha1.AuxiliaryFileReferences{
				GeneralFiles: []haproxyv1alpha1.ResourceReference{
					{Kind: "HAProxyGeneralFile", Name: "500.http", Namespace: "haptic"},
				},
			},
		},
		{
			name:      "crt-list file names produce HAProxyCRTListFile refs",
			namespace: "haptic",
			result: &PublishResult{
				CRTListFileNames: []string{"crt-list.txt"},
			},
			want: &haproxyv1alpha1.AuxiliaryFileReferences{
				CRTListFiles: []haproxyv1alpha1.ResourceReference{
					{Kind: "HAProxyCRTListFile", Name: "crt-list.txt", Namespace: "haptic"},
				},
			},
		},
		{
			name:      "all categories together preserve insertion order",
			namespace: "edge",
			result: &PublishResult{
				MapFileNames:     []string{"a.map", "b.map"},
				SecretNames:      []string{"s1"},
				GeneralFileNames: []string{"errors.http"},
				CRTListFileNames: []string{"crt-list"},
			},
			want: &haproxyv1alpha1.AuxiliaryFileReferences{
				MapFiles: []haproxyv1alpha1.ResourceReference{
					{Kind: "HAProxyMapFile", Name: "a.map", Namespace: "edge"},
					{Kind: "HAProxyMapFile", Name: "b.map", Namespace: "edge"},
				},
				SSLCertificates: []haproxyv1alpha1.ResourceReference{
					{Kind: "Secret", Name: "s1", Namespace: "edge"},
				},
				GeneralFiles: []haproxyv1alpha1.ResourceReference{
					{Kind: "HAProxyGeneralFile", Name: "errors.http", Namespace: "edge"},
				},
				CRTListFiles: []haproxyv1alpha1.ResourceReference{
					{Kind: "HAProxyCRTListFile", Name: "crt-list", Namespace: "edge"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildAuxiliaryFileReferences(tt.namespace, tt.result)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAuxiliaryRefsEqual(t *testing.T) {
	mapFile := func(name string) haproxyv1alpha1.ResourceReference {
		return haproxyv1alpha1.ResourceReference{Kind: "HAProxyMapFile", Name: name, Namespace: "haptic"}
	}
	secret := func(name string) haproxyv1alpha1.ResourceReference {
		return haproxyv1alpha1.ResourceReference{Kind: "Secret", Name: name, Namespace: "haptic"}
	}

	tests := []struct {
		name string
		a    *haproxyv1alpha1.AuxiliaryFileReferences
		b    *haproxyv1alpha1.AuxiliaryFileReferences
		want bool
	}{
		{
			name: "both nil are equal",
			a:    nil,
			b:    nil,
			want: true,
		},
		{
			name: "nil vs empty struct are NOT equal (nil semantics matter)",
			a:    nil,
			b:    &haproxyv1alpha1.AuxiliaryFileReferences{},
			want: false,
		},
		{
			name: "two empty structs are equal",
			a:    &haproxyv1alpha1.AuxiliaryFileReferences{},
			b:    &haproxyv1alpha1.AuxiliaryFileReferences{},
			want: true,
		},
		{
			name: "identical refs are equal",
			a: &haproxyv1alpha1.AuxiliaryFileReferences{
				MapFiles:        []haproxyv1alpha1.ResourceReference{mapFile("hosts.map")},
				SSLCertificates: []haproxyv1alpha1.ResourceReference{secret("tls")},
			},
			b: &haproxyv1alpha1.AuxiliaryFileReferences{
				MapFiles:        []haproxyv1alpha1.ResourceReference{mapFile("hosts.map")},
				SSLCertificates: []haproxyv1alpha1.ResourceReference{secret("tls")},
			},
			want: true,
		},
		{
			name: "differing map file name detected",
			a: &haproxyv1alpha1.AuxiliaryFileReferences{
				MapFiles: []haproxyv1alpha1.ResourceReference{mapFile("hosts.map")},
			},
			b: &haproxyv1alpha1.AuxiliaryFileReferences{
				MapFiles: []haproxyv1alpha1.ResourceReference{mapFile("paths.map")},
			},
			want: false,
		},
		{
			name: "different ordering of map files is NOT equal (slices.Equal is order-sensitive)",
			a: &haproxyv1alpha1.AuxiliaryFileReferences{
				MapFiles: []haproxyv1alpha1.ResourceReference{mapFile("a.map"), mapFile("b.map")},
			},
			b: &haproxyv1alpha1.AuxiliaryFileReferences{
				MapFiles: []haproxyv1alpha1.ResourceReference{mapFile("b.map"), mapFile("a.map")},
			},
			want: false,
		},
		{
			name: "extra category in b detected",
			a: &haproxyv1alpha1.AuxiliaryFileReferences{
				MapFiles: []haproxyv1alpha1.ResourceReference{mapFile("hosts.map")},
			},
			b: &haproxyv1alpha1.AuxiliaryFileReferences{
				MapFiles:        []haproxyv1alpha1.ResourceReference{mapFile("hosts.map")},
				SSLCertificates: []haproxyv1alpha1.ResourceReference{secret("tls")},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := auxiliaryRefsEqual(tt.a, tt.b)
			assert.Equal(t, tt.want, got)
			// Symmetry: equality must not depend on argument order
			assert.Equal(t, tt.want, auxiliaryRefsEqual(tt.b, tt.a), "auxiliaryRefsEqual must be symmetric")
		})
	}
}
