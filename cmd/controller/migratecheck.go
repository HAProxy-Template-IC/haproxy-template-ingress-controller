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
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/conversion"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/migratecheck"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// migrateCheckOptions carries the migrate-check flag values. Kept as a
// struct (rather than package globals like validate) so the whole run is
// testable end to end without flag mutation.
type migrateCheckOptions struct {
	configFile string
	chartDir   string
	schemaDir  string
	resources  string
	namespace  string
	kubeconfig string
	output     string
}

var migrateCheckOpts migrateCheckOptions

// migrateCheckCmd audits existing Ingresses against the migration coverage
// data the template libraries declare (spec.migrationCoverage) and renders
// each Ingress through the real template pipeline to find hard failures.
var migrateCheckCmd = &cobra.Command{
	Use:   "migrate-check",
	Short: "Check existing Ingresses for migration compatibility",
	Long: `Check the Ingresses of another ingress controller against HAPTIC.

The command answers "what happens to my Ingresses if I migrate them to
HAPTIC?" in three steps:

  1. It reads the migration coverage data (spec.migrationCoverage) that
     each bundled template library declares for its source controller.
  2. It groups your Ingresses by detected source controller and classifies
     every source-controller annotation: supported, different, dropped,
     failing, or unknown.
  3. It renders every Ingress through HAPTIC's real template pipeline —
     an Ingress the templates reject is reported as a blocker.

With no arguments it uses the Helm chart embedded in the controller image
(all vendor libraries enabled), the live cluster's schemas, and the live
cluster's Ingresses across all namespaces:

  # Inside the controller image (kubeconfig mounted):
  docker run --rm -v ~/.kube/config:/kube/config -e KUBECONFIG=/kube/config \
    <controller-image> migrate-check

Flags switch individual inputs to offline sources:

  # Audit manifests from a directory against a config file, no cluster:
  haptic-controller migrate-check -f config.yaml --resources ./manifests --schema-dir ./schemas

Exit codes: 0 every annotation fully supported; 1 differences or unknown
annotations to review; 2 blockers (failing annotations, render failures,
or the check itself failed).`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runMigrateCheck,
}

func init() {
	migrateCheckCmd.Flags().StringVarP(&migrateCheckOpts.configFile, "file", "f", "",
		"Path to a HAProxyTemplateConfig YAML file (default: render the bundled Helm chart in-process)")
	migrateCheckCmd.Flags().StringVar(&migrateCheckOpts.chartDir, "chart", "",
		"Helm chart directory to render the config from (default: $"+chartDirEnvVar+", then "+embeddedChartPath+")")
	migrateCheckCmd.Flags().StringVar(&migrateCheckOpts.schemaDir, "schema-dir", os.Getenv("HAPTIC_SCHEMA_DIR"),
		"Directory of schema files (CRD YAMLs or OpenAPI v3) for typed-resource access "+
			"(default: fetch schemas from the live cluster). Also reads HAPTIC_SCHEMA_DIR.")
	migrateCheckCmd.Flags().StringVar(&migrateCheckOpts.resources, "resources", "",
		"Directory of Ingress manifests to audit (default: list Ingresses from the live cluster)")
	migrateCheckCmd.Flags().StringVarP(&migrateCheckOpts.namespace, "namespace", "n", "",
		"Only audit Ingresses in this namespace (default: all namespaces)")
	migrateCheckCmd.Flags().StringVar(&migrateCheckOpts.kubeconfig, "kubeconfig", "",
		"Path to kubeconfig (default: $KUBECONFIG, ~/.kube/config, then in-cluster)")
	migrateCheckCmd.Flags().StringVarP(&migrateCheckOpts.output, "output", "o", migratecheck.FormatText,
		"Output format: text, json, markdown")
}

// exitCodeError carries a specific process exit code out of a cobra RunE.
// main() unwraps it and exits with the code; a nil wrapped error means the
// report already told the user everything (nothing extra is printed).
type exitCodeError struct {
	code int
	err  error
}

func (e *exitCodeError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *exitCodeError) Unwrap() error { return e.err }

// runMigrateCheck executes the audit and maps the outcome to the exit-code
// contract. Operational failures exit with the blocker code: a check that
// could not complete must never read as "safe to migrate".
func runMigrateCheck(cmd *cobra.Command, _ []string) error {
	// Engine/bootstrap progress logs stay off the report; warnings and
	// errors still surface on stderr.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))

	report, err := executeMigrateCheck(cmd.Context(), &migrateCheckOpts, logger)
	if err != nil {
		return &exitCodeError{code: migratecheck.ExitBlockers, err: fmt.Errorf("migrate-check could not complete: %w", err)}
	}

	out, err := migratecheck.Format(report, migrateCheckOpts.output)
	if err != nil {
		return &exitCodeError{code: migratecheck.ExitBlockers, err: err}
	}
	fmt.Print(out)

	if code := report.ExitCode(); code != migratecheck.ExitClean {
		return &exitCodeError{code: code}
	}
	return nil
}

// executeMigrateCheck acquires the config (file or in-process chart
// render), the schemas (dir or live cluster), and the Ingresses (dir or
// live cluster), renders every Ingress through the real template pipeline,
// and classifies the annotations against spec.migrationCoverage.
func executeMigrateCheck(ctx context.Context, opts *migrateCheckOptions, logger *slog.Logger) (*migratecheck.Report, error) {
	spec, err := acquireConfigSpec(opts)
	if err != nil {
		return nil, err
	}

	// A live-cluster connection is needed whenever any input defaults to
	// the cluster.
	var live *liveCluster
	if opts.schemaDir == "" || opts.resources == "" {
		live, err = connectLiveCluster(opts.kubeconfig)
		if err != nil {
			return nil, err
		}
	}

	// --schema-dir switches schema access offline; without it, schemas
	// come from the live cluster.
	schemas := schemaSource{live: live}
	if opts.schemaDir != "" {
		schemas, err = newDirSchemaSource(opts.schemaDir, logger)
		if err != nil {
			return nil, err
		}
	}

	if _, err := schemas.resolveEffectiveSpec(ctx, spec, logger); err != nil {
		return nil, err
	}

	if len(spec.MigrationCoverage) == 0 {
		return nil, errors.New("the configuration declares no spec.migrationCoverage data — " +
			"there is nothing to classify against.\n" +
			"Hint: coverage is declared by the chart's vendor template libraries; " +
			"use a chart/config version that ships it")
	}

	ingressKey, err := findIngressResourceKey(spec.WatchedResources)
	if err != nil {
		return nil, err
	}

	ingresses, err := collectIngresses(ctx, opts, live)
	if err != nil {
		return nil, err
	}

	checked, aggregateErr, err := renderIngresses(ctx, spec, schemas, ingressKey, ingresses, logger)
	if err != nil {
		return nil, err
	}

	report := migratecheck.Classify(spec.MigrationCoverage, checked)
	report.AggregateRenderError = aggregateErr
	return report, nil
}

// acquireConfigSpec loads the HAProxyTemplateConfig spec from --file, or
// renders the bundled Helm chart in-process.
func acquireConfigSpec(opts *migrateCheckOptions) (*v1alpha1.HAProxyTemplateConfigSpec, error) {
	if opts.configFile != "" {
		spec, err := loadConfigFromFiles([]string{opts.configFile})
		if err != nil {
			return nil, fmt.Errorf("loading config %s: %w", opts.configFile, err)
		}
		return spec, nil
	}
	chartDir, err := resolveChartDir(opts.chartDir)
	if err != nil {
		return nil, err
	}
	return renderChartConfigSpec(chartDir)
}

// findIngressResourceKey locates the watched-resource entry for Kubernetes
// Ingresses; its key is the fixture-store name the per-Ingress render uses.
//
// Auditing the networking.k8s.io/v1 Ingress kind is this subcommand's fixed
// operational domain (it exists to migrate away from Ingress controllers),
// not a template input — hardcoding the GVR here is the documented
// operational-identity exception to RULE #1. Which annotations mean what
// stays fully data-driven via spec.migrationCoverage.
func findIngressResourceKey(watched map[string]v1alpha1.WatchedResource) (string, error) {
	for name := range watched {
		wr := watched[name]
		if wr.Resources == "ingresses" && wr.APIVersion == "networking.k8s.io/v1" {
			return name, nil
		}
	}
	return "", errors.New("the configuration watches no networking.k8s.io/v1 ingresses — " +
		"migrate-check audits Ingress resources and needs the ingress template library enabled")
}

// collectIngresses reads the audited Ingresses from --resources or the
// live cluster.
func collectIngresses(ctx context.Context, opts *migrateCheckOptions, live *liveCluster) ([]*unstructured.Unstructured, error) {
	if opts.resources != "" {
		return loadIngressesFromDir(opts.resources, opts.namespace)
	}
	return live.listIngresses(ctx, opts.namespace)
}

// renderIngresses compiles the template engine once (typed globals from
// --schema-dir or the live cluster) and renders each Ingress through the
// full pipeline in isolation, recording the simplified render error.
func renderIngresses(
	ctx context.Context,
	spec *v1alpha1.HAProxyTemplateConfigSpec,
	schemas schemaSource,
	ingressKey string,
	ingresses []*unstructured.Unstructured,
	logger *slog.Logger,
) ([]migratecheck.Ingress, string, error) {
	coreCfg, err := conversion.ConvertSpec(spec)
	if err != nil {
		return nil, "", fmt.Errorf("converting config: %w", err)
	}

	typedResult, err := schemas.typeBootstrap(ctx, spec, logger)
	if err != nil {
		return nil, "", fmt.Errorf("type bootstrap: %w", err)
	}

	engine, err := createTemplateEngine(spec, typedResult, logger)
	if err != nil {
		return nil, "", err
	}

	validationPaths, capabilities, haproxyVersion, cleanup, err := setupValidationPaths(spec)
	if err != nil {
		return nil, "", err
	}
	defer cleanup()

	runner := testrunner.New(coreCfg, engine, validationPaths, &testrunner.Options{
		Logger:             logger,
		Workers:            1,
		Capabilities:       capabilities,
		HAProxyVersion:     haproxyVersion,
		TypedResourceTypes: typedResult.Types,
	})

	checked := make([]migratecheck.Ingress, 0, len(ingresses))
	for _, ing := range ingresses {
		entry := migratecheck.FromUnstructured(ing)
		if _, err := runner.RenderFixtures(map[string][]any{ingressKey: {ing.Object}}); err != nil {
			entry.RenderError = dataplane.SimplifyRenderingError(err)
		}
		checked = append(checked, entry)
	}

	// One aggregate pass renders ALL audited Ingresses together, the way
	// reconciliation does (templates iterate resources.ingresses.List()).
	// Isolated per-Ingress renders above cannot see conflicts that only
	// arise from the combination — duplicate backend/frontend names,
	// colliding hosts/paths, cross-resource map-key collisions — which
	// would be rejected at admission. Only run it when no individual render
	// already failed and there is more than one Ingress (a single Ingress's
	// aggregate render is its isolated render, already recorded).
	aggregateErr := ""
	if len(ingresses) > 1 && !anyRenderFailed(checked) {
		all := make([]any, 0, len(ingresses))
		for _, ing := range ingresses {
			all = append(all, ing.Object)
		}
		if _, err := runner.RenderFixtures(map[string][]any{ingressKey: all}); err != nil {
			aggregateErr = dataplane.SimplifyRenderingError(err)
		}
	}
	return checked, aggregateErr, nil
}

// anyRenderFailed reports whether any Ingress recorded an isolated render
// error (in which case the aggregate render would just re-surface it).
func anyRenderFailed(checked []migratecheck.Ingress) bool {
	for i := range checked {
		if checked[i].RenderError != "" {
			return true
		}
	}
	return false
}
