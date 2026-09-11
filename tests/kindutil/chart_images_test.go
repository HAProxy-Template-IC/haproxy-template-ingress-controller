// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package kindutil

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v4/pkg/chart/loader"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
)

func loadImageTestChart(t *testing.T) *chartv2.Chart {
	t.Helper()
	root, err := RepoRoot()
	require.NoError(t, err)
	loaded, err := loader.Load(filepath.Join(root, "charts", hapticChartName))
	require.NoError(t, err)
	chart, ok := loaded.(*chartv2.Chart)
	require.True(t, ok)
	return chart
}

func TestChartImagesFollowTheDefaultAndEverySupportedSeries(t *testing.T) {
	chart := loadImageTestChart(t)
	images, err := LoadChartImages("")
	require.NoError(t, err)
	assert.Equal(t, chart.Values["haproxyVersion"], images.HAProxyVersion)
	patches := chart.Values["haproxyPatchVersions"].(map[string]any)
	for series, patch := range patches {
		t.Run(series, func(t *testing.T) {
			images, err := renderChartImages(loadImageTestChart(t), series)
			require.NoError(t, err)
			assert.Equal(t, series, images.HAProxyVersion)
			assert.Equal(t, "haproxytech/haproxy-debian:"+patch.(string), images.HAProxy)
		})
	}
}

func TestChartImagesFollowChangedChartValues(t *testing.T) {
	chart := loadImageTestChart(t)
	series := chart.Values["haproxyVersion"].(string)
	chart.Values["haproxyPatchVersions"].(map[string]any)[series] = "3.4.999"
	chart.Values["cache"].(map[string]any)["varnish"].(map[string]any)["image"] = "example.test/cache:new"
	chart.Values["rateLimit"].(map[string]any)["shared"].(map[string]any)["managedStore"].(map[string]any)["image"] = "example.test/store:new"
	images, err := renderChartImages(chart, "")
	require.NoError(t, err)
	assert.Equal(t, "haproxytech/haproxy-debian:3.4.999", images.HAProxy)
	assert.Equal(t, "example.test/cache:new", images.Varnish)
	assert.Equal(t, "example.test/store:new", images.Valkey)
}

func TestChartImagesUseTheChartImageHelpers(t *testing.T) {
	chart := loadImageTestChart(t)
	chart.Values["haproxy"].(map[string]any)["image"] = map[string]any{
		"repository": "example.test/haproxy", "tag": "custom@sha256:abc",
	}
	chart.Values["vector"].(map[string]any)["image"] = map[string]any{
		"repository": "example.test/vector", "tag": "custom",
	}
	chart.Metadata.AppVersion = "9.9.9"
	images, err := renderChartImages(chart, "")
	require.NoError(t, err)
	assert.Equal(t, "example.test/haproxy:custom@sha256:abc", images.HAProxy)
	assert.Equal(t, "example.test/vector:custom", images.Vector)
	assert.Equal(t, chart.Values["spoaHub"].(map[string]any)["image"].(map[string]any)["repository"].(string)+":9.9.9", images.SPOAHub)
}

func TestChartImagesRejectMissingPatch(t *testing.T) {
	for _, series := range []string{"99.0", ""} {
		t.Run(series, func(t *testing.T) {
			chart := loadImageTestChart(t)
			chart.Values["haproxyPatchVersions"] = map[string]any{}
			_, err := renderChartImages(chart, series)
			require.ErrorContains(t, err, "has no patch in the Helm chart")
		})
	}
}
