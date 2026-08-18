// Copyright 2026 Philipp Hossner
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
	"strings"

	"github.com/spf13/cobra"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/conversion"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/planblob"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

const (
	// podSourceScheme marks a --from / --to value as a live pod rather than a
	// configuration file.
	podSourceScheme = "pod://"

	// defaultDiffHAProxyVersion judges a render when no pod says otherwise.
	// It is the newest supported version, so the answer names every change
	// that cannot run at runtime rather than every change an old pod reloads.
	defaultDiffHAProxyVersion = "3.4"

	// defaultOpsListed caps the printed op list; --all prints every one.
	defaultOpsListed = 20
)

var (
	diffFiles          []string
	diffFrom           []string
	diffTo             []string
	diffTestName       string
	diffSchemaDir      string
	diffOutputFormat   string
	diffAll            bool
	diffHAProxyVersion string
	diffAgentOps       []string
	diffKubeconfig     string
	diffNamespace      string
	diffCRDName        string
	diffSecretName     string
	diffPort           int
)

var diffCmd = &cobra.Command{
	Use:   "diff",
	Short: "Answer whether a configuration change reloads HAProxy",
	Long: `Compare two configurations and print what a pod has to do to reach the second.

The verdict is one of:

  runtime    every change reaches the running worker over the runtime API
  file_only  files change, nothing the worker has loaded does
  reload     HAProxy has to reload

Both sides are either a configuration file set, which is rendered, or a running
pod, whose applied plan is read from its agent. This is the same decision the
controller makes per pod, so the verdict is what a deployment would do.

Rendered sides render against no watched resources at all unless --test names a
validationTest, whose fixtures are then loaded. A configuration change judged
against no Ingress and no Gateway is an answer about the configuration; pass
--test to ask about one fixture set.

Example usage:
  # Against the first HAProxy pod in the cluster
  haptic diff -f config.yaml

  # Against one named pod
  haptic diff -f config.yaml --from pod://haptic/haptic-haproxy-0

  # Two files, no cluster
  haptic diff --from deployed.yaml --to candidate.yaml

  # With one validationTest's fixtures, and every op listed
  haptic diff --from deployed.yaml --to candidate.yaml --test test-host-routing --all

  # As JSON, for a pipeline gate
  haptic diff --from deployed.yaml --to candidate.yaml --output json`,
	RunE: runDiff,
}

func init() {
	diffCmd.Flags().StringArrayVarP(&diffFiles, "file", "f", nil,
		"Path to a HAProxyTemplateConfig YAML file, repeatable, merged in order, exactly as haptic validate -f "+
			"accepts it. This is the side being diffed TO.")
	diffCmd.Flags().StringArrayVar(&diffTo, "to", nil,
		"Target of the diff: repeated config file paths, or one pod://<namespace>/<pod>. Defaults to -f.")
	diffCmd.Flags().StringArrayVar(&diffFrom, "from", nil,
		"Baseline of the diff: repeated config file paths, or one pod://<namespace>/<pod>. "+
			"Defaults to the first HAProxy pod the cluster reports.")
	diffCmd.Flags().StringVar(&diffTestName, "test", "",
		"Render both file sides with this validationTest's fixtures instead of no resources at all")
	diffCmd.Flags().StringVar(&diffSchemaDir, "schema-dir", os.Getenv("HAPTIC_SCHEMA_DIR"),
		"Directory of schema files for typed-resource access during the render "+
			"(accepts CustomResourceDefinition YAMLs or bare OpenAPI v3 schemas). Also reads HAPTIC_SCHEMA_DIR.")
	diffCmd.Flags().StringVarP(&diffOutputFormat, "output", "o", outputHuman,
		"Output format: human, json")
	diffCmd.Flags().BoolVar(&diffAll, "all", false,
		"List every op instead of the first 20")
	diffCmd.Flags().StringVar(&diffHAProxyVersion, "haproxy-version", "",
		"HAProxy version to judge the change against (default: the pod's, else "+defaultDiffHAProxyVersion+")")
	diffCmd.Flags().StringSliceVar(&diffAgentOps, "agent-ops", nil,
		"Op kinds the agent executes, comma-separated (default: all of them)")
	diffCmd.Flags().StringVar(&diffKubeconfig, "kubeconfig", "",
		"Path to kubeconfig file (for out-of-cluster usage)")
	diffCmd.Flags().StringVarP(&diffNamespace, "namespace", "n", "",
		"Namespace holding the HAProxyTemplateConfig, the credentials Secret and the HAProxy pods "+
			"(default: the pod's namespace, else the kubeconfig context's)")
	diffCmd.Flags().StringVar(&diffCRDName, "crd-name", "",
		"Name of the HAProxyTemplateConfig naming the agent port, credentials Secret and pod selector (env: CRD_NAME)")
	diffCmd.Flags().StringVar(&diffSecretName, "secret-name", "",
		"Name of the credentials Secret, when the HAProxyTemplateConfig names none (env: SECRET_NAME)")
	diffCmd.Flags().IntVar(&diffPort, "port", 0,
		"Agent port on the HAProxy pods (default: the HAProxyTemplateConfig's spec.dataplane.port)")
}

// newDiffLogger keeps the render's progress out of the answer: the verdict is
// one word, and anything printed above it reads as a problem.
func newDiffLogger() *slog.Logger {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	slog.SetDefault(logger)
	return logger
}

// diffSide is one end of the comparison: the plan it declares, and the pod it
// came from when it came from one.
type diffSide struct {
	plan  *renderplan.Plan
	state *api.State
}

func runDiff(cmd *cobra.Command, _ []string) error {
	logger := newDiffLogger()

	target := diffTo
	if len(target) == 0 {
		target = diffFiles
	}
	if len(target) == 0 {
		return errors.New("nothing to diff to: pass -f <config.yaml> (repeatable) or --to <source>")
	}
	toSource, err := parseDiffSource("--to", target)
	if err != nil {
		return err
	}
	fromSource, err := parseDiffSource("--from", diffFrom)
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	env := &diffEnv{}
	defer env.close()

	// The target renders first: its directories are where both renders resolve
	// map and certificate paths, and those paths are inside haproxy.cfg.
	to, err := resolveDiffSide(ctx, &toSource, env, logger)
	if err != nil {
		return fmt.Errorf("resolving the target of the diff: %w", err)
	}
	from, err := resolveDiffSide(ctx, &fromSource, env, logger)
	if err != nil {
		return fmt.Errorf("resolving the baseline of the diff: %w", err)
	}

	decision := deployplan.Diff(to.plan, baselineOf(from))
	return printDiff(os.Stdout, &diffReport{
		decision:   &decision,
		renderRoot: env.root(),
		unchanged:  from.plan.ID == to.plan.ID,
	})
}

// diffEnv owns the render directories of one run: the first render creates
// them, the second reuses them, and close removes them once.
type diffEnv struct{ env *renderEnv }

func (e *diffEnv) close() {
	if e.env != nil {
		e.env.cleanup()
	}
}

// root is the directory the renders wrote into, empty when neither side was a
// render.
func (e *diffEnv) root() string {
	if e.env == nil {
		return ""
	}
	return e.env.paths.TempDir
}

// diffSource is what one side of the diff was asked for: a file set, a named
// pod, or — with neither — whichever pod the cluster reports first.
type diffSource struct {
	files []string
	pod   *podRef
}

type podRef struct{ namespace, name string }

// parseDiffSource reads one side's values: every value is a file path, or the
// single value is a pod reference.
func parseDiffSource(flag string, values []string) (diffSource, error) {
	if len(values) == 0 {
		return diffSource{}, nil
	}
	pods := 0
	for _, value := range values {
		if strings.HasPrefix(value, podSourceScheme) {
			pods++
		}
	}
	switch {
	case pods == 0:
		return diffSource{files: values}, nil
	case pods == len(values) && pods == 1:
		ref, err := parsePodRef(values[0])
		if err != nil {
			return diffSource{}, fmt.Errorf("%s: %w", flag, err)
		}
		return diffSource{pod: ref}, nil
	default:
		return diffSource{}, fmt.Errorf("%s names both files and a pod; one side is one or the other", flag)
	}
}

func parsePodRef(value string) (*podRef, error) {
	namespace, name, found := strings.Cut(strings.TrimPrefix(value, podSourceScheme), "/")
	if !found || namespace == "" || name == "" {
		return nil, fmt.Errorf("%q is not a pod reference; write pod://<namespace>/<pod>", value)
	}
	return &podRef{namespace: namespace, name: name}, nil
}

// resolveDiffSide turns one side into the plan it declares, rendering into the
// run's shared directories so both renders name their files alike.
func resolveDiffSide(ctx context.Context, source *diffSource, env *diffEnv, logger *slog.Logger) (*diffSide, error) {
	if len(source.files) > 0 {
		return renderDiffSide(ctx, source.files, env, logger)
	}
	// No files: a named pod, or — for a side that was left out — whichever pod
	// the cluster reports first.
	state, err := readPodState(ctx, source.pod)
	if err != nil {
		return nil, err
	}
	plan, err := appliedPlanOf(state)
	if err != nil {
		return nil, err
	}
	return &diffSide{plan: plan, state: state}, nil
}

func renderDiffSide(ctx context.Context, files []string, env *diffEnv, logger *slog.Logger) (*diffSide, error) {
	schemas, err := newDirSchemaSource(diffSchemaDir, logger)
	if err != nil {
		return nil, err
	}
	setup, err := setupValidation(ctx, files, schemas, env.env, logger)
	if err != nil {
		return nil, err
	}
	if env.env == nil {
		env.env = setup.Env
	}

	cfg, err := conversion.ConvertSpec(setup.ConfigSpec)
	if err != nil {
		return nil, fmt.Errorf("converting config: %w", err)
	}
	runner := testrunner.New(cfg, setup.Engine, setup.ValidationPaths, &testrunner.Options{
		Logger:             logger,
		Workers:            1,
		Capabilities:       setup.Capabilities,
		HAProxyVersion:     setup.HAProxyVersion,
		TypedResourceTypes: setup.TypedResourceTypes,
	})

	rendered, err := renderForDiff(ctx, runner)
	if err != nil {
		return nil, err
	}
	if rendered.Plan == nil {
		return nil, fmt.Errorf("the render of %s declared no plan", strings.Join(files, ", "))
	}
	return &diffSide{plan: rendered.Plan}, nil
}

func renderForDiff(ctx context.Context, runner *testrunner.Runner) (testrunner.RenderOutput, error) {
	if diffTestName == "" {
		rendered, err := runner.RenderWithoutFixtures(ctx)
		if err != nil {
			return rendered, fmt.Errorf("rendering with no fixtures: %w\n"+
				"Hint: pass --test <name> to render against a validationTest's fixtures", err)
		}
		return rendered, nil
	}
	rendered, err := runner.Render(ctx, diffTestName)
	if err != nil {
		return rendered, fmt.Errorf("rendering test %q: %w", diffTestName, err)
	}
	return rendered, nil
}

// appliedPlanOf decodes what the pod reports it applied. A blob that decodes
// into a different plan describes a different pod, so it is no baseline.
func appliedPlanOf(state *api.State) (*renderplan.Plan, error) {
	if len(state.AppliedPlan) == 0 {
		return nil, errors.New("the pod reports no plan blob, so there is nothing to diff against.\n" +
			"Hint: it has not been applied to since it started; deploy once, or pass --from <config.yaml>")
	}
	plan, err := planblob.Decode(state.AppliedPlan)
	if err != nil {
		return nil, fmt.Errorf("decoding the pod's plan blob: %w", err)
	}
	if plan.ID != state.AppliedPlanID {
		return nil, fmt.Errorf("the pod's plan blob carries plan %s but it reports %s applied", plan.ID, state.AppliedPlanID)
	}
	if plan.SchemaVersion != renderplan.SchemaVersion {
		return nil, fmt.Errorf("the pod applied plan schema %d, this build reads %d", plan.SchemaVersion, renderplan.SchemaVersion)
	}
	return plan, nil
}

// baselineOf describes the pod the target has to be reached from. A rendered
// baseline is a pod that reloaded that render: everything it declares is on
// disk and loaded, nothing is pending.
func baselineOf(from *diffSide) *deployplan.Baseline {
	baseline := deployplan.Baseline{Applied: from.plan, Caps: capsForDiff(from.state)}
	if from.state == nil {
		baseline.Running, baseline.WorkerOps = from.plan, from.plan
		baseline.Inventory = deployplan.InventoryOf(from.plan)
		return &baseline
	}
	if from.state.RunningPlanID == from.plan.ID {
		baseline.Running = from.plan
	}
	if from.state.WorkerOpsPlanID == from.plan.ID {
		baseline.WorkerOps = from.plan
	}
	baseline.Inventory = from.state.Inventory
	baseline.PendingServerDeletes = len(from.state.PendingDeletes.Servers)
	baseline.PendingBackendDeletes = len(from.state.PendingDeletes.Backends)
	baseline.ReloadPending = from.state.ReloadPendingAt != ""
	return &baseline
}

func capsForDiff(state *api.State) deployplan.Caps {
	version, agentOps := diffHAProxyVersion, diffAgentOps
	if state != nil {
		if version == "" {
			version = state.HAProxy.Version
		}
		if len(agentOps) == 0 {
			agentOps = state.AgentOps
		}
	}
	if version == "" {
		version = defaultDiffHAProxyVersion
	}
	return deployplan.CapsFor(version, agentOps)
}
