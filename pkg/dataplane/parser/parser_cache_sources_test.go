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

const sourcesTestConfig = `global
  daemon
defaults
  mode http
  timeout connect 5s
  timeout client 30s
  timeout server 30s
frontend fe_%d
  bind :8080
`

// TestCacheStatsBySource_AttributesMisses pins the observability that #139
// lacked. The aggregate hit rate cannot say WHICH call site is missing, so a
// cache being flushed by single-use content looks identical to one that is
// merely too small — the ambiguity that made the first two fixes miss.
func TestCacheStatsBySource_AttributesMisses(t *testing.T) {
	p, err := New()
	require.NoError(t, err)

	hitsBefore, missBefore := CacheStatsBySource()

	desired := fmt.Sprintf(sourcesTestConfig, 1000)
	_, err = p.ParseFromStringFor(SourceDesired, desired)
	require.NoError(t, err)
	_, err = p.ParseFromStringFor(SourceDesired, desired) // same content → hit
	require.NoError(t, err)

	hits, misses := CacheStatsBySource()
	assert.Equal(t, hitsBefore[SourceDesired]+1, hits[SourceDesired],
		"the second parse of identical desired content must be attributed as a hit")
	assert.Equal(t, missBefore[SourceDesired]+1, misses[SourceDesired],
		"the first parse must be attributed as a miss")
}

// TestUncachedSourcesNeverPolluteTheCache is the invariant that actually fixes
// #139: content that is unique by construction — a read-back, a current-config
// read, a post-sync fetch, each carrying HAProxy's own `_version` header — must
// neither be counted nor stored. Storing it evicts the desired config, the one
// parse with reuse across replicas.
func TestUncachedSourcesNeverPolluteTheCache(t *testing.T) {
	p, err := New()
	require.NoError(t, err)

	desired := fmt.Sprintf(sourcesTestConfig, 2000)
	_, err = p.ParseFromString(desired) // seed
	require.NoError(t, err)

	hitsBefore, missBefore := CacheStatsBySource()

	// Every single-use source, more of them than the cache has slots.
	for _, source := range []string{SourceReadBack, SourceCurrent, SourcePostSync} {
		for i := 1; i <= ParsedConfigCacheSize; i++ {
			_, err := p.ParseFromStringUncachedFor(source, fmt.Sprintf(sourcesTestConfig, i))
			require.NoError(t, err)
		}
	}

	hits, misses := CacheStatsBySource()
	for _, source := range []string{SourceReadBack, SourceCurrent, SourcePostSync} {
		assert.Equal(t, hitsBefore[source], hits[source],
			"%s is uncached and must record no hits", source)
		assert.Equal(t, missBefore[source], misses[source],
			"%s is uncached and must record no misses — counting it would make the "+
				"hit rate look bad for parses the cache was never meant to serve", source)
	}

	// The seeded desired config must have survived all of that.
	_, err = p.ParseFromString(desired)
	require.NoError(t, err)
	hitsAfter, _ := CacheStatsBySource()
	assert.Greater(t, hitsAfter[SourceUnlabelled], hitsBefore[SourceUnlabelled],
		"single-use parses evicted the desired config — the cache is still being thrashed")
}
