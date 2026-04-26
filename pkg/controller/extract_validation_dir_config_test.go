// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"

	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// extractValidationDirConfig translates the operator-facing dataplane
// directory paths (absolute, with arbitrary nesting) into the four-field
// layout the local validation tooling expects:
//
//   - BaseDir: parent of MapsDir (used as the validation root that the
//     three sibling dirs are placed under)
//   - MapsDir / SSLCertsDir / GeneralDir: just the *basename* of each
//     configured directory
//
// The contract is subtle: BaseDir is derived ONLY from MapsDir, never
// from SSLCertsDir or GeneralStorageDir. If those three configured paths
// share the same parent (the common case), this is invisible — but if a
// future refactor "harmonises" the derivation by, say, taking
// filepath.Dir(SSLCertsDir) or stitching parents together, the
// validation tree silently diverges from the runtime layout and local
// haproxy -c validation passes against a wrong-shaped tree.
//
// These cases pin:
//
//  1. Common case (sibling dirs under /etc/haproxy) — base is the shared
//     parent, basenames are extracted cleanly.
//  2. Mismatched parents — BaseDir tracks MapsDir's parent specifically,
//     ignoring SSLCertsDir/GeneralStorageDir parents. This is the case
//     a "let's compute base from all three" refactor would silently break.
//  3. Single-path dir names (no slash) — filepath.Dir returns "." and
//     filepath.Base returns the input unchanged.
//  4. Trailing slash on MapsDir — filepath.Dir treats the trailing slash
//     as part of the path and returns the dir itself, which is a real
//     foot-gun if operators include a trailing slash in their config.
//  5. Deeply nested MapsDir — only the immediate parent becomes BaseDir,
//     not the root.
func TestExtractValidationDirConfig(t *testing.T) {
	tests := []struct {
		name    string
		in      coreconfig.DataplaneConfig
		want    validationDirConfig
		comment string
	}{
		{
			name: "common case — sibling dirs under /etc/haproxy",
			in: coreconfig.DataplaneConfig{
				MapsDir:           "/etc/haproxy/maps",
				SSLCertsDir:       "/etc/haproxy/ssl",
				GeneralStorageDir: "/etc/haproxy/general",
			},
			want: validationDirConfig{
				BaseDir:     "/etc/haproxy",
				MapsDir:     "maps",
				SSLCertsDir: "ssl",
				GeneralDir:  "general",
			},
		},
		{
			name: "mismatched parents — BaseDir follows MapsDir only",
			in: coreconfig.DataplaneConfig{
				MapsDir:           "/etc/haproxy/maps",
				SSLCertsDir:       "/var/lib/haproxy/certs",
				GeneralStorageDir: "/opt/files",
			},
			want: validationDirConfig{
				BaseDir:     "/etc/haproxy", // ← derived from MapsDir, NOT the others
				MapsDir:     "maps",
				SSLCertsDir: "certs",
				GeneralDir:  "files",
			},
			comment: "regression guard: BaseDir must come from MapsDir's parent, not from SSLCertsDir or GeneralStorageDir",
		},
		{
			name: "bare dir names (no slashes)",
			in: coreconfig.DataplaneConfig{
				MapsDir:           "maps",
				SSLCertsDir:       "ssl",
				GeneralStorageDir: "general",
			},
			want: validationDirConfig{
				BaseDir:     ".", // filepath.Dir("maps") → "."
				MapsDir:     "maps",
				SSLCertsDir: "ssl",
				GeneralDir:  "general",
			},
		},
		{
			name: "trailing slash on MapsDir collapses base to maps itself",
			in: coreconfig.DataplaneConfig{
				MapsDir:           "/etc/haproxy/maps/",
				SSLCertsDir:       "/etc/haproxy/ssl",
				GeneralStorageDir: "/etc/haproxy/general",
			},
			want: validationDirConfig{
				BaseDir:     "/etc/haproxy/maps", // filepath.Dir strips only the trailing /
				MapsDir:     "maps",              // filepath.Base ignores trailing /
				SSLCertsDir: "ssl",
				GeneralDir:  "general",
			},
			comment: "behavioural pin: trailing slash on MapsDir is a foot-gun — BaseDir resolves to MapsDir itself, not its parent",
		},
		{
			name: "deeply nested MapsDir — only the immediate parent becomes BaseDir",
			in: coreconfig.DataplaneConfig{
				MapsDir:           "/srv/haproxy/runtime/etc/maps",
				SSLCertsDir:       "/srv/haproxy/runtime/etc/ssl",
				GeneralStorageDir: "/srv/haproxy/runtime/etc/general",
			},
			want: validationDirConfig{
				BaseDir:     "/srv/haproxy/runtime/etc",
				MapsDir:     "maps",
				SSLCertsDir: "ssl",
				GeneralDir:  "general",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractValidationDirConfig(&tt.in)
			assert.Equal(t, tt.want, got, tt.comment)
		})
	}
}
