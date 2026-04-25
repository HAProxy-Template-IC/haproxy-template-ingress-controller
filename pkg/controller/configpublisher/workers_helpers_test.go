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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

// Three pure helpers in pkg/controller/configpublisher/workers.go
// had no direct test coverage despite governing the publish-side
// content-deduplication discipline:
//
//  - buildPublishRequest assembles the K8s API request shape from
//    a templateConfig + cached entry. The hardcoded ConfigPath and
//    pass-through of name/namespace/UID/checksum are load-bearing.
//
//  - discardCachedConfig removes a renderedConfigs entry under
//    lock. Used by both worker paths after a publish completes (or
//    is deduped); a regression that forgot to take the lock would
//    race with the handler goroutine writing new entries; a
//    regression that targeted the wrong key would leak entries.
//
//  - skipIfAlreadyPublished implements the content-dedup gate.
//    Three branches matter: empty-checksum (can't dedup → publish),
//    different-checksum (must publish), same-checksum (skip AND
//    discard the cached entry to prevent unbounded growth of the
//    renderedConfigs map).

func TestComponent_BuildPublishRequest(t *testing.T) {
	c := &Component{
		// Component embeds *ReadySignal but buildPublishRequest
		// doesn't touch it; nil here is fine.
	}

	tc := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "haproxy-config",
			Namespace: "haptic",
			UID:       types.UID("abc-123"),
		},
		Spec: v1alpha1.HAProxyTemplateConfigSpec{
			Controller: v1alpha1.ControllerConfig{
				ConfigPublishing: v1alpha1.ConfigPublishingConfig{
					CompressionThreshold: 524288, // 512 KiB
				},
			},
		},
	}

	entry := &renderedConfigEntry{
		config:          "global\n  daemon\n",
		auxFiles:        nil, // exercises the nil-aux-files passthrough via convertAuxiliaryFiles
		contentChecksum: "ab12cd34",
	}

	req := c.buildPublishRequest(tc, entry)

	require.NotNil(t, req)

	// Identity fields must round-trip from the templateConfig.
	assert.Equal(t, "haproxy-config", req.TemplateConfigName,
		"TemplateConfigName must round-trip from CRD ObjectMeta")
	assert.Equal(t, "haptic", req.TemplateConfigNamespace,
		"TemplateConfigNamespace must round-trip from CRD ObjectMeta")
	assert.Equal(t, types.UID("abc-123"), req.TemplateConfigUID,
		"TemplateConfigUID must round-trip — ownerReferences in published resources depend on it")

	// Content fields from the entry.
	assert.Equal(t, "global\n  daemon\n", req.Config)
	assert.Equal(t, "ab12cd34", req.Checksum,
		"Checksum must propagate so downstream content-dedupe in the K8s publisher can short-circuit")

	// Hardcoded path — pinning catches a regression that points the
	// publisher elsewhere (which would silently break HAProxy at
	// container startup since the config wouldn't be where HAProxy
	// looks for it).
	assert.Equal(t, "/etc/haproxy/haproxy.cfg", req.ConfigPath,
		"ConfigPath is the well-known HAProxy config location; "+
			"pointing it elsewhere would silently break HAProxy at container startup")

	// Compression threshold from the CRD passes through verbatim
	// (not zero — the getCompressionThreshold default-fallback is
	// covered separately in handlers_helpers_test.go).
	assert.Equal(t, int64(524288), req.CompressionThreshold)

	// nil aux-files round-trips to nil via convertAuxiliaryFiles.
	assert.Nil(t, req.AuxiliaryFiles,
		"nil aux-files in entry must round-trip to nil in request — "+
			"otherwise downstream branches that test for nil flip silently")
}

func TestComponent_DiscardCachedConfig(t *testing.T) {
	t.Run("removes the entry under lock and leaves siblings intact", func(t *testing.T) {
		c := &Component{
			renderedConfigs: map[string]*renderedConfigEntry{
				"corr-A": {config: "config-A"},
				"corr-B": {config: "config-B"},
				"corr-C": {config: "config-C"},
			},
		}

		c.discardCachedConfig("corr-B")

		c.mu.RLock()
		defer c.mu.RUnlock()
		assert.NotContains(t, c.renderedConfigs, "corr-B",
			"the targeted correlation ID must be removed")
		assert.Contains(t, c.renderedConfigs, "corr-A",
			"unrelated entries must remain untouched — discard targets a single key")
		assert.Contains(t, c.renderedConfigs, "corr-C")
	})

	t.Run("missing key is a no-op (no panic)", func(t *testing.T) {
		// Worker callbacks may fire after the entry has already
		// been discarded by an earlier branch (e.g. dedupe + worker
		// completion both call discard). The function MUST be
		// idempotent and tolerate missing keys.
		c := &Component{
			renderedConfigs: map[string]*renderedConfigEntry{
				"corr-A": {config: "config-A"},
			},
		}

		require.NotPanics(t, func() {
			c.discardCachedConfig("never-existed")
		})
		assert.Len(t, c.renderedConfigs, 1, "missing-key discard must not touch other entries")
	})
}

func TestComponent_SkipIfAlreadyPublished(t *testing.T) {
	logger := testutil.NewTestLogger()

	t.Run("empty checksum is NOT skipped (cannot dedupe without a checksum)", func(t *testing.T) {
		c := &Component{
			logger:                logger,
			lastPublishedChecksum: "any-previous-checksum",
			renderedConfigs:       map[string]*renderedConfigEntry{"corr-1": {}},
		}

		work := &publishWorkItem{
			correlationID: "corr-1",
			entry:         &renderedConfigEntry{contentChecksum: ""},
		}

		skipped := c.skipIfAlreadyPublished(work, "would-skip")
		assert.False(t, skipped,
			"empty checksum must NOT trigger skip — without a checksum we cannot prove "+
				"the content matches the last publish, so the safe default is to publish anyway")

		// The cached entry must NOT be discarded when we don't skip
		// (it's still needed for the actual publish path).
		assert.Contains(t, c.renderedConfigs, "corr-1",
			"non-skip path must leave the cached entry intact for the actual publish")
	})

	t.Run("different checksum is NOT skipped (content has changed)", func(t *testing.T) {
		c := &Component{
			logger:                logger,
			lastPublishedChecksum: "old-checksum",
			renderedConfigs:       map[string]*renderedConfigEntry{"corr-1": {}},
		}

		work := &publishWorkItem{
			correlationID: "corr-1",
			entry:         &renderedConfigEntry{contentChecksum: "new-checksum"},
		}

		skipped := c.skipIfAlreadyPublished(work, "would-skip")
		assert.False(t, skipped,
			"different checksum from lastPublished means content changed — must publish")
		assert.Contains(t, c.renderedConfigs, "corr-1")
	})

	t.Run("same checksum IS skipped AND discards the cached entry", func(t *testing.T) {
		// This is the load-bearing branch. A regression that:
		// - skipped without discarding would leak cached entries
		//   forever, growing renderedConfigs unboundedly under
		//   stable-content reconciliation;
		// - failed to skip would re-publish the same content on
		//   every reconciliation, hammering the K8s API.
		c := &Component{
			logger:                logger,
			lastPublishedChecksum: "stable-checksum",
			renderedConfigs:       map[string]*renderedConfigEntry{"corr-1": {}},
		}

		work := &publishWorkItem{
			correlationID: "corr-1",
			entry:         &renderedConfigEntry{contentChecksum: "stable-checksum"},
		}

		skipped := c.skipIfAlreadyPublished(work, "deduped")
		assert.True(t, skipped,
			"matching checksum must trigger skip to avoid re-publishing identical content")
		assert.NotContains(t, c.renderedConfigs, "corr-1",
			"the skip path MUST also discard the cached entry; "+
				"otherwise renderedConfigs would grow unboundedly under stable-content reconciliation")
	})
}

// renderedConfigEntry timestamps are documented but the helpers
// above don't depend on them. Sanity-check that buildPublishRequest
// doesn't accidentally read the timestamp field — adding a field
// should not silently change the request shape.
func TestComponent_BuildPublishRequest_TimestampUnused(t *testing.T) {
	c := &Component{}
	tc := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "y"},
	}

	now := time.Now()
	entry := &renderedConfigEntry{
		config:          "cfg",
		contentChecksum: "ck",
		renderedAt:      now,
	}

	req := c.buildPublishRequest(tc, entry)
	assert.Equal(t, "cfg", req.Config,
		"buildPublishRequest must not be affected by entry.renderedAt — that field is metadata, not request input")
}
