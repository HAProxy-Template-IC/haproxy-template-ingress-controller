// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpgradeBaselinesPreserveReleasedAndPrereleaseCoverage(t *testing.T) {
	tags := []string{"0.1.0", "0.2.0-alpha.3", "0.2.0", "0.2.1", "0.2.2", "0.2.10", "0.3.0-rc.1", "0.3.0", "main-abc123"}
	for _, tt := range []struct {
		target string
		want   []string
	}{
		{"0.2.2", []string{"0.1.0", "0.2.0", "0.2.1", "0.2.2", "0.2.0-alpha.3"}},
		{"0.2.3", []string{"0.1.0", "0.2.0", "0.2.1", "0.2.2", "0.2.0-alpha.3"}},
		{"0.2.10", []string{"0.1.0", "0.2.0", "0.2.1", "0.2.2", "0.2.10", "0.2.0-alpha.3"}},
		{"0.3.0-alpha.1", []string{"0.1.0", "0.2.0", "0.2.1", "0.2.2", "0.2.10", "0.2.0-alpha.3"}},
		{"0.3.0", []string{"0.1.0", "0.2.0", "0.2.1", "0.2.2", "0.2.10", "0.3.0", "0.2.0-alpha.3"}},
	} {
		t.Run(tt.target, func(t *testing.T) {
			got, err := UpgradeBaselines(tags, tt.target)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
	_, err := UpgradeBaselines([]string{"0.1.0", "0.2.0"}, "0.2.2")
	require.ErrorContains(t, err, "0.2.0-alpha.3")
	_, err = UpgradeBaselines([]string{"0.2.0-alpha.3"}, "0.2.2")
	require.ErrorContains(t, err, "no published stable")
	_, err = UpgradeBaselines(tags, "dev")
	require.ErrorContains(t, err, "not semver")
}

func TestUpgradeMatrixRequiresExactCoverage(t *testing.T) {
	content := []byte("test-chart-upgrade:\n  parallel:\n    matrix:\n    - BASELINE_CHART_VERSION: [0.1.0, 0.2.0-alpha.3]\n")
	require.NoError(t, CheckUpgradeMatrix([]string{"0.1.0", "0.2.0-alpha.3"}, content))
	require.ErrorContains(t, CheckUpgradeMatrix([]string{"0.1.0"}, content), "unknown=[0.2.0-alpha.3]")
	require.ErrorContains(t, CheckUpgradeMatrix([]string{"0.1.0", "0.2.0-alpha.3", "0.4.1"}, content), "missing=[0.4.1]")
	require.Error(t, CheckUpgradeMatrix(nil, []byte("{}")))
}
