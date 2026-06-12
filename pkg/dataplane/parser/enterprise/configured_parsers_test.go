// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package enterprise

import (
	"testing"

	parser "github.com/haproxytech/client-native/v6/config-parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// NewConfiguredParsers must pre-allocate every per-section map so the
// reader can write to them unconditionally without nil-checks. Pin
// every CE and EE section map plus the singleton fields (which start
// nil — they're populated lazily by getSingletonParsers).
func TestNewConfiguredParsers_AllSectionMapsPreAllocated(t *testing.T) {
	c := NewConfiguredParsers()
	require.NotNil(t, c)

	t.Run("CE named-section maps are pre-allocated and empty", func(t *testing.T) {
		ceMaps := map[string]map[string]*parser.Parsers{
			"Frontend":   c.Frontend,
			"Backend":    c.Backend,
			"Listen":     c.Listen,
			"Resolvers":  c.Resolvers,
			"Peers":      c.Peers,
			"Mailers":    c.Mailers,
			"Cache":      c.Cache,
			"Program":    c.Program,
			"HTTPErrors": c.HTTPErrors,
			"Ring":       c.Ring,
			"LogForward": c.LogForward,
			"FCGIApp":    c.FCGIApp,
			"CrtStore":   c.CrtStore,
			"Traces":     c.Traces,
			"LogProfile": c.LogProfile,
			"ACME":       c.ACME,
			"Userlist":   c.Userlist,
		}
		for name, m := range ceMaps {
			assert.NotNil(t, m, "%s map must be pre-allocated", name)
			assert.Empty(t, m, "%s map must start empty", name)
		}
	})

	t.Run("EE named-section maps are pre-allocated and empty", func(t *testing.T) {
		eeMaps := map[string]map[string]*parser.Parsers{
			"WAFProfile":     c.WAFProfile,
			"BotMgmtProfile": c.BotMgmtProfile,
			"Captcha":        c.Captcha,
			"UDPLB":          c.UDPLB,
			"DynamicUpdate":  c.DynamicUpdate,
		}
		for name, m := range eeMaps {
			assert.NotNil(t, m, "%s map must be pre-allocated", name)
			assert.Empty(t, m, "%s map must start empty", name)
		}
	})

	t.Run("singleton fields start nil (populated lazily)", func(t *testing.T) {
		// Populated lazily by getSingletonParsers when the parser
		// encounters the corresponding section header.
		assert.Nil(t, c.Global)
		assert.Nil(t, c.Defaults)
		assert.Nil(t, c.WAFGlobal)
		assert.Nil(t, c.Comments)
	})

	t.Run("State / SectionName / Active start zero", func(t *testing.T) {
		assert.Equal(t, Section(""), c.State)
		assert.Empty(t, c.SectionName)
		assert.Nil(t, c.Active)
	})
}

// SetState must atomically set the current section, name, and active
// parser collection in one call so the reader can switch context
// without intermediate inconsistent states.
func TestConfiguredParsers_SetState(t *testing.T) {
	c := NewConfiguredParsers()
	dummy := &parser.Parsers{}

	c.SetState(SectionFrontend, "http", dummy)

	assert.Equal(t, SectionFrontend, c.State)
	assert.Equal(t, "http", c.SectionName)
	assert.Same(t, dummy, c.Active)

	// A second SetState call replaces all three.
	dummy2 := &parser.Parsers{}
	c.SetState(SectionBackend, "api", dummy2)
	assert.Equal(t, SectionBackend, c.State)
	assert.Equal(t, "api", c.SectionName)
	assert.Same(t, dummy2, c.Active)
}

// getOrCreate is the shared lazy-initialiser the per-section
// dispatchers funnel through. The contract: existing entries are
// returned; missing entries trigger the create function exactly once
// and are cached. Pin both branches plus the cache invariant so a
// future refactor can't accidentally re-create on every call.
func TestConfiguredParsers_GetOrCreate(t *testing.T) {
	c := NewConfiguredParsers()

	// Use the Frontend map as a representative — the helper is generic
	// over the map argument, so the same contract applies to every
	// per-section map.

	t.Run("missing key triggers create exactly once", func(t *testing.T) {
		var calls int
		create := func() *parser.Parsers {
			calls++
			return &parser.Parsers{}
		}

		got := c.getOrCreate(c.Frontend, "http", create)
		require.NotNil(t, got)
		assert.Equal(t, 1, calls, "first call must invoke create")

		// Second call with the same key must NOT re-create.
		got2 := c.getOrCreate(c.Frontend, "http", create)
		assert.Same(t, got, got2, "subsequent calls must return the cached entry")
		assert.Equal(t, 1, calls, "create must NOT be invoked again")
	})

	t.Run("different keys get independent entries", func(t *testing.T) {
		gotA := c.getOrCreate(c.Backend, "a", func() *parser.Parsers { return &parser.Parsers{} })
		gotB := c.getOrCreate(c.Backend, "b", func() *parser.Parsers { return &parser.Parsers{} })

		assert.NotSame(t, gotA, gotB, "different keys must return different entries")
	})
}
