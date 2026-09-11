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
	"fmt"
	"strconv"
	"strings"
)

// VerifyVarnishMemoryResidency rejects missing evidence or pageable shared-memory mappings.
func VerifyVarnishMemoryResidency(values map[string]string) (string, error) {
	vslBytes, err := parseVarnishMemorySize(values["vsl_space"])
	if err != nil {
		return "", err
	}
	numbers := map[string]uint64{}
	for _, key := range []string{"shared_size_kib", "shared_rss_kib", "shared_locked_kib", "shared_swap_kib", "node_swap_devices"} {
		value, parseErr := strconv.ParseUint(values[key], 10, 64)
		if parseErr != nil {
			return "", fmt.Errorf("invalid %s evidence %q: %w", key, values[key], parseErr)
		}
		numbers[key] = value
	}
	mapped, resident, locked := numbers["shared_size_kib"], numbers["shared_rss_kib"], numbers["shared_locked_kib"]
	if mapped < (vslBytes-1)/1024+1 || resident > mapped || locked > resident {
		return "", fmt.Errorf("inconsistent shared-memory mappings: size=%d KiB, resident=%d KiB, locked=%d KiB, VSL=%d bytes", mapped, resident, locked, vslBytes)
	}
	if numbers["shared_swap_kib"] != 0 {
		return "", fmt.Errorf("shared memory for Varnish has %d KiB swapped out", numbers["shared_swap_kib"])
	}
	if values["mount_noswap"] != "true" && values["mount_noswap"] != "false" {
		return "", fmt.Errorf("invalid noswap mount evidence %q", values["mount_noswap"])
	}
	switch {
	case locked == mapped:
		return "mlock", nil
	case values["mount_noswap"] == "true":
		return "tmpfs-noswap", nil
	case numbers["node_swap_devices"] == 0:
		return "node-noswap", nil
	case values["cgroup_swap_max"] == "0" && values["cgroup_swap_current"] == "0":
		return "cgroup-noswap", nil
	default:
		return "", fmt.Errorf("shared memory for Varnish can page out: %d of %d KiB locked; configure sufficient memlock or disable swapping", locked, mapped)
	}
}

func parseVarnishMemorySize(value string) (uint64, error) {
	if value == "" {
		return 0, fmt.Errorf("missing VSL size")
	}
	shift := uint(0)
	if index := strings.IndexByte("KMGT", value[len(value)-1]); index >= 0 {
		shift = uint(index+1) * 10
		value = value[:len(value)-1]
	}
	amount, err := strconv.ParseUint(value, 10, 64-int(shift))
	if err != nil || amount == 0 {
		return 0, fmt.Errorf("invalid VSL size %q", value)
	}
	return amount << shift, nil
}
