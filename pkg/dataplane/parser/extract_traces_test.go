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

// extractTraces is reached via Parser.ParseFromString → conf.Traces.
// It has three branches:
//
//  1. SectionExists(Traces, "") returns false → return nil
//     (the config has no `traces` section, which is the OVERWHELMING
//     common case — traces is a HAProxy 3.1+ debugging feature
//     that operators rarely enable in production).
//  2. SectionExists is true and ParseTraces fails → log + return nil
//     (defensive fallback; hard to trigger from a syntactically valid
//     config so this case is left to the upstream client-native
//     library's own tests).
//  3. SectionExists is true and ParseTraces succeeds → return the
//     populated *models.Traces struct (the v3.1+ tracing happy path).
//
// Existing parser tests cover plenty of OTHER sections via
// ParseFromString, but none assert on conf.Traces specifically. The
// uncovered branches matter because:
//
//   - Case (1) is the contract that lets template authors compile
//     configs WITHOUT a traces section without provoking a nil-deref
//     downstream. A regression that returned a zero-value
//     *models.Traces instead of nil would break every consumer that
//     uses `if conf.Traces != nil` to guard tracing-aware logic.
//
//   - Case (3) is the contract that traces config — when authored —
//     actually flows through to the structured config. A regression
//     that swapped the section name argument or stopped calling
//     ParseTraces would silently strip every operator's tracing
//     config and the v3.1+ trace feature would appear broken.
//
// These two cases pin the contract end-to-end via ParseFromString.
func TestParseFromString_TracesField(t *testing.T) {
	tests := []struct {
		name      string
		config    string
		wantNil   bool
		wantTrace bool // true → at least one Trace entry expected in the result
	}{
		{
			name: "no traces section — Traces field is nil",
			config: `
global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s
`,
			wantNil: true,
		},
		{
			name: "traces section present — Traces field is populated",
			config: `
global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s

traces
    trace h1 sink buf1 level developer verbosity complete start now
`,
			wantNil:   false,
			wantTrace: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestParser(t)
			conf, err := p.ParseFromString(tt.config)
			require.NoError(t, err)
			require.NotNil(t, conf)

			if tt.wantNil {
				assert.Nil(t, conf.Traces,
					"Traces MUST be nil when the source config has no traces "+
						"section — every consumer guards tracing-aware logic with "+
						"`if conf.Traces != nil`, so a regression that returned a "+
						"zero-value struct here would break that guard everywhere")
				return
			}

			require.NotNil(t, conf.Traces,
				"Traces MUST be non-nil when the source config DOES contain a "+
					"traces section — a regression that stripped the section here "+
					"would silently break v3.1+ trace configuration end-to-end")

			if tt.wantTrace {
				assert.NotEmpty(t, conf.Traces.Entries,
					"Traces.Entries MUST contain the parsed trace lines — "+
						"otherwise traces-section text reaches the parser but "+
						"the actual trace directives are silently dropped")
			}
		})
	}
}
