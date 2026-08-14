// Copyright 2026 Philipp Hossner
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

package templating

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"regexp/syntax"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/scriggo/native"
)

type regexCacheEnv struct {
	fatalValues []any
}

func (e *regexCacheEnv) CallPath() string                    { return "" }
func (e *regexCacheEnv) CallLine() int                       { return 0 }
func (e *regexCacheEnv) Context() context.Context            { return context.Background() }
func (e *regexCacheEnv) Fatal(value any)                     { e.fatalValues = append(e.fatalValues, value) }
func (e *regexCacheEnv) MarkdownConverter() native.Converter { return nil }
func (e *regexCacheEnv) Print(...any)                        {}
func (e *regexCacheEnv) Println(...any)                      {}
func (e *regexCacheEnv) Stop(error)                          {}
func (e *regexCacheEnv) TypeOf(v reflect.Value) reflect.Type { return v.Type() }

func TestBoundedRegexCacheReusesSuccessfulCompilation(t *testing.T) {
	cache := &boundedRegexCache[*regexp.Regexp]{maxEntries: regexSearchCacheEntries}
	var compileCalls atomic.Int64
	compile := func(pattern string) (*regexp.Regexp, error) {
		compileCalls.Add(1)
		return regexp.Compile(pattern)
	}

	first, err := cache.compile("^value$", compile)
	require.NoError(t, err)
	second, err := cache.compile("^value$", compile)
	require.NoError(t, err)

	assert.Same(t, first, second)
	assert.Equal(t, int64(1), compileCalls.Load())
}

func TestBoundedRegexCacheOwnsRetainedPattern(t *testing.T) {
	cache := &boundedRegexCache[*regexp.Regexp]{maxEntries: 1}
	parent := strings.Repeat("x", 1<<20) + "^value$"
	pattern := parent[len(parent)-len("^value$"):]

	_, err := cache.compile(pattern, regexp.Compile)
	require.NoError(t, err)
	require.Len(t, cache.entries, 1)

	var retainedPattern string
	for key := range cache.entries {
		retainedPattern = key
	}
	if unsafe.StringData(pattern) == unsafe.StringData(retainedPattern) {
		t.Fatal("retained pattern aliases its source")
	}
}

func TestBoundedRegexCacheDoesNotRetainInvalidOrLongPatterns(t *testing.T) {
	cache := &boundedRegexCache[*regexp.Regexp]{maxEntries: regexSearchCacheEntries}

	for range 2 {
		_, err := cache.compile("[", regexp.Compile)
		require.Error(t, err)
	}

	longPattern := strings.Repeat("a", regexCachePatternMaxBytes+1)
	first, err := cache.compile(longPattern, regexp.Compile)
	require.NoError(t, err)
	second, err := cache.compile(longPattern, regexp.Compile)
	require.NoError(t, err)

	assert.NotSame(t, first, second)
	assert.Empty(t, cache.entries)
}

func TestBoundedRegexCacheDoesNotRetainLargeCompiledPrograms(t *testing.T) {
	cache := &boundedRegexCache[*regexp.Regexp]{maxEntries: regexSearchCacheEntries}
	var compileCalls atomic.Int64
	compile := func(pattern string) (*regexp.Regexp, error) {
		compileCalls.Add(1)
		return regexp.Compile(pattern)
	}
	var patternBuilder strings.Builder
	for r := 'a'; r <= 'z'; r++ {
		fmt.Fprintf(&patternBuilder, "%c{1000}", r)
	}
	pattern := patternBuilder.String()

	first, err := cache.compile(pattern, compile)
	require.NoError(t, err)
	second, err := cache.compile(pattern, compile)
	require.NoError(t, err)

	assert.NotSame(t, first, second)
	require.Len(t, cache.entries, 1)
	assert.True(t, cache.entries[pattern].compileEveryTime)
	assert.Nil(t, cache.entries[pattern].compiled)
	assert.Equal(t, int64(2), compileCalls.Load())
}

func TestRegexCacheBounds(t *testing.T) {
	assert.Equal(t, 64, regexSearchCacheEntries)
	assert.Equal(t, 256, regexCachePatternMaxBytes)
	assert.Equal(t, 256, regexCacheMaxComplexity)
}

func TestRegexCacheComplexity(t *testing.T) {
	atLimitPattern := strings.Repeat("a", 127) + ".$"
	overLimitPattern := "^" + atLimitPattern

	tests := []struct {
		name      string
		pattern   string
		cost      int
		cacheable bool
	}{
		{name: "literal", pattern: "value", cost: 10, cacheable: true},
		{name: "empty", pattern: "(?:)", cost: 1, cacheable: true},
		{name: "anchor", pattern: "^", cost: 1, cacheable: true},
		{name: "wildcard", pattern: ".", cost: 1, cacheable: true},
		{name: "character class", pattern: "[ab]", cost: 3, cacheable: true},
		{name: "capture", pattern: "(a)", cost: 4, cacheable: true},
		{name: "star", pattern: "a*", cost: 4, cacheable: true},
		{name: "plus", pattern: "a+", cost: 3, cacheable: true},
		{name: "question", pattern: "a?", cost: 3, cacheable: true},
		{name: "alternation", pattern: "ab|cd", cost: 9, cacheable: true},
		{name: "finite repeat", pattern: "a{2,3}", cost: 7, cacheable: true},
		{name: "unbounded repeat", pattern: "a{2,}", cost: 5, cacheable: true},
		{name: "at limit", pattern: atLimitPattern, cost: 256, cacheable: true},
		{name: "over limit", pattern: overLimitPattern, cost: 257, cacheable: false},
		{name: "unicode class", pattern: `\p{L}`, cost: 257, cacheable: false},
		{name: "large counted repeat", pattern: "a{1000}", cost: 257, cacheable: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := syntax.Parse(tt.pattern, syntax.Perl)
			require.NoError(t, err)
			assert.Equal(t, tt.cost, regexComplexity(parsed, regexCacheMaxComplexity))
			assert.Equal(t, tt.cacheable, regexCacheable(tt.pattern))
		})
	}

	zeroMinimumRepeat := &syntax.Regexp{
		Op:  syntax.OpRepeat,
		Min: 0,
		Max: -1,
		Sub: []*syntax.Regexp{{Op: syntax.OpLiteral, Rune: []rune{'a'}}},
	}
	assert.Equal(t, 4, regexComplexity(zeroMinimumRepeat, regexCacheMaxComplexity))
	assert.Equal(t, 1, regexComplexity(&syntax.Regexp{Op: syntax.OpNoMatch}, regexCacheMaxComplexity))
}

func TestBoundedRegexCachePatternLengthBoundary(t *testing.T) {
	cache := &boundedRegexCache[*regexp.Regexp]{maxEntries: 2}
	atLimit := strings.Repeat(".", regexCachePatternMaxBytes)
	overLimit := atLimit + "."

	_, err := cache.compile(atLimit, regexp.Compile)
	require.NoError(t, err)
	_, err = cache.compile(overLimit, regexp.Compile)
	require.NoError(t, err)

	assert.Contains(t, cache.entries, atLimit)
	assert.NotContains(t, cache.entries, overLimit)
}

func TestBoundedRegexCacheHasFixedCapacity(t *testing.T) {
	const capacity = 3
	cache := &boundedRegexCache[*regexp.Regexp]{maxEntries: capacity}
	var firstCached *regexp.Regexp

	for i := range capacity {
		compiled, err := cache.compile(fmt.Sprintf("^value-%d$", i), regexp.Compile)
		require.NoError(t, err)
		if i == 0 {
			firstCached = compiled
		}
	}

	overflow := fmt.Sprintf("^value-%d$", capacity)
	overflowFirst, err := cache.compile(overflow, regexp.Compile)
	require.NoError(t, err)
	overflowSecond, err := cache.compile(overflow, regexp.Compile)
	require.NoError(t, err)

	assert.Len(t, cache.entries, capacity)
	assert.NotContains(t, cache.entries, overflow)
	assert.NotSame(t, overflowFirst, overflowSecond)

	var compileCalls atomic.Int64
	cached, err := cache.compile("^value-0$", func(pattern string) (*regexp.Regexp, error) {
		compileCalls.Add(1)
		return regexp.Compile(pattern)
	})
	require.NoError(t, err)
	assert.Same(t, firstCached, cached)
	assert.Zero(t, compileCalls.Load())
}

func TestBoundedRegexCacheConcurrentColdHit(t *testing.T) {
	cache := &boundedRegexCache[*regexp.Regexp]{maxEntries: regexSearchCacheEntries}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan *regexp.Regexp, 64)
	errs := make(chan error, 64)

	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			compiled, err := cache.compile("^value$", regexp.Compile)
			if err != nil {
				errs <- err
				return
			}
			results <- compiled
		}()
	}

	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Len(t, results, 64)
	var first *regexp.Regexp
	for compiled := range results {
		if first == nil {
			first = compiled
		}
		assert.Same(t, first, compiled)
	}
	assert.Len(t, cache.entries, 1)
}

func TestBoundedRegexCacheConcurrentCapacity(t *testing.T) {
	const capacity = 4
	cache := &boundedRegexCache[*regexp.Regexp]{maxEntries: capacity}
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 64)

	for i := range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := cache.compile(fmt.Sprintf("^value-%d$", i), regexp.Compile)
			if err != nil {
				errs <- err
			}
		}()
	}

	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.Len(t, cache.entries, capacity)
}

func TestBoundedRegexCachesAreIndependent(t *testing.T) {
	firstCache := &boundedRegexCache[*regexp.Regexp]{maxEntries: 1}
	secondCache := &boundedRegexCache[*regexp.Regexp]{maxEntries: 1}
	first, err := firstCache.compile("^value$", regexp.Compile)
	require.NoError(t, err)
	second, err := secondCache.compile("^value$", regexp.Compile)
	require.NoError(t, err)

	assert.NotSame(t, first, second)
	assert.Len(t, firstCache.entries, 1)
	assert.Len(t, secondCache.entries, 1)
}

func TestRegexSearchInvalidPatternKeepsFatalError(t *testing.T) {
	search := newScriggoRegexSearch()
	env := &regexCacheEnv{}

	assert.False(t, search(env, "value", "["))
	require.Len(t, env.fatalValues, 1)
	fatalErr, ok := env.fatalValues[0].(error)
	require.True(t, ok)
	assert.Equal(t, "regex_search: invalid pattern \"[\": error parsing regexp: missing closing ]: `[`", fatalErr.Error())
}

func TestRegexSearchCachePreservesTemplateResults(t *testing.T) {
	tests := []struct {
		name     string
		template string
		expected string
	}{
		{name: "substring", template: `{{ regex_search("prefix-value-suffix", "value") }}`, expected: "true\n"},
		{name: "anchored", template: `{{ regex_search("prefix-value", "^value$") }}`, expected: "false\n"},
		{name: "empty", template: `{{ regex_search("", "") }}`, expected: "true\n"},
		{name: "unicode", template: `{{ regex_search("Grüße", "üß") }}`, expected: "true\n"},
		{name: "lenient coercion", template: `{{ regex_search(12345, 234) }} {{ regex_search(true, "ru") }}`, expected: "true true\n"},
		{name: "regexp identity remains fresh", template: `{{ regexp("value") == regexp("value") }}`, expected: "false\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, err := New(map[string]string{"test": tt.template}, &Options{EntryPoints: []string{"test"}})
			require.NoError(t, err)

			for range 2 {
				output, renderErr := engine.Render(context.Background(), "test", nil)
				require.NoError(t, renderErr)
				assert.Equal(t, tt.expected, output)
			}
		})
	}
}

func TestRegexSearchCanBeOverridden(t *testing.T) {
	engine, err := New(map[string]string{
		"test": `{{ regex_search() }}`,
	}, &Options{
		EntryPoints: []string{"test"},
		Functions: map[string]GlobalFunc{
			FuncRegexSearch: func(...any) (any, error) { return "custom-search", nil },
		},
	})
	require.NoError(t, err)

	output, err := engine.Render(context.Background(), "test", nil)
	require.NoError(t, err)
	assert.Equal(t, "custom-search\n", output)
}
