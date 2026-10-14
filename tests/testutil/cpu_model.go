// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"slices"
	"strings"
)

// CPUModel identifies all processor models in Linux cpuinfo, or returns empty for incomplete evidence.
func CPUModel(cpuinfo []byte) string {
	models := make(map[string]struct{})
	for _, block := range strings.Split(strings.TrimSpace(string(cpuinfo)), "\n\n") {
		fields := make(map[string]string)
		for _, line := range strings.Split(block, "\n") {
			if key, value, found := strings.Cut(line, ":"); found {
				fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
			}
		}
		if _, processor := fields["processor"]; !processor {
			continue
		}
		model := fields["model name"]
		if model == "" {
			var parts []string
			for _, key := range []string{"CPU implementer", "CPU architecture", "CPU variant", "CPU part", "CPU revision"} {
				if fields[key] == "" {
					return ""
				}
				parts = append(parts, key+"="+fields[key])
			}
			model = strings.Join(parts, ", ")
		}
		models[model] = struct{}{}
	}
	result := make([]string, 0, len(models))
	for model := range models {
		result = append(result, model)
	}
	slices.Sort(result)
	return strings.Join(result, "; ")
}
