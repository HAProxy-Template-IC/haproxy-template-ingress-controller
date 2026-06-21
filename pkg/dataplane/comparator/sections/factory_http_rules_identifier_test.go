// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package sections

import (
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
)

// httpRequestRuleIdentifier and httpResponseRuleIdentifier feed
// describeTypedChild — the helper that decides whether to render a
// parenthesized identifier "(redirect)" or fall back to "at index N".
//
// The two helpers intentionally differ in their nil/empty handling:
//   - httpRequestRuleIdentifier returns "" for nil OR empty type, which
//     causes describeTypedChild to fall back to the "at index N" label.
//   - httpResponseRuleIdentifier returns unknownIdentifier ("<unknown>")
//     for nil OR empty type, because HTTP response rules always have a
//     type in practice — an empty type is itself a signal worth
//     surfacing in the description.
//
// Pin both contracts directly so a future refactor can't accidentally
// align them and silently change which descriptions show "at index N"
// vs "(<unknown>)".
func TestHTTPRequestRuleIdentifier(t *testing.T) {
	tests := []struct {
		name string
		rule *models.HTTPRequestRule
		want string
	}{
		{name: "nil rule returns empty (fall back to index in describer)", rule: nil, want: ""},
		{name: "empty type returns empty (fall back to index)", rule: &models.HTTPRequestRule{Type: ""}, want: ""},
		{name: "set type passes through verbatim", rule: &models.HTTPRequestRule{Type: "redirect"}, want: "redirect"},
		{name: "another type passes through verbatim", rule: &models.HTTPRequestRule{Type: "set-header"}, want: "set-header"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, httpRequestRuleIdentifier(tt.rule))
		})
	}
}

func TestHTTPResponseRuleIdentifier(t *testing.T) {
	tests := []struct {
		name string
		rule *models.HTTPResponseRule
		want string
	}{
		{
			name: "nil rule returns the unknown sentinel (NOT empty)",
			rule: nil,
			want: unknownIdentifier,
		},
		{
			name: "empty type returns the unknown sentinel",
			rule: &models.HTTPResponseRule{Type: ""},
			want: unknownIdentifier,
		},
		{
			name: "set type passes through verbatim",
			rule: &models.HTTPResponseRule{Type: "set-header"},
			want: "set-header",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, httpResponseRuleIdentifier(tt.rule))
		})
	}
}

// Cross-helper invariant: for the SAME nil/empty inputs, the two helpers
// produce DIFFERENT identifier strings. This is the load-bearing
// asymmetry — pin it explicitly so a future refactor can't quietly align
// them.
func TestHTTPRuleIdentifiers_AsymmetryOnEmptyInputs(t *testing.T) {
	assert.NotEqual(t,
		httpRequestRuleIdentifier(nil),
		httpResponseRuleIdentifier(nil),
		"nil-input handling must remain divergent: request->'', response->unknown sentinel",
	)
	assert.NotEqual(t,
		httpRequestRuleIdentifier(&models.HTTPRequestRule{Type: ""}),
		httpResponseRuleIdentifier(&models.HTTPResponseRule{Type: ""}),
		"empty-Type handling must remain divergent: request->'', response->unknown sentinel",
	)
}
