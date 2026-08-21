// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

//go:build playground

package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// extractCaches reaches the public surface via Parser.ParseFromString
// → conf.Caches. The function follows the same structural pattern as
// every other "list-of-named-sections" extractor:
//
//  1. SectionsGet returns error → propagate the error
//  2. Per-section ParseCacheSection error → log and continue (the
//     section is dropped from the slice but other sections survive)
//  3. Happy path → append the populated *models.Cache to the slice
//
// The two end-to-end contracts that matter to consumers are:
//
//   - When the source config has NO cache section, conf.Caches MUST
//     be empty (nil or len 0). Consumers iterate this slice; a
//     regression that returned a `nil`-element-bearing slice would
//     nil-deref on the first iteration.
//
//   - When the source config DOES contain a cache section, the entry
//     MUST appear in conf.Caches with the section's NAME populated.
//     A regression that swapped the section type argument or stopped
//     calling ParseCacheSection (or stopped propagating the section
//     name) would silently drop every operator's cache config and
//     the HAProxy cache feature would appear broken end-to-end.
//
// Test harness uses ParseFromString since it is the only public
// surface that exercises extractCaches — extractCaches itself is
// package-private. This pins the contract at the surface that
// matters to consumers.
func TestParseFromString_CachesField(t *testing.T) {
	tests := []struct {
		name       string
		config     string
		wantEmpty  bool
		wantName   string
		wantMaxAge int64 // 0 → don't assert; >0 → MaxAge must equal this
	}{
		{
			name: "no cache section — Caches is empty",
			config: `
global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s
`,
			wantEmpty: true,
		},
		{
			name: "cache section present — Caches has one entry with name + max-age",
			config: `
global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s

cache foobar
    total-max-size 4
    max-age 240
`,
			wantEmpty:  false,
			wantName:   "foobar",
			wantMaxAge: 240,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestParser(t)
			conf, err := p.ParseFromString(tt.config)
			require.NoError(t, err)
			require.NotNil(t, conf)

			if tt.wantEmpty {
				assert.Empty(t, conf.Caches,
					"Caches MUST be empty when the source config has no cache "+
						"section — consumers iterate this slice and a regression "+
						"that returned a nil-element-bearing slice would nil-deref "+
						"on first iteration")
				return
			}

			require.Len(t, conf.Caches, 1,
				"a single-cache config MUST yield exactly one entry — a "+
					"regression that dropped the section here would silently "+
					"break the HAProxy cache feature end-to-end")

			cache := conf.Caches[0]
			require.NotNil(t, cache.Name,
				"the cache entry's Name pointer MUST be populated — "+
					"a regression that stopped propagating the section name "+
					"would leave consumers unable to identify which cache "+
					"config they're looking at")
			assert.Equal(t, tt.wantName, *cache.Name)

			if tt.wantMaxAge > 0 {
				assert.Equal(t, tt.wantMaxAge, cache.MaxAge,
					"max-age directive MUST flow through ParseCacheSection — "+
						"otherwise cache TTL silently defaults to zero and "+
						"every cache lookup misses")
			}
		})
	}
}
