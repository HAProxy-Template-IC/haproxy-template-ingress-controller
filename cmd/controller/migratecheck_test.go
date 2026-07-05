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
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/migratecheck"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/dataplanetest"
)

// TestMain installs the fake HAProxy executor so setupValidationPaths'
// local-version detection doesn't shell out to a real `haproxy` binary
// (absent on many CI runners and all Windows). Per-package install avoids
// restore-ordering hazards with t.Parallel.
func TestMain(m *testing.M) {
	restore := dataplanetest.InstallFakeHAProxy()
	code := m.Run()
	restore()
	os.Exit(code)
}

// TestMigrateCheck_OfflineFixtureRun is the validate-style offline
// integration test: a hand-made config carrying spec.migrationCoverage in
// the contract shape plus a directory of Ingress manifests, run entirely
// without a cluster (--schema-dir + --resources). It exercises the full
// path — config load, effective-config resolution, real per-Ingress
// render, and classification — and pins the exit-code contract and the
// per-status grouping.
func TestMigrateCheck_OfflineFixtureRun(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	opts := &migrateCheckOptions{
		configFile: "testdata/migratecheck/config.yaml",
		resources:  "testdata/migratecheck/manifests",
		schemaDir:  "../../tests/schemas",
		output:     migratecheck.FormatText,
	}

	report, err := executeMigrateCheck(context.Background(), opts, logger)
	require.NoError(t, err)

	assert.Equal(t, 4, report.TotalIngresses)

	// One source ("acme") with three attributed Ingresses; the fourth
	// (someone-else class, no acme annotations) is unattributed.
	require.Len(t, report.Sources, 1)
	assert.Equal(t, "acme", report.Sources[0].Source)
	assert.Len(t, report.Sources[0].Ingresses, 3)
	require.Len(t, report.Unattributed, 1)
	assert.Equal(t, "plain", report.Unattributed[0].Name)

	// One of each classified status across the fixtures.
	assert.Equal(t, 1, report.Counts[migratecheck.StatusSupported])
	assert.Equal(t, 1, report.Counts[migratecheck.StatusDifferent])
	assert.Equal(t, 1, report.Counts[migratecheck.StatusDropped])
	assert.Equal(t, 1, report.Counts[migratecheck.StatusFails])
	assert.Equal(t, 1, report.Counts[migratecheck.StatusUnknown], "unknown-prefix annotation bucketed honestly")

	// The REAL render — not a hardcoded Go check — rejects the Ingress
	// carrying acme.io/rewrite-target, so it's a render failure.
	assert.Equal(t, 1, report.RenderFailures)
	assert.Equal(t, migratecheck.ExitBlockers, report.ExitCode())

	// The report renders in every format.
	for _, format := range []string{migratecheck.FormatText, migratecheck.FormatJSON, migratecheck.FormatMarkdown} {
		out, ferr := migratecheck.Format(report, format)
		require.NoError(t, ferr, "format %s", format)
		assert.NotEmpty(t, out)
	}
}

// TestMigrateCheck_NamespaceFilter narrows the audit to one namespace.
func TestMigrateCheck_NamespaceFilter(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	opts := &migrateCheckOptions{
		configFile: "testdata/migratecheck/config.yaml",
		resources:  "testdata/migratecheck/manifests",
		schemaDir:  "../../tests/schemas",
		namespace:  "default",
		output:     migratecheck.FormatText,
	}

	report, err := executeMigrateCheck(context.Background(), opts, logger)
	require.NoError(t, err)

	// Only the "default/plain" Ingress remains; it's unattributed and
	// renders fine, so the run is clean.
	assert.Equal(t, 1, report.TotalIngresses)
	assert.Empty(t, report.Sources)
	require.Len(t, report.Unattributed, 1)
	assert.Equal(t, "plain", report.Unattributed[0].Name)
	assert.Equal(t, migratecheck.ExitClean, report.ExitCode())
}

// TestMigrateCheck_ListWrapperManifests audits a `kind: List` manifest, the
// form `kubectl get ingress -A -o yaml` produces.
func TestMigrateCheck_ListWrapperManifests(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	opts := &migrateCheckOptions{
		configFile: "testdata/migratecheck/config.yaml",
		resources:  "testdata/migratecheck/list",
		schemaDir:  "../../tests/schemas",
		output:     migratecheck.FormatText,
	}

	report, err := executeMigrateCheck(context.Background(), opts, logger)
	require.NoError(t, err)

	// Both Ingresses inside the List wrapper are audited and attributed.
	assert.Equal(t, 2, report.TotalIngresses)
	require.Len(t, report.Sources, 1)
	assert.Len(t, report.Sources[0].Ingresses, 2)
	assert.Empty(t, report.Unattributed)
}

// TestLoadIngressesFromDir_ParsesBareAndListForms verifies the offline
// loader reads both `---`-separated bare Ingresses and List wrappers.
func TestLoadIngressesFromDir_ParsesBareAndListForms(t *testing.T) {
	bare, err := loadIngressesFromDir("testdata/migratecheck/manifests", "")
	require.NoError(t, err)
	assert.Len(t, bare, 4, "four bare Ingress docs")

	list, err := loadIngressesFromDir("testdata/migratecheck/list", "")
	require.NoError(t, err)
	assert.Len(t, list, 2, "two Ingresses unwrapped from the List")
}

// TestMigrateCheck_MissingCoverageIsAnError verifies a config with no
// coverage data fails the run (a clean exit would falsely read as "safe").
func TestMigrateCheck_MissingCoverageIsAnError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	dir := t.TempDir()
	// A config that watches ingresses but declares no migrationCoverage.
	configNoCoverage := `apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: no-coverage
spec:
  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      indexBy: [metadata.namespace, metadata.name]
  haproxyConfig:
    template: |
      global
      defaults
        mode http
      frontend http
        bind :8080
`
	configPath := dir + "/config.yaml"
	require.NoError(t, os.WriteFile(configPath, []byte(configNoCoverage), 0o600))

	opts := &migrateCheckOptions{
		configFile: configPath,
		resources:  "testdata/migratecheck/manifests",
		schemaDir:  "../../tests/schemas",
		output:     migratecheck.FormatText,
	}

	_, err := executeMigrateCheck(context.Background(), opts, logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no spec.migrationCoverage")
}
