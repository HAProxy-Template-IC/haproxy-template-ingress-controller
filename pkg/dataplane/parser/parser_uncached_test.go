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

package parser

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const uncachedTestConfig = `global
  daemon
defaults
  mode http
  timeout connect 5s
  timeout client 30s
  timeout server 30s
frontend fe_%d
  bind :8080
`

// TestParseFromStringUncached_DoesNotEvictCachedEntries pins the fix for #139.
//
// A post-reload read-back carries HAProxy's own `_version` header, so it is a
// different string on every push: the lookup can never hit, and the insert
// evicts the desired config, which is the one parse with reuse value. With
// ParsedConfigCacheSize == 4, a handful of read-backs used to flush the cache.
func TestParseFromStringUncached_DoesNotEvictCachedEntries(t *testing.T) {
	p, err := New()
	require.NoError(t, err)

	desired := fmt.Sprintf(uncachedTestConfig, 0)

	// Seed the cache with the desired config.
	_, err = p.ParseFromString(desired)
	require.NoError(t, err)

	hitsBefore, _ := CacheStats()

	// More unique read-backs than the cache has slots. Routed through the
	// caching path, these would evict `desired` several times over.
	for i := 1; i <= ParsedConfigCacheSize*3; i++ {
		_, err := p.ParseFromStringUncached(fmt.Sprintf(uncachedTestConfig, i))
		require.NoError(t, err)
	}

	// The desired config must still be cached.
	_, err = p.ParseFromString(desired)
	require.NoError(t, err)

	hitsAfter, _ := CacheStats()
	assert.Greater(t, hitsAfter, hitsBefore,
		"the desired config was evicted by single-use read-backs — the cache is being thrashed")
}

// TestParseFromStringUncached_ParsesCorrectly proves skipping the cache does
// not change the result.
func TestParseFromStringUncached_ParsesCorrectly(t *testing.T) {
	p, err := New()
	require.NoError(t, err)

	cfg := fmt.Sprintf(uncachedTestConfig, 99)

	cached, err := p.ParseFromString(cfg)
	require.NoError(t, err)
	uncached, err := p.ParseFromStringUncached(cfg)
	require.NoError(t, err)

	require.NotNil(t, cached)
	require.NotNil(t, uncached)
	assert.Equal(t, len(cached.Frontends), len(uncached.Frontends))
}
