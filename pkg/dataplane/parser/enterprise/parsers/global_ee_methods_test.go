// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package parsers

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cnerrors "github.com/haproxytech/client-native/v6/config-parser/errors"
)

// isEEGlobalDirective is the gate that decides whether a directive is
// EE-specific (intercepted by the GlobalEE parser) or standard CE
// (delegated to client-native). Pin every accepted directive plus
// representative rejected ones — adding a new EE directive without
// updating this set would silently route it to client-native and
// surface as an opaque parse error.
func TestIsEEGlobalDirective(t *testing.T) {
	tests := []struct {
		name      string
		directive string
		want      bool
	}{
		// Accepted (EE-specific)
		{name: "maxmind-load", directive: "maxmind-load", want: true},
		{name: "maxmind-update", directive: "maxmind-update", want: true},
		{name: "maxmind-cache-size", directive: "maxmind-cache-size", want: true},
		{name: "device-atlas-log-level", directive: "device-atlas-log-level", want: true},
		{name: "device-atlas-json-file", directive: "device-atlas-json-file", want: true},
		{name: "device-atlas-properties-cookie", directive: "device-atlas-properties-cookie", want: true},
		{name: "waf-load", directive: "waf-load", want: true},
		{name: "module-load", directive: "module-load", want: true},

		// Rejected (CE — delegated to client-native)
		{name: "daemon (CE)", directive: "daemon", want: false},
		{name: "log (CE)", directive: "log", want: false},
		{name: "maxconn (CE)", directive: "maxconn", want: false},
		{name: "empty string", directive: "", want: false},
		{name: "case-sensitive: WAF-LOAD is rejected", directive: "WAF-LOAD", want: false},
		{name: "trimmed: ' waf-load' (with leading space) is rejected", directive: " waf-load", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isEEGlobalDirective(tt.directive))
		})
	}
}

// GlobalEE.GetParserName returns "global-ee" — pin to prevent
// collision with the standard "global" parser registered by
// client-native.
func TestGlobalEE_GetParserName(t *testing.T) {
	p := NewGlobalEE()
	assert.Equal(t, "global-ee", p.GetParserName())
}

// GlobalEE.Init resets every internal field so the parser can be
// reused across config reloads. Pin that any previously-stored
// directives and pre-comments are cleared.
func TestGlobalEE_Init_ClearsState(t *testing.T) {
	p := NewGlobalEE()
	require.NoError(t, p.Insert(&EEGlobalDirective{Type: "waf-load"}, 0))
	p.SetPreComments([]string{"# old comment"})

	p.Init()

	gotDirectives, _, err := p.ResultAll()
	require.NoError(t, err)
	assert.Empty(t, gotDirectives, "Init must clear stored directives")

	preComments, err := p.GetPreComments()
	require.NoError(t, err)
	assert.Nil(t, preComments, "Init must clear stored pre-comments")
}

// CRUD round-trip: Insert, GetOne, Set, Delete, ResultAll all need to
// agree about ordering and the parser-data type guard.
func TestGlobalEE_CRUDRoundTrip(t *testing.T) {
	p := NewGlobalEE()

	// Insert with index >= len appends to the end.
	d1 := &EEGlobalDirective{Type: "maxmind-load", Parts: []string{"/etc/geoip"}}
	require.NoError(t, p.Insert(d1, 999))

	// Insert with index <= 0 prepends.
	d0 := &EEGlobalDirective{Type: "waf-load", Parts: []string{"/etc/waf.conf"}}
	require.NoError(t, p.Insert(d0, 0))

	// Insert in the middle.
	dMid := &EEGlobalDirective{Type: "module-load", Parts: []string{"foo"}}
	require.NoError(t, p.Insert(dMid, 1))

	// Order is now: [waf-load, module-load, maxmind-load]
	got, err := p.GetOne(0)
	require.NoError(t, err)
	assert.Equal(t, "waf-load", got.(*EEGlobalDirective).Type)

	got, err = p.GetOne(1)
	require.NoError(t, err)
	assert.Equal(t, "module-load", got.(*EEGlobalDirective).Type)

	got, err = p.GetOne(2)
	require.NoError(t, err)
	assert.Equal(t, "maxmind-load", got.(*EEGlobalDirective).Type)

	// GetOne out of range returns ErrFetch.
	_, err = p.GetOne(3)
	assert.True(t, errors.Is(err, cnerrors.ErrFetch), "GetOne out of range must return ErrFetch")
	_, err = p.GetOne(-1)
	assert.True(t, errors.Is(err, cnerrors.ErrFetch), "GetOne negative index must return ErrFetch")

	// Set replaces in place.
	dReplace := &EEGlobalDirective{Type: "device-atlas-json-file", Parts: []string{"/atlas.json"}}
	require.NoError(t, p.Set(dReplace, 1))
	got, _ = p.GetOne(1)
	assert.Equal(t, "device-atlas-json-file", got.(*EEGlobalDirective).Type)

	require.Error(t, p.Set("not a directive", 1), "Set must reject non-EEGlobalDirective data")

	require.Error(t, p.Set(dReplace, 99), "Set must reject out-of-range index")
	require.Error(t, p.Set(dReplace, -1), "Set must reject negative index")

	require.Error(t, p.Insert("not a directive", 0), "Insert must reject non-EEGlobalDirective data")

	// Delete in the middle.
	require.NoError(t, p.Delete(1))
	// Order is now: [waf-load, maxmind-load]
	got, _ = p.GetOne(0)
	assert.Equal(t, "waf-load", got.(*EEGlobalDirective).Type)
	got, _ = p.GetOne(1)
	assert.Equal(t, "maxmind-load", got.(*EEGlobalDirective).Type)

	// Delete out of range is a silent no-op.
	require.NoError(t, p.Delete(99), "Delete out of range must be a no-op (no error)")
	require.NoError(t, p.Delete(-1), "Delete negative index must be a no-op")
}

// ResultAll serializes directives back to client-native ReturnResultLine
// shape: line = "<type> <part1> <part2>...", comment carried verbatim.
func TestGlobalEE_ResultAll(t *testing.T) {
	p := NewGlobalEE()

	require.NoError(t, p.Insert(&EEGlobalDirective{
		Type: "waf-load", Parts: []string{"/etc/waf.conf"}, Comment: "production",
	}, 0))
	require.NoError(t, p.Insert(&EEGlobalDirective{
		Type: "maxmind-load", Parts: []string{"/etc/geoip"},
	}, 1))
	// Type-only directive (no parts).
	require.NoError(t, p.Insert(&EEGlobalDirective{Type: "module-load"}, 2))

	p.SetPreComments([]string{"# header"})

	results, preComments, err := p.ResultAll()
	require.NoError(t, err)

	require.Len(t, results, 3)
	assert.Equal(t, "waf-load /etc/waf.conf", results[0].Data)
	assert.Equal(t, "production", results[0].Comment)

	assert.Equal(t, "maxmind-load /etc/geoip", results[1].Data)
	assert.Empty(t, results[1].Comment)

	assert.Equal(t, "module-load", results[2].Data, "type-only directive must serialize without trailing space")
	assert.Empty(t, results[2].Comment)

	assert.Equal(t, []string{"# header"}, preComments)
}
