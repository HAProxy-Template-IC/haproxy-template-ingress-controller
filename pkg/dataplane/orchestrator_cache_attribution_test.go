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

package dataplane

import (
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// reconciliationConfig is a realistic rendered config. The %d stands in for the
// endpoint churn that makes every render a different string.
const reconciliationConfig = `global
  daemon
defaults
  mode http
  timeout connect 5s
  timeout client 30s
  timeout server 30s
frontend fe_main
  bind :8080
  default_backend be_app
backend be_app
  server SRV_1 10.42.0.%d:8080 check
`

// haproxyReturned is what HAProxy hands back: the same config plus the version
// header the Dataplane API stamps on it, which changes on every push. This is
// the whole reason those parses can never hit the cache.
func haproxyReturned(config string, version int) string {
	return fmt.Sprintf("# _md5hash=%032x\n# _version=%d\n%s", version, version, config)
}

// TestReconciliationParsePattern_CacheAttribution reproduces one reconciliation
// cycle against a two-replica fleet and asserts where the parses land.
//
// This exists because #139 was closed twice on reasoning. The aggregate hit
// rate cannot distinguish a cache that is too small from one being flushed by
// content that can never hit, so the fix has to be pinned per source or the
// next refactor silently reintroduces the thrash.
func TestReconciliationParsePattern_CacheAttribution(t *testing.T) {
	realParser, err := parser.New()
	require.NoError(t, err)

	orch := &orchestrator{
		parser:     realParser,
		comparator: comparator.New(),
		logger:     slog.Default(),
	}

	hitsBefore, missBefore := parser.CacheStatsBySource()

	// One render, deployed to two replicas — the shape of every endpoint-churn
	// reconciliation. Each replica returns its OWN current config, because the
	// version counter differs per pod.
	desired := fmt.Sprintf(reconciliationConfig, 7)
	for replica := 1; replica <= 2; replica++ {
		current := haproxyReturned(fmt.Sprintf(reconciliationConfig, 6), 200+replica)
		_, err := orch.parseAndCompareConfigs(current, desired, nil, nil)
		require.NoError(t, err)
	}

	hits, misses := parser.CacheStatsBySource()
	delta := func(m map[string]int64, before map[string]int64, k string) int64 {
		return m[k] - before[k]
	}

	// The desired config is the one parse with reuse across replicas: parsed
	// for the first, served from cache for the second.
	assert.Equal(t, int64(1), delta(misses, missBefore, parser.SourceDesired),
		"the desired config should be parsed once for the fleet")
	assert.Equal(t, int64(1), delta(hits, hitsBefore, parser.SourceDesired),
		"the second replica must be served from cache; if this is 0 the desired "+
			"entry was evicted between replicas, which is the #139 thrash")

	// Per-replica current configs are unique by construction and must not touch
	// the cache in either direction — not stored, and not counted as misses
	// against a cache that was never meant to serve them.
	assert.Zero(t, delta(misses, missBefore, parser.SourceCurrent),
		"current-config reads are uncached and must record no misses")
	assert.Zero(t, delta(hits, hitsBefore, parser.SourceCurrent),
		"current-config reads are uncached and must record no hits")
}

// TestValidationTestParsesDoNotEvictDesired pins the polluter that the
// aggregate metric hid: `haproxy_valid` runs over every validationTest fixture
// at config load. Those configs are never parsed again, so routing them through
// a four-slot cache flushed the desired entry on every load.
func TestValidationTestParsesDoNotEvictDesired(t *testing.T) {
	realParser, err := parser.New()
	require.NoError(t, err)

	desired := fmt.Sprintf(reconciliationConfig, 42)
	_, err = realParser.ParseFromStringFor(parser.SourceDesired, desired)
	require.NoError(t, err)

	hitsBefore, missBefore := parser.CacheStatsBySource()

	// Far more distinct fixtures than the cache has slots — the real suite runs
	// hundreds.
	for i := 0; i < parser.ParsedConfigCacheSize*4; i++ {
		_, err := ValidateSyntaxAndSchemaUncached(fmt.Sprintf(reconciliationConfig, 100+i), nil)
		require.NoError(t, err)
	}

	hits, misses := parser.CacheStatsBySource()
	assert.Equal(t, missBefore[parser.SourceValidation], misses[parser.SourceValidation],
		"validationTest fixtures are uncached and must record no misses")

	// The desired entry must have survived the whole suite.
	_, err = realParser.ParseFromStringFor(parser.SourceDesired, desired)
	require.NoError(t, err)
	hitsAfter, _ := parser.CacheStatsBySource()
	assert.Equal(t, hitsBefore[parser.SourceDesired]+1, hitsAfter[parser.SourceDesired],
		"the desired config was evicted by validationTest fixtures — the cache is "+
			"still being flushed at config load")
	assert.Equal(t, hits[parser.SourceDesired], hitsBefore[parser.SourceDesired],
		"no spurious desired hits should have been recorded during validation")
}
