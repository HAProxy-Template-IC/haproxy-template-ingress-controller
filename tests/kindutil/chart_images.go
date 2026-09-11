// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package kindutil

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"helm.sh/helm/v4/pkg/chart/common"
	commonutil "helm.sh/helm/v4/pkg/chart/common/util"
	"helm.sh/helm/v4/pkg/chart/loader"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	chartv2util "helm.sh/helm/v4/pkg/chart/v2/util"
	"helm.sh/helm/v4/pkg/engine"
	"sigs.k8s.io/yaml"
)

const hapticChartName = "haptic"

// ChartImages is the runtime selection made by this checkout's Helm chart.
type ChartImages struct {
	HAProxyVersion string `json:"haproxyVersion"`
	HAProxy        string `json:"haproxy"`
	Vector         string `json:"vector"`
	Varnish        string `json:"varnish"`
	Valkey         string `json:"valkey"`
	SPOAHub        string `json:"spoaHub"`
}

// ValidateChartImageTag rejects an independent tag that changes the chart image.
func ValidateChartImageTag(image, tag string) error {
	if tag == "" {
		return nil
	}
	if strings.Contains(image, "@") || !strings.HasSuffix(image, ":"+tag) {
		return fmt.Errorf("tag %q differs from chart image %q; unset the tag override", tag, image)
	}
	return nil
}

// LoadChartImages uses the chart's default series unless a matrix series is given.
func LoadChartImages(series string) (ChartImages, error) {
	root, err := RepoRoot()
	if err != nil {
		return ChartImages{}, err
	}
	loaded, err := loader.Load(filepath.Join(root, "charts", hapticChartName))
	if err != nil {
		return ChartImages{}, fmt.Errorf("loading runtime image chart: %w", err)
	}
	chart, ok := loaded.(*chartv2.Chart)
	if !ok {
		return ChartImages{}, fmt.Errorf("runtime image chart has unsupported type %T", loaded)
	}
	return renderChartImages(chart, series)
}

const chartImagesTemplate = `haproxyVersion: {{ .Values.haproxyVersion | quote }}
haproxy: {{ include "haptic.haproxy.image" . | quote }}
vector: {{ include "haptic.vector.image" . | quote }}
varnish: {{ .Values.cache.varnish.image | quote }}
valkey: {{ .Values.rateLimit.shared.managedStore.image | quote }}
spoaHub: {{ include "haptic.spoaHub.image" . | quote }}
`

func renderChartImages(chart *chartv2.Chart, series string) (ChartImages, error) {
	if series == "" {
		series, _ = chart.Values["haproxyVersion"].(string)
	}
	patches, _ := chart.Values["haproxyPatchVersions"].(map[string]any)
	patch, _ := patches[series].(string)
	if series == "" || patch == "" {
		return ChartImages{}, fmt.Errorf("HAProxy series %q has no patch in the Helm chart", series)
	}
	overrides := map[string]any{"haproxyVersion": series}
	if err := chartv2util.ProcessDependencies(chart, overrides); err != nil {
		return ChartImages{}, fmt.Errorf("processing runtime image chart dependencies: %w", err)
	}
	values, err := commonutil.ToRenderValues(chart, overrides, common.ReleaseOptions{
		Name: hapticChartName, Namespace: hapticChartName, Revision: 1, IsInstall: true,
	}, nil)
	if err != nil {
		return ChartImages{}, fmt.Errorf("composing runtime image chart values: %w", err)
	}
	const templateName = "templates/test-runtime-images.yaml"
	retainChartHelpers(chart)
	chart.Templates = append(chart.Templates, &common.File{Name: templateName, Data: []byte(chartImagesTemplate)})
	rendered, err := engine.Render(chart, values)
	if err != nil {
		return ChartImages{}, fmt.Errorf("rendering runtime image chart: %w", err)
	}
	var images ChartImages
	if err := yaml.UnmarshalStrict([]byte(rendered[chart.Name()+"/"+templateName]), &images); err != nil {
		return ChartImages{}, fmt.Errorf("decoding runtime images: %w", err)
	}
	for name, image := range map[string]string{
		"haproxy": images.HAProxy, "vector": images.Vector, "varnish": images.Varnish,
		"valkey": images.Valkey, "spoaHub": images.SPOAHub,
	} {
		if image == "" {
			return ChartImages{}, fmt.Errorf("helm chart selected no %s image", name)
		}
	}
	return images, nil
}

func retainChartHelpers(chart *chartv2.Chart) {
	chart.Templates = slices.DeleteFunc(chart.Templates, func(file *common.File) bool {
		return !strings.HasPrefix(path.Base(file.Name), "_")
	})
	for _, dependency := range chart.Dependencies() {
		retainChartHelpers(dependency)
	}
}
