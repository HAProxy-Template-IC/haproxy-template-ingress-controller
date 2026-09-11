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

package testutil

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVarnishMemoryResidencyPolicy(t *testing.T) {
	base := map[string]string{
		"vsl_space": "80M", "shared_size_kib": "81924", "shared_rss_kib": "81924",
		"shared_locked_kib": "4", "shared_swap_kib": "0", "node_swap_devices": "1",
		"mount_noswap": "false", "cgroup_swap_max": "max", "cgroup_swap_current": "0",
	}
	for _, tc := range []struct {
		name    string
		changes map[string]string
		want    string
		wantErr string
	}{
		{name: "pageable tmpfs", wantErr: "can page out"},
		{name: "locked VSL and statistics", changes: map[string]string{"shared_locked_kib": "81924"}, want: "mlock"},
		{name: "log locked but statistics pageable", changes: map[string]string{"shared_locked_kib": "81920"}, wantErr: "can page out"},
		{name: "noswap mount", changes: map[string]string{"mount_noswap": "true"}, want: "tmpfs-noswap"},
		{name: "node without swap", changes: map[string]string{"node_swap_devices": "0"}, want: "node-noswap"},
		{name: "container without swap", changes: map[string]string{"cgroup_swap_max": "0"}, want: "cgroup-noswap"},
		{name: "unreadable container limit", changes: map[string]string{"cgroup_swap_max": "unknown"}, wantErr: "can page out"},
		{name: "container still swapping", changes: map[string]string{"cgroup_swap_max": "0", "cgroup_swap_current": "4096"}, wantErr: "can page out"},
		{name: "existing swapped pages", changes: map[string]string{"mount_noswap": "true", "shared_swap_kib": "1"}, wantErr: "swapped out"},
		{name: "missing mapping", changes: map[string]string{"shared_size_kib": "0"}, wantErr: "inconsistent"},
		{name: "missing resident evidence", changes: map[string]string{"shared_rss_kib": ""}, wantErr: "invalid shared_rss_kib"},
		{name: "invalid mount evidence", changes: map[string]string{"mount_noswap": "unknown"}, wantErr: "invalid noswap"},
		{name: "missing node evidence", changes: map[string]string{"node_swap_devices": ""}, wantErr: "invalid node_swap_devices"},
		{name: "invalid VSL size", changes: map[string]string{"vsl_space": "0M"}, wantErr: "invalid VSL size"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := maps.Clone(base)
			maps.Copy(values, tc.changes)
			got, err := VerifyVarnishMemoryResidency(values)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				require.Empty(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestParseVarnishMemorySize(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  uint64
	}{
		{value: "80M", want: 80 << 20}, {value: "81920K", want: 80 << 20},
		{value: "83886080", want: 80 << 20}, {value: "1G", want: 1 << 30},
		{value: "1T", want: 1 << 40}, {value: "18446744073709551615", want: ^uint64(0)},
	} {
		t.Run(tc.value, func(t *testing.T) {
			got, err := parseVarnishMemorySize(tc.value)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
	for _, value := range []string{"", "0", "-1", "80m", "80MiB", "M", "18446744073709551616", "18446744073709551615M"} {
		t.Run("invalid="+value, func(t *testing.T) {
			_, err := parseVarnishMemorySize(value)
			require.Error(t, err)
		})
	}
}
