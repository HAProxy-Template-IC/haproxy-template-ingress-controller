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

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// parserCacheBySource exports the parser cache's per-source tallies.
//
// A custom collector rather than a CounterFunc because the aggregate rate
// cannot distinguish a cache that is too small from one being flushed by
// single-use content, and Prometheus has no labelled CounterFunc. The parser
// package owns the counters and must not import prometheus, so the values are
// read on scrape.
type parserCacheBySource struct {
	hits   *prometheus.Desc
	misses *prometheus.Desc
}

func newParserCacheBySource() *parserCacheBySource {
	return &parserCacheBySource{
		hits: prometheus.NewDesc(
			"haptic_parser_cache_hits_by_source_total",
			"Parser cache hits, by the call site that requested the parse",
			[]string{"source"}, nil,
		),
		misses: prometheus.NewDesc(
			"haptic_parser_cache_misses_by_source_total",
			"Parser cache misses, by the call site that requested the parse. A source that only ever misses is parsing content that is unique by construction and should use the uncached path.",
			[]string{"source"}, nil,
		),
	}
}

func (c *parserCacheBySource) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.hits
	ch <- c.misses
}

func (c *parserCacheBySource) Collect(ch chan<- prometheus.Metric) {
	hits, misses := parser.CacheStatsBySource()
	for source, n := range hits {
		ch <- prometheus.MustNewConstMetric(c.hits, prometheus.CounterValue, float64(n), source)
	}
	for source, n := range misses {
		ch <- prometheus.MustNewConstMetric(c.misses, prometheus.CounterValue, float64(n), source)
	}
}
