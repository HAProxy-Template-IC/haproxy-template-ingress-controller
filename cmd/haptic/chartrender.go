// Copyright 2025 Philipp Hossner
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

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"helm.sh/helm/v4/pkg/chart/common"
	commonutil "helm.sh/helm/v4/pkg/chart/common/util"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	chartv2util "helm.sh/helm/v4/pkg/chart/v2/util"
	"helm.sh/helm/v4/pkg/engine"
)

const (
	// embeddedChartPath is where the Dockerfile copies the bundled Helm chart
	// into the controller image, so a command given no --file / --chart can
	// render it in-process and run against no mounted config at all.
	embeddedChartPath = "/usr/share/haptic/chart"

	// chartDirEnvVar overrides the embedded chart location (between the
	// --chart flag and the built-in default).
	chartDirEnvVar = "HAPTIC_CHART_DIR"

	// defaultReleaseName is what the chart's own docs install as, and what
	// resource names are derived from.
	defaultReleaseName = "haptic"
)

// resolveChartDir picks the chart directory for the in-process render:
// the --chart flag, then $HAPTIC_CHART_DIR, then the image-embedded path.
func resolveChartDir(flagValue string) (string, error) {
	candidates := []struct {
		dir    string
		origin string
	}{
		{flagValue, "--chart"},
		{os.Getenv(chartDirEnvVar), chartDirEnvVar},
		{embeddedChartPath, "image-embedded chart"},
	}
	for _, c := range candidates {
		if c.dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(c.dir, "Chart.yaml")); err != nil {
			return "", fmt.Errorf("no Helm chart at %s (from %s): %w\n"+
				"Hint: pass --chart <dir>, set %s, or pass -f <config.yaml> to skip the chart render",
				c.dir, c.origin, err, chartDirEnvVar)
		}
		return c.dir, nil
	}
	// Unreachable: the embedded path candidate is never empty.
	return "", fmt.Errorf("no chart directory configured")
}

// renderChart runs the chart's templates with the given overrides. Nil caps
// means Helm's defaults. Template-only, like `helm template`: no cluster
// access, so `lookup` returns empty.
func renderChart(c *chartv2.Chart, overrides map[string]any, rel common.ReleaseOptions,
	caps *common.Capabilities,
) (map[string]string, error) {
	// Prune/keep conditional subcharts per the override values.
	if err := chartv2util.ProcessDependencies(c, overrides); err != nil {
		return nil, fmt.Errorf("processing chart dependencies: %w", err)
	}

	renderValues, err := commonutil.ToRenderValues(c, overrides, rel, caps)
	if err != nil {
		return nil, fmt.Errorf("composing chart values: %w", err)
	}

	rendered, err := engine.Render(c, renderValues)
	if err != nil {
		// A chart `fail()` lands here: the values themselves are rejected,
		// which is a finding rather than an internal error.
		return nil, fmt.Errorf("the chart rejects these values: %w", err)
	}
	return rendered, nil
}
