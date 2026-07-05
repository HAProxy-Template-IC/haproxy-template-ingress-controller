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
	"strings"

	"helm.sh/helm/v4/pkg/chart/common"
	commonutil "helm.sh/helm/v4/pkg/chart/common/util"
	"helm.sh/helm/v4/pkg/chart/loader"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	chartv2util "helm.sh/helm/v4/pkg/chart/v2/util"
	"helm.sh/helm/v4/pkg/engine"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
)

const (
	// embeddedChartPath is where the Dockerfile copies the bundled Helm
	// chart into the controller image. The migrate-check CLI renders it
	// in-process when no --file / --chart override is given, so the
	// zero-argument docker one-liner works without any mounted config.
	embeddedChartPath = "/usr/share/haptic/chart"

	// chartDirEnvVar overrides the embedded chart location (between the
	// --chart flag and the built-in default).
	chartDirEnvVar = "HAPTIC_CHART_DIR"

	// configTemplateBasename is the chart template whose rendered output
	// is the HAProxyTemplateConfig resource.
	configTemplateBasename = "haproxytemplateconfig.yaml"
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

// renderChartConfigSpec renders the bundled Helm chart in-process (helm Go
// SDK, template-only — no cluster access, `lookup` returns empty) and
// returns the HAProxyTemplateConfig spec it produces. Every template
// library the chart's values declare under controller.templateLibraries is
// force-enabled so the render carries the migration coverage of every
// vendor library, regardless of the chart's defaults.
func renderChartConfigSpec(chartDir string) (*v1alpha1.HAProxyTemplateConfigSpec, error) {
	chrt, err := loader.Load(chartDir)
	if err != nil {
		return nil, fmt.Errorf("loading chart %s: %w", chartDir, err)
	}
	c, ok := chrt.(*chartv2.Chart)
	if !ok {
		return nil, fmt.Errorf("chart %s: unsupported chart apiVersion (got %T)", chartDir, chrt)
	}

	overrides := enableAllTemplateLibraries(c)

	// Prune/keep conditional subcharts per the override values (all
	// library conditions are true now, so every library subchart stays).
	if err := chartv2util.ProcessDependencies(c, overrides); err != nil {
		return nil, fmt.Errorf("processing chart dependencies: %w", err)
	}

	renderValues, err := commonutil.ToRenderValues(c, overrides, common.ReleaseOptions{
		Name:      "haptic",
		Namespace: "haptic",
		Revision:  1,
		IsInstall: true,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("composing chart values: %w", err)
	}

	rendered, err := engine.Render(c, renderValues)
	if err != nil {
		return nil, fmt.Errorf("rendering chart: %w", err)
	}

	for name, content := range rendered {
		if filepath.Base(name) != configTemplateBasename {
			continue
		}
		if strings.TrimSpace(content) == "" {
			return nil, fmt.Errorf("chart template %s rendered empty (controller.config unset?)", name)
		}
		spec, err := parseConfigSpec([]byte(content))
		if err != nil {
			return nil, fmt.Errorf("parsing rendered %s: %w", name, err)
		}
		return spec, nil
	}
	return nil, fmt.Errorf("chart %s renders no %s template", chartDir, configTemplateBasename)
}

// enableAllTemplateLibraries builds the sparse values override that sets
// controller.templateLibraries.<name>.enabled=true for every library key
// the chart's own default values declare. The key set is discovered from
// the chart, never hardcoded, so new libraries are covered automatically.
func enableAllTemplateLibraries(c *chartv2.Chart) map[string]any {
	libs := map[string]any{}
	if controller, ok := c.Values["controller"].(map[string]any); ok {
		if declared, ok := controller["templateLibraries"].(map[string]any); ok {
			for name := range declared {
				libs[name] = map[string]any{"enabled": true}
			}
		}
	}
	return map[string]any{"controller": map[string]any{"templateLibraries": libs}}
}
