// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package configpublisher

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// buildRuntimeConfig is the pure builder Publisher.createOrUpdateRuntimeConfig
// uses to construct the HAProxyCfg resource. It has THREE load-bearing
// behaviours that are part of the public contract for downstream
// consumers (DeploymentScheduler, garbage collection):
//
//  1. OwnerReference shape — APIVersion/Kind/Name/UID of the parent
//     HAProxyTemplateConfig with Controller=true and BlockOwnerDeletion=
//     true. These flags are what guarantees:
//       - cascading deletion (deleting the parent reaps the child)
//       - single-controller semantics (only this controller manages it)
//     A regression flipping either to false would silently break GC.
//
//  2. Checksum vs Content split during compression — the Spec.Checksum
//     stored on the CRD is ALWAYS the checksum of the ORIGINAL
//     uncompressed content (req.Checksum), even when the Spec.Content
//     field stores compressed bytes. Drift detection on the consumer
//     side uses the checksum to match against re-rendered configs; if
//     the builder accidentally re-checksummed the compressed bytes,
//     drift detection would fire on every reload.
//
//  3. label/owner/spec field plumbing — the template-config label
//     (`haproxy-haptic.org/template-config`) is what the
//     DeploymentScheduler watches to find the HAProxyCfg for a given
//     template. A regression in the label key or value would silently
//     orphan the new HAProxyCfg.

func TestBuildRuntimeConfig_OwnerReferenceShape(t *testing.T) {
	// Pin the OwnerReference shape — every field matters for cascading
	// deletion + single-controller semantics.
	p := &Publisher{logger: testLogger()}

	uid := types.UID("11111111-2222-3333-4444-555555555555")
	req := &PublishRequest{
		TemplateConfigName:      "my-template",
		TemplateConfigNamespace: "haptic",
		TemplateConfigUID:       uid,
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "sha256:original",
	}

	cfg := p.buildRuntimeConfig("my-template-haproxycfg", req)

	require.NotNil(t, cfg)
	require.Len(t, cfg.OwnerReferences, 1,
		"buildRuntimeConfig must produce exactly one OwnerReference "+
			"pointing at the parent HAProxyTemplateConfig")

	ref := cfg.OwnerReferences[0]
	assert.Equal(t, "haproxy-haptic.org/v1alpha1", ref.APIVersion,
		"APIVersion must point at the haproxy-haptic group/version so "+
			"the API server can resolve the parent for cascading deletion")
	assert.Equal(t, "HAProxyTemplateConfig", ref.Kind,
		"Kind must match the parent CRD so the API server's "+
			"garbage collector reaps the child when the parent is deleted")
	assert.Equal(t, "my-template", ref.Name,
		"Name must reflect req.TemplateConfigName so the garbage "+
			"collector can resolve the parent by name")
	assert.Equal(t, uid, ref.UID,
		"UID must reflect req.TemplateConfigUID so a recreated parent "+
			"with the same name doesn't accidentally adopt this child")

	require.NotNil(t, ref.Controller,
		"Controller flag must be set (nil=defaults-to-false to API server)")
	assert.True(t, *ref.Controller,
		"Controller=true so only this controller manages the HAProxyCfg — "+
			"two controllers fighting would race-write the spec")

	require.NotNil(t, ref.BlockOwnerDeletion,
		"BlockOwnerDeletion flag must be set (nil=defaults-to-false)")
	assert.True(t, *ref.BlockOwnerDeletion,
		"BlockOwnerDeletion=true so deleting the HAProxyTemplateConfig "+
			"blocks until this HAProxyCfg is reaped — prevents orphaned "+
			"HAProxyCfgs from continuing to drive deployments")
}

func TestBuildRuntimeConfig_ChecksumComesFromRequestNotCompressedContent(t *testing.T) {
	// CRITICAL: Spec.Checksum on the resulting HAProxyCfg must always be
	// req.Checksum (the original-content checksum), NEVER recomputed from
	// the possibly-compressed Spec.Content. Downstream drift detection
	// re-renders the original config and compares its checksum; if the
	// builder accidentally re-checksummed the compressed bytes, drift
	// detection would falsely fire on every reload because compression
	// output isn't byte-stable across zstd library versions.
	tests := []struct {
		name                 string
		threshold            int64
		config               string
		expectedCompressFlag bool
	}{
		{
			name:                 "compression disabled → Checksum is req.Checksum",
			threshold:            0,
			config:               strings.Repeat("global\n  daemon\n", 1000), // big but won't compress
			expectedCompressFlag: false,
		},
		{
			name:                 "compression enabled but below threshold → Checksum is req.Checksum",
			threshold:            1_000_000, // huge threshold
			config:               "small",
			expectedCompressFlag: false,
		},
		{
			name:                 "compression enabled and active → Checksum still from req.Checksum",
			threshold:            100,
			config:               strings.Repeat("frontend http\n  bind *:80\n", 200),
			expectedCompressFlag: true,
		},
	}

	const originalChecksum = "sha256:ORIGINAL_CHECKSUM_FROM_CALLER"

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Publisher{logger: testLogger()}
			req := &PublishRequest{
				TemplateConfigName:      "x",
				TemplateConfigNamespace: "y",
				Config:                  tt.config,
				Checksum:                originalChecksum,
				CompressionThreshold:    tt.threshold,
			}

			cfg := p.buildRuntimeConfig("x-haproxycfg", req)

			require.NotNil(t, cfg)
			assert.Equal(t, originalChecksum, cfg.Spec.Checksum,
				"Spec.Checksum MUST equal req.Checksum (the ORIGINAL "+
					"content checksum) regardless of compression — drift "+
					"detection compares against re-rendered originals; "+
					"recomputing from compressed bytes would fire on every "+
					"reload because zstd output isn't byte-stable")

			assert.Equal(t, tt.expectedCompressFlag, cfg.Spec.Compressed,
				"Spec.Compressed MUST reflect whether Spec.Content was "+
					"actually compressed — consumers use this flag to "+
					"decide whether to decompress before parsing")

			if tt.expectedCompressFlag {
				assert.NotEqual(t, tt.config, cfg.Spec.Content,
					"Compressed=true means Spec.Content must be the "+
						"compressed bytes, NOT the original config")
				assert.Less(t, len(cfg.Spec.Content), len(tt.config),
					"compressed Content must be smaller than original "+
						"(otherwise compression should not have been applied)")
			} else {
				assert.Equal(t, tt.config, cfg.Spec.Content,
					"Compressed=false means Spec.Content must be the "+
						"verbatim original config")
			}
		})
	}
}

func TestBuildRuntimeConfig_NameNamespaceLabelsAndPath(t *testing.T) {
	// Pin the namespace/label/spec plumbing. A regression in any of these
	// would silently orphan the HAProxyCfg from its watcher.
	p := &Publisher{logger: testLogger()}

	req := &PublishRequest{
		TemplateConfigName:      "my-template",
		TemplateConfigNamespace: "edge",
		Config:                  "global\n",
		ConfigPath:              "/custom/path/haproxy.cfg",
		Checksum:                "sha256:abc",
	}

	cfg := p.buildRuntimeConfig("explicit-name-haproxycfg", req)

	require.NotNil(t, cfg)
	// Name comes from the explicit argument, NOT regenerated from the
	// req. This decoupling lets createOrUpdateRuntimeConfig append a
	// NameSuffix (e.g. "-invalid") without buildRuntimeConfig knowing
	// about it.
	assert.Equal(t, "explicit-name-haproxycfg", cfg.Name,
		"Name MUST come from the function argument, not regenerated "+
			"from req.TemplateConfigName — the suffix-handling caller "+
			"depends on this")
	assert.Equal(t, "edge", cfg.Namespace,
		"Namespace must reflect req.TemplateConfigNamespace so the "+
			"HAProxyCfg lives next to the HAProxyTemplateConfig that "+
			"owns it (RBAC + namespace-scoped reconciliation)")

	// The template-config label is what DeploymentScheduler / cleanup
	// uses to find the HAProxyCfg for a given template. Pin both key
	// AND value.
	require.Contains(t, cfg.Labels, "haproxy-haptic.org/template-config",
		"the haproxy-haptic.org/template-config label is what consumers "+
			"watch — without it, the HAProxyCfg is orphaned from its "+
			"DeploymentScheduler")
	assert.Equal(t, "my-template", cfg.Labels["haproxy-haptic.org/template-config"],
		"template-config label value MUST match req.TemplateConfigName "+
			"so the consumer can resolve the parent template")

	// Spec.Path must reflect req.ConfigPath verbatim — operators can
	// override the default /etc/haproxy/haproxy.cfg, and the running
	// HAProxy needs to load from the right path.
	assert.Equal(t, "/custom/path/haproxy.cfg", cfg.Spec.Path,
		"Spec.Path MUST reflect req.ConfigPath verbatim — operators "+
			"override this for sidecar / multi-instance setups")
}

func TestBuildRuntimeConfig_NameSuffixHandledByCaller(t *testing.T) {
	// buildRuntimeConfig itself does NOT apply NameSuffix — the caller
	// (createOrUpdateRuntimeConfig) does so before passing the name. Pin
	// this contract so a refactor that pushed the suffix handling INTO
	// buildRuntimeConfig (and accidentally double-suffixed) is caught.
	p := &Publisher{logger: testLogger()}

	req := &PublishRequest{
		TemplateConfigName:      "tc",
		TemplateConfigNamespace: "ns",
		NameSuffix:              "-invalid", // deliberately NOT applied by the builder
		Config:                  "x",
		Checksum:                "c",
	}

	cfg := p.buildRuntimeConfig("name-from-caller", req)

	require.NotNil(t, cfg)
	assert.Equal(t, "name-from-caller", cfg.Name,
		"buildRuntimeConfig MUST use the literal name argument; the "+
			"NameSuffix concatenation is the CALLER's responsibility "+
			"(createOrUpdateRuntimeConfig). A regression that applied "+
			"the suffix here too would produce double-suffixed names "+
			"like 'name-from-caller-invalid'")
	assert.NotContains(t, cfg.Name, req.NameSuffix,
		"NameSuffix MUST NOT leak into the builder's output — "+
			"caller already applied it")
}

func TestBuildRuntimeConfig_CompressionMetadataConsistency(t *testing.T) {
	// Negative test: when compressIfNeeded chooses NOT to compress (e.g.
	// content already smaller than threshold), the resulting HAProxyCfg
	// MUST have Compressed=false AND Content==req.Config. Both fields
	// must agree — Compressed=true with original Content (or vice versa)
	// would trigger consumers to attempt zstd decompression on plain
	// text and crash.
	p := &Publisher{logger: testLogger()}

	tests := []struct {
		name           string
		config         string
		threshold      int64
		wantCompressed bool
		wantContent    string
	}{
		{
			name:           "no compression: content unchanged, flag false",
			config:         "global\n",
			threshold:      0,
			wantCompressed: false,
			wantContent:    "global\n",
		},
		{
			name:           "below-threshold content unchanged",
			config:         "global\n",
			threshold:      1_000_000,
			wantCompressed: false,
			wantContent:    "global\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &PublishRequest{
				TemplateConfigName:   "x",
				Config:               tt.config,
				CompressionThreshold: tt.threshold,
				Checksum:             "abc",
			}
			cfg := p.buildRuntimeConfig("name", req)
			require.NotNil(t, cfg)

			// Content<->Compressed flag MUST agree to prevent consumer
			// from feeding plaintext to zstd or compressed bytes to
			// the parser.
			assert.Equal(t, tt.wantCompressed, cfg.Spec.Compressed)
			assert.Equal(t, tt.wantContent, cfg.Spec.Content)
		})
	}
}

// Compile-time guard that we are testing the right thing; if the field
// names migrate, this fails to compile (catches API renames before they
// reach reviewers).
var _ = func() {
	_ = (&Publisher{}).buildRuntimeConfig
	var _ metav1.OwnerReference
}
