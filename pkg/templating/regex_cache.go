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
	"regexp/syntax"
	"strings"
	"sync"
)

const (
	regexSearchCacheEntries   = 64
	regexCachePatternMaxBytes = 256
	regexCacheMaxComplexity   = 256
)

type regexCacheEntry[T any] struct {
	compiled         T
	compileEveryTime bool
}

type boundedRegexCache[T any] struct {
	mu         sync.RWMutex
	maxEntries int
	entries    map[string]regexCacheEntry[T]
}

func (c *boundedRegexCache[T]) compile(pattern string, compile func(string) (T, error)) (T, error) {
	if len(pattern) > regexCachePatternMaxBytes {
		return compile(pattern)
	}

	c.mu.RLock()
	cached, ok := c.entries[pattern]
	full := len(c.entries) >= c.maxEntries
	c.mu.RUnlock()
	if ok {
		if cached.compileEveryTime {
			return compile(pattern)
		}
		return cached.compiled, nil
	}
	if full {
		return compile(pattern)
	}

	// regexp.Regexp retains its input expression, so the cache must own it.
	ownedPattern := strings.Clone(pattern)
	compiled, err := compile(ownedPattern)
	if err != nil {
		var zero T
		return zero, err
	}
	// Cost the unsimplified AST because simplification expands counted repeats.
	cacheable := regexCacheable(ownedPattern)

	c.mu.Lock()
	defer c.mu.Unlock()
	if cached, ok = c.entries[pattern]; ok {
		if cached.compileEveryTime {
			return compiled, nil
		}
		return cached.compiled, nil
	}
	if len(c.entries) < c.maxEntries {
		if c.entries == nil {
			c.entries = make(map[string]regexCacheEntry[T])
		}
		entry := regexCacheEntry[T]{compiled: compiled}
		if !cacheable {
			// Avoid reparsing repeated misses without retaining the compiled program.
			entry = regexCacheEntry[T]{compileEveryTime: true}
		}
		c.entries[ownedPattern] = entry
	}
	return compiled, nil
}

func regexCacheable(pattern string) bool {
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return false
	}
	return regexComplexity(parsed, regexCacheMaxComplexity) <= regexCacheMaxComplexity
}

func regexComplexity(expr *syntax.Regexp, limit int) int {
	childCost := func() int {
		if len(expr.Sub) == 0 {
			return 0
		}
		return regexComplexity(expr.Sub[0], limit)
	}

	switch expr.Op {
	case syntax.OpNoMatch, syntax.OpEmptyMatch,
		syntax.OpBeginLine, syntax.OpEndLine,
		syntax.OpBeginText, syntax.OpEndText,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary,
		syntax.OpAnyCharNotNL, syntax.OpAnyChar:
		return 1
	case syntax.OpLiteral:
		return max(1, cappedMultiply(2, len(expr.Rune), limit))
	case syntax.OpCharClass:
		return cappedAdd(1, len(expr.Rune), limit)
	case syntax.OpCapture:
		return cappedAdd(childCost(), 2, limit)
	case syntax.OpConcat, syntax.OpAlternate:
		cost := 0
		for _, child := range expr.Sub {
			cost = cappedAdd(cost, regexComplexity(child, limit), limit)
		}
		if expr.Op == syntax.OpAlternate && len(expr.Sub) > 1 {
			cost = cappedAdd(cost, len(expr.Sub)-1, limit)
		}
		return cost
	case syntax.OpStar:
		return cappedAdd(childCost(), 2, limit)
	case syntax.OpPlus, syntax.OpQuest:
		return cappedAdd(childCost(), 1, limit)
	case syntax.OpRepeat:
		cost := childCost()
		if expr.Max < 0 {
			if expr.Min == 0 {
				return cappedAdd(cost, 2, limit)
			}
			return cappedAdd(cappedMultiply(cost, expr.Min, limit), 1, limit)
		}
		return cappedAdd(cappedMultiply(cost, expr.Max, limit), expr.Max-expr.Min, limit)
	default:
		return limit + 1
	}
}

func cappedAdd(a, b, limit int) int {
	if a > limit || b > limit || a > limit-b {
		return limit + 1
	}
	return a + b
}

func cappedMultiply(a, b, limit int) int {
	if a == 0 || b == 0 {
		return 0
	}
	if a > limit || b > limit/a {
		return limit + 1
	}
	return a * b
}
