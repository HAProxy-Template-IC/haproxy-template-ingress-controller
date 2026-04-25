// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package helpers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// ExtractPostProcessorConfigs builds the per-template post-processor
// map that the renderer feeds into the engine. The function picks up
// post_processing entries from the four template-bearing config
// shapes (HAProxyConfig, Maps, Files, SSLCertificates) and keys them
// by template name.
//
// Pin every contract:
//   - empty config yields an empty (non-nil) map
//   - main template's post-processors land under the canonical
//     MainTemplateName key
//   - each Maps / Files / SSLCertificates entry's post-processors
//     land under that entry's name
//   - templates with NO post-processors must NOT add a key (the
//     renderer iterates the result and an empty-slice key would
//     trigger an empty no-op pipeline)
//   - convertPostProcessorConfigs propagates Type and Params
//     verbatim — it's the type-cast bridge between config and
//     templating packages
func TestExtractPostProcessorConfigs(t *testing.T) {
	t.Run("empty config yields empty map", func(t *testing.T) {
		got := ExtractPostProcessorConfigs(&config.Config{})
		assert.NotNil(t, got, "must always return a non-nil map for safe iteration")
		assert.Empty(t, got)
	})

	t.Run("main template's post-processors land under MainTemplateName", func(t *testing.T) {
		cfg := &config.Config{
			HAProxyConfig: config.HAProxyConfig{
				PostProcessing: []config.PostProcessorConfig{
					{Type: "regex_replace", Params: map[string]string{"pattern": "^[ ]+", "replace": "  "}},
				},
			},
		}
		got := ExtractPostProcessorConfigs(cfg)
		require.Contains(t, got, names.MainTemplateName)
		assert.Len(t, got[names.MainTemplateName], 1)
		assert.Equal(t, templating.PostProcessorType("regex_replace"), got[names.MainTemplateName][0].Type)
	})

	t.Run("Maps / Files / SSLCertificates each land under their own name", func(t *testing.T) {
		cfg := &config.Config{
			Maps: map[string]config.MapFile{
				"hosts.map": {
					PostProcessing: []config.PostProcessorConfig{
						{Type: "regex_replace", Params: map[string]string{"pattern": "x", "replace": "y"}},
					},
				},
			},
			Files: map[string]config.GeneralFile{
				"errors.http": {
					PostProcessing: []config.PostProcessorConfig{
						{Type: "noop"},
					},
				},
			},
			SSLCertificates: map[string]config.SSLCertificate{
				"tls.pem": {
					PostProcessing: []config.PostProcessorConfig{
						{Type: "regex_replace", Params: map[string]string{"pattern": "a", "replace": "b"}},
					},
				},
			},
		}
		got := ExtractPostProcessorConfigs(cfg)

		assert.Contains(t, got, "hosts.map")
		assert.Contains(t, got, "errors.http")
		assert.Contains(t, got, "tls.pem")
		assert.Len(t, got, 3)
	})

	t.Run("templates with no post-processors do NOT add a key", func(t *testing.T) {
		// Critical: the renderer iterates the result to build per-
		// template pipelines. An empty-slice key would create an
		// empty no-op pipeline and waste cycles on every render. Pin
		// the contract that empty post_processing is treated as
		// absent.
		cfg := &config.Config{
			HAProxyConfig: config.HAProxyConfig{Template: "global"}, // no PostProcessing
			Maps: map[string]config.MapFile{
				"empty.map": {Template: "x"}, // no PostProcessing
			},
			Files: map[string]config.GeneralFile{
				"with-pp.http": {
					PostProcessing: []config.PostProcessorConfig{{Type: "regex_replace"}},
				},
			},
		}
		got := ExtractPostProcessorConfigs(cfg)

		assert.NotContains(t, got, names.MainTemplateName,
			"main template without post-processors must not be in the map")
		assert.NotContains(t, got, "empty.map",
			"map file without post-processors must not be in the map")
		assert.Contains(t, got, "with-pp.http")
		assert.Len(t, got, 1)
	})
}

// convertPostProcessorConfigs is the type-cast bridge between
// config.PostProcessorConfig and templating.PostProcessorConfig. The
// Type field is cast (string → PostProcessorType) and Params is
// passed by reference (NOT defensively copied — the caller owns the
// map). Pin both halves.
func TestConvertPostProcessorConfigs(t *testing.T) {
	t.Run("empty input yields empty (non-nil) slice", func(t *testing.T) {
		got := convertPostProcessorConfigs(nil)
		assert.NotNil(t, got, "result is allocated up front based on len(input), so empty input yields a zero-length slice")
		assert.Empty(t, got)

		got = convertPostProcessorConfigs([]config.PostProcessorConfig{})
		assert.Empty(t, got)
	})

	t.Run("Type field is cast to templating.PostProcessorType verbatim", func(t *testing.T) {
		got := convertPostProcessorConfigs([]config.PostProcessorConfig{
			{Type: "regex_replace"},
			{Type: "noop"},
			{Type: "future-type-not-yet-validated"},
		})
		require.Len(t, got, 3)
		assert.Equal(t, templating.PostProcessorType("regex_replace"), got[0].Type)
		assert.Equal(t, templating.PostProcessorType("noop"), got[1].Type)
		assert.Equal(t, templating.PostProcessorType("future-type-not-yet-validated"), got[2].Type,
			"unrecognised types pass through (validation happens later in templating package)")
	})

	t.Run("Params are passed by reference (not defensively copied)", func(t *testing.T) {
		params := map[string]string{"pattern": "^[ ]+", "replace": "  "}
		got := convertPostProcessorConfigs([]config.PostProcessorConfig{
			{Type: "regex_replace", Params: params},
		})
		require.Len(t, got, 1)

		// Mutate the original map; the converted entry should observe
		// the mutation because no copy is made.
		params["pattern"] = "MUTATED"
		assert.Equal(t, "MUTATED", got[0].Params["pattern"],
			"convertPostProcessorConfigs aliases Params, not copies — pin the contract so callers know")
	})
}
