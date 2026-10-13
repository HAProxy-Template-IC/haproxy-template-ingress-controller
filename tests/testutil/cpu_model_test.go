// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCPUModel(t *testing.T) {
	const arm = "processor\t: 0\nCPU implementer\t: 0x41\nCPU architecture: 8\nCPU variant: 0x1\nCPU part: 0xd49\nCPU revision: 0\n"
	const armModel = "CPU implementer=0x41, CPU architecture=8, CPU variant=0x1, CPU part=0xd49, CPU revision=0"
	const x86 = "processor: 0\nmodel name: AMD EPYC 7763 64-Core Processor\ncpu MHz: 2445.420\n"
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "x86 model unchanged", input: x86, want: "AMD EPYC 7763 64-Core Processor"},
		{name: "arm identity", input: arm, want: armModel},
		{name: "repeated arm cores", input: arm + "\n" + strings.Replace(arm, "0\n", "1\n", 1), want: armModel},
		{name: "heterogeneous arm cores", input: strings.Replace(arm, "0xd49", "0xd4f", 1) + "\n" + arm,
			want: armModel + "; " + strings.Replace(armModel, "0xd49", "0xd4f", 1)},
		{name: "incomplete arm core", input: strings.ReplaceAll(arm, "CPU part: 0xd49\n", "")},
		{name: "incomplete second core", input: arm + "\nprocessor: 1\nCPU architecture: 8\n"},
		{name: "unidentified architecture", input: "processor: 0\nFeatures: fp asimd\n"},
		{name: "hardware summary ignored", input: arm + "\nHardware: Virtual Machine\nSerial: 123\n", want: armModel},
		{name: "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, CPUModel([]byte(tt.input)))
		})
	}
}
