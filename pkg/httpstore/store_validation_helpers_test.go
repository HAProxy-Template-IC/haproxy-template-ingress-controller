// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package httpstore

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// checksumPrefix is a tiny pure helper that returns the first 16 characters
// of a checksum followed by "..." for compact log output. The contract:
//   - prefix is at most 16 characters of the input
//   - "..." is always appended (even when the input is shorter than 16)
//
// Pin both branches so a future refactor can't quietly change log
// fingerprints (operator-facing during validation incidents).
func TestChecksumPrefix(t *testing.T) {
	tests := []struct {
		name     string
		checksum string
		want     string
	}{
		{
			name:     "long checksum is truncated to 16 chars + ellipsis",
			checksum: "abcdef0123456789aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			want:     "abcdef0123456789...",
		},
		{
			name:     "exactly 16 chars get the ellipsis appended without truncation",
			checksum: "abcdef0123456789",
			want:     "abcdef0123456789...",
		},
		{
			name:     "shorter than 16 chars gets the ellipsis appended (no panic on small input)",
			checksum: "abc",
			want:     "abc...",
		},
		{
			name:     "single character",
			checksum: "x",
			want:     "x...",
		},
		{
			name:     "empty input still gets the ellipsis (the bare-minimum log marker)",
			checksum: "",
			want:     "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checksumPrefix(tt.checksum)
			assert.Equal(t, tt.want, got)
			// Cross-invariant: the ellipsis is always present.
			assert.True(t, strings.HasSuffix(got, "..."), "result must end in ellipsis")
		})
	}
}
