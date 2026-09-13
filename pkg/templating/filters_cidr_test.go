// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package templating

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScriggoCidrPartition(t *testing.T) {
	tests := []struct {
		name string
		in   []any
		want map[string][]string
	}{
		{
			name: "disjoint prefixes are their own blocks",
			in:   []any{"10.0.0.0/8", "192.168.0.0/16"},
			want: map[string][]string{
				"10.0.0.0/8":     {"10.0.0.0/8"},
				"192.168.0.0/16": {"192.168.0.0/16"},
			},
		},
		{
			name: "a nested prefix splits the outer one into blocks",
			in:   []any{"10.0.0.0/8", "10.1.0.0/16"},
			want: map[string][]string{
				"10.0.0.0/8": {
					"10.0.0.0/16", "10.1.0.0/16", "10.128.0.0/9", "10.16.0.0/12",
					"10.2.0.0/15", "10.32.0.0/11", "10.4.0.0/14", "10.64.0.0/10", "10.8.0.0/13",
				},
				"10.1.0.0/16": {"10.1.0.0/16"},
			},
		},
		{
			name: "a bare address is a host block and the outer prefix keeps it",
			in:   []any{"10.1.2.3", "10.1.2.0/30"},
			want: map[string][]string{
				"10.1.2.3":    {"10.1.2.3/32"},
				"10.1.2.0/30": {"10.1.2.0/31", "10.1.2.2/32", "10.1.2.3/32"},
			},
		},
		{
			name: "host bits are masked and duplicates share blocks",
			in:   []any{"10.1.2.3/8", "10.0.0.0/8"},
			want: map[string][]string{
				"10.1.2.3/8": {"10.0.0.0/8"},
				"10.0.0.0/8": {"10.0.0.0/8"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := scriggoCidrPartition(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			requireDisjointCover(t, got)
		})
	}

	t.Run("IPv6 nests like IPv4 and families never split each other", func(t *testing.T) {
		got, err := scriggoCidrPartition([]any{
			"2001:db8::/32", "2001:db8:1::/48", "10.0.0.0/8", "::ffff:10.1.0.0/112",
		})
		require.NoError(t, err)
		requireDisjointCover(t, got)
		assert.Len(t, got["2001:db8::/32"], 17)
		assert.Contains(t, got["2001:db8::/32"], "2001:db8:1::/48")
		assert.Equal(t, []string{"2001:db8:1::/48"}, got["2001:db8:1::/48"])
		assert.Len(t, got["10.0.0.0/8"], 9)
		assert.Equal(t, []string{"10.1.0.0/16"}, got["::ffff:10.1.0.0/112"])
	})

	t.Run("invalid input fails", func(t *testing.T) {
		_, err := scriggoCidrPartition([]any{"10.0.0.0/8", "not-a-cidr"})
		require.ErrorContains(t, err, "cidr_partition")
		_, err = scriggoCidrPartition([]any{"::ffff:10.0.0.0/64"})
		require.ErrorContains(t, err, "not an IPv4 prefix")
	})
}

// requireDisjointCover checks that no two blocks overlap and that every input
// prefix is exactly the union of its blocks.
func requireDisjointCover(t *testing.T, partition map[string][]string) {
	t.Helper()
	var blocks []netip.Prefix
	seen := map[string]bool{}
	for _, names := range partition {
		for _, name := range names {
			if seen[name] {
				continue
			}
			seen[name] = true
			blocks = append(blocks, netip.MustParsePrefix(name))
		}
	}
	for i, left := range blocks {
		for _, right := range blocks[i+1:] {
			if left.Addr().Is4() != right.Addr().Is4() {
				continue
			}
			require.Falsef(t, left.Overlaps(right), "blocks %s and %s overlap", left, right)
		}
	}
	for raw, names := range partition {
		prefix, err := parseCidrPrefix(raw)
		require.NoError(t, err)
		covered := 0.0
		for _, name := range names {
			block := netip.MustParsePrefix(name)
			require.Truef(t, prefix.Contains(block.Addr()) && block.Bits() >= prefix.Bits(),
				"block %s is outside %s", block, prefix)
			covered += 1 / float64(uint64(1)<<uint(block.Bits()-prefix.Bits()))
		}
		require.InDeltaf(t, 1.0, covered, 1e-12, "blocks of %s cover %v of it", prefix, covered)
	}
}
