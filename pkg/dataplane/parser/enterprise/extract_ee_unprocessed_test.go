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

	clientparser "github.com/haproxytech/client-native/v6/config-parser"
	"github.com/haproxytech/client-native/v6/config-parser/parsers/extra"
	"github.com/stretchr/testify/assert"
)

// getUnprocessedLines is the read-side adapter that every EE section
// extractor (extractWAFGlobalFields, extractCaptchaFields, etc.)
// calls to pull raw directive lines out of client-native's
// UnProcessed parser bucket.
//
// Pin every defensive branch — these guards are how the function
// avoids panics when client-native returns unexpected shapes:
//   - nil parsers wrapper      → nil
//   - nil Parsers map          → nil
//   - missing "" key           → nil (UnProcessed registers under "")
//   - "" key is wrong type     → nil (defensive type assertion)
//   - UnProcessed has no data  → nil (Get returns ErrFetch)
//   - happy path               → []string of trimmed values, in order
func TestGetUnprocessedLines(t *testing.T) {
	t.Run("nil parsers wrapper returns nil", func(t *testing.T) {
		assert.Nil(t, getUnprocessedLines(nil))
	})

	t.Run("nil Parsers map returns nil", func(t *testing.T) {
		ps := &clientparser.Parsers{Parsers: nil}
		assert.Nil(t, getUnprocessedLines(ps))
	})

	t.Run("missing \"\" key returns nil", func(t *testing.T) {
		ps := &clientparser.Parsers{
			Parsers: map[string]clientparser.ParserInterface{
				"some-other-key": &extra.UnProcessed{},
			},
		}
		assert.Nil(t, getUnprocessedLines(ps))
	})

	t.Run("\"\" key holding wrong ParserInterface type returns nil", func(t *testing.T) {
		// The defensive type assertion `parser.(*extra.UnProcessed)`
		// must convert any non-UnProcessed parser at the "" slot to
		// a nil result rather than panic. Use extra.Section as a
		// stand-in for any other ParserInterface implementation.
		ps := &clientparser.Parsers{
			Parsers: map[string]clientparser.ParserInterface{
				"": &extra.Section{},
			},
		}
		assert.Nil(t, getUnprocessedLines(ps))
	})

	t.Run("UnProcessed bucket with no data returns nil", func(t *testing.T) {
		// Get returns (nil, ErrFetch) when len(data)==0; the function
		// must convert that to a nil result rather than propagating
		// the error.
		u := &extra.UnProcessed{}
		u.Init()
		ps := &clientparser.Parsers{
			Parsers: map[string]clientparser.ParserInterface{"": u},
		}
		assert.Nil(t, getUnprocessedLines(ps))
	})

	t.Run("happy path: returns trimmed values in original order", func(t *testing.T) {
		u := &extra.UnProcessed{}
		u.Init()
		// Parse trims surrounding whitespace, so feed lines with
		// padding to verify the trim flows through.
		_, _ = u.Parse("  waf-load /etc/waf.conf  ", nil, "")
		_, _ = u.Parse("module-load extension.so", nil, "")
		_, _ = u.Parse("\tmaxmind-load /etc/geoip\t", nil, "")

		ps := &clientparser.Parsers{
			Parsers: map[string]clientparser.ParserInterface{"": u},
		}

		got := getUnprocessedLines(ps)
		assert.Equal(t, []string{
			"waf-load /etc/waf.conf",
			"module-load extension.so",
			"maxmind-load /etc/geoip",
		}, got)
	})
}
