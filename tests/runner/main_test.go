// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScenarioOptionsRetainEnvironmentAndFlags(t *testing.T) {
	t.Setenv("PLAIN_CLUSTER_NAME", "owned-plain")
	t.Setenv("KIND_NODE_IMAGE", "kindest/node:fixed")
	t.Setenv("KEEP_CLUSTER", "true")
	options, err := parseOptions([]string{"install-without-gateway-api"})
	require.NoError(t, err)
	require.Equal(t, "owned-plain", options.cluster)
	require.Equal(t, "kindest/node:fixed", options.nodeImage)
	require.True(t, options.keep)
	options, err = parseOptions([]string{"install-without-gateway-api", "--keep=false", "--cluster", "another", "--image", "registry:5000/haptic:test", "--artifacts", "/private/evidence"})
	require.NoError(t, err)
	require.Equal(t, "another", options.cluster)
	require.False(t, options.keep)
	require.Equal(t, "/private/evidence", options.artifacts)
}

func TestImagePartsPreserveRegistryPortAndRequireTag(t *testing.T) {
	repository, tag, err := imageParts("registry:5000/project/haptic:test")
	require.NoError(t, err)
	require.Equal(t, "registry:5000/project/haptic", repository)
	require.Equal(t, "test", tag)
	for _, invalid := range []string{"haptic", "registry:5000/haptic", "haptic:", "haptic@sha256:abcd", ":test"} {
		_, _, err = imageParts(invalid)
		require.Error(t, err, invalid)
	}
}

func TestDefaultsOptionsKeepChartDefaultsAndVersionedImage(t *testing.T) {
	t.Setenv("TIMEOUT", "240")
	t.Setenv("CLUSTER_NAME", "owned-defaults")
	options, err := parseOptions([]string{defaultsScenario, "--keep-cluster", "--namespace", "custom"})
	require.NoError(t, err)
	require.Empty(t, options.image)
	require.True(t, options.keep)
	require.Equal(t, "custom", options.namespace)
	require.Equal(t, "owned-defaults", options.cluster)
	options.image = "registry:5000/haptic:test-haproxy3.3"
	repository, tag, series, err := controllerImage(&options)
	require.NoError(t, err)
	require.Equal(t, "registry:5000/haptic", repository)
	require.Equal(t, "test", tag)
	require.Equal(t, "3.3", series)
}

func TestArtifactsRemainFreshAndUpgradeEvidenceIsCollectedByCI(t *testing.T) {
	root := t.TempDir()
	path, err := scenarioArtifacts(root, &options{name: upgradeScenario, baseline: "0.4.0"})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "debug-logs", "upgrade", "0.4.0"), filepath.Dir(path))
	requested := filepath.Join(root, "gitops")
	path, err = artifactDirectory(root, requested, gitopsScenario)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(path, "evidence"), []byte("first run"), 0o600))
	_, err = artifactDirectory(root, requested, gitopsScenario)
	require.ErrorIs(t, err, os.ErrExist)
}

func TestDefaultsRejectNonpositiveBudgetsAndRetainImageEnvironment(t *testing.T) {
	t.Setenv("IMAGE", "haptic:local-haproxy3.4")
	for _, seconds := range []string{"0", "-1"} {
		t.Setenv("TIMEOUT", seconds)
		_, err := parseOptions([]string{defaultsScenario})
		require.Error(t, err)
	}
	t.Setenv("TIMEOUT", "300")
	options, err := parseOptions([]string{defaultsScenario})
	require.NoError(t, err)
	require.Equal(t, "haptic:local-haproxy3.4", options.image)
}
