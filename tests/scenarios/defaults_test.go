// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/tests/kubeexec"
	"gitlab.com/haproxy-haptic/haptic/tests/process"
)

func TestDefaultsRequireCurrentLibrarySetWithTests(t *testing.T) {
	for _, defect := range []string{"", "empty suite", "missing library", "wrong revision", "no references", "multiple configs"} {
		t.Run(defect, func(t *testing.T) {
			configs := []api.HAProxyTemplateConfig{{Spec: api.HAProxyTemplateConfigSpec{LibraryRefs: []api.LibraryRef{{Name: "base", Revision: "current"}}}}}
			libraries := []api.HAProxyTemplateLibrary{{ObjectMeta: metav1.ObjectMeta{Name: "base"}}}
			libraries[0].Spec.Revision = "current"
			libraries[0].Spec.ValidationTests = map[string]api.ValidationTest{"test": {}}
			switch defect {
			case "empty suite":
				libraries[0].Spec.ValidationTests = nil
			case "missing library":
				libraries[0].Name = "foreign"
			case "wrong revision":
				libraries[0].Spec.Revision = "stale"
			case "no references":
				configs[0].Spec.LibraryRefs = nil
			case "multiple configs":
				configs = append(configs, configs[0])
			}
			err := checkDefaultLibraries(configs, libraries)
			if defect == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestDefaultsRejectStaleLoadGateVerdict(t *testing.T) {
	config := &api.HAProxyTemplateConfig{ObjectMeta: metav1.ObjectMeta{Generation: 2}}
	config.Status.Conditions = []metav1.Condition{{Type: "Validated", Status: metav1.ConditionTrue, ObservedGeneration: 1}}
	require.Error(t, currentValidation(config))
	config.Status.Conditions[0].ObservedGeneration = 2
	require.NoError(t, currentValidation(config))
	config.Status.Conditions[0].Status = metav1.ConditionFalse
	require.Error(t, currentValidation(config))
}

func TestDefaultDryRunReplaceRejectsWarningsAndDenialsWithoutRetry(t *testing.T) {
	for _, mode := range []string{"clean", "warning", "denied"} {
		t.Run(mode, func(t *testing.T) {
			replaces := 0
			runner := scenarioRunner{run: func(_ context.Context, command *process.Command) (process.Result, error) {
				if slices.Contains(command.Args, "get") {
					return process.Result{Stdout: `{"metadata":{"resourceVersion":"fresh"}}`}, nil
				}
				require.Contains(t, command.Args, "replace")
				require.Contains(t, command.Args, "--dry-run=server")
				input, err := io.ReadAll(command.Stdin)
				require.NoError(t, err)
				require.Contains(t, string(input), "fresh")
				replaces++
				switch mode {
				case "warning":
					return process.Result{Stderr: "Warning: unknown field"}, nil
				case "denied":
					return process.Result{Stderr: "invalid template"}, errors.New("exit 1")
				default:
					return process.Result{}, nil
				}
			}}
			dir := t.TempDir()
			session := &Session{Client: kubeexec.Client{Runner: runner, Kubeconfig: filepath.Join(dir, "kubeconfig"), Context: "kind-owned"}, Artifacts: dir}
			err := session.defaultsReplace(t.Context(), "config", time.Second)
			if mode == "clean" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, 1, replaces)
		})
	}
}

func TestDefaultWarningsAndWorkerRetirement(t *testing.T) {
	require.True(t, haproxyWarning.MatchString("  [WARNING] ignored option"))
	require.True(t, haproxyWarning.MatchString("Warnings were found."))
	require.False(t, haproxyWarning.MatchString("Configuration file is valid"))
	for _, output := range []string{"", "10 master 0\n# old workers\n", "10 master 1\n# old workers\n11 worker 0\n", "10 master unknown\n"} {
		require.False(t, bootstrapRetired(output), output)
	}
	require.True(t, bootstrapRetired("10 master 1\n# workers\n12 worker 0\n# old workers\n"))
	require.True(t, applyFailure.MatchString("level=ERROR msg=\"Failed to apply rendered resource\""))
	require.True(t, applyFailure.MatchString("level=ERROR msg=\"Failed to resolve GVR for rendered resource\""))
	require.False(t, applyFailure.MatchString("level=ERROR transient connection refused"))
}

func TestManifestApplyPreservesDeclaredNamespaces(t *testing.T) {
	runner := scenarioRunner{run: func(_ context.Context, command *process.Command) (process.Result, error) {
		require.Contains(t, command.Args, "--context")
		require.Contains(t, command.Args, "kind-owned")
		require.NotContains(t, command.Args, "--namespace")
		content, err := io.ReadAll(command.Stdin)
		require.NoError(t, err)
		require.Contains(t, string(content), "cert-manager")
		return process.Result{}, nil
	}}
	dir := t.TempDir()
	session := &Session{Client: kubeexec.Client{Runner: runner, Kubeconfig: filepath.Join(dir, "kubeconfig"), Context: "kind-owned", Namespace: "haptic"}, Artifacts: dir}
	require.NoError(t, session.Apply(t.Context(), resource("v1", "Secret", "cert-manager", "webhook")))
	require.Equal(t, "haptic", session.Client.Namespace)
}

func TestCommandEvidenceAcceptsAbsoluteExecutablePaths(t *testing.T) {
	dir := t.TempDir()
	s := &Session{Artifacts: dir}
	_, err := s.record("/checkout/scripts/source-hash.sh", nil, process.Result{Stdout: "identity"}, nil)
	require.NoError(t, err)
	content, err := os.ReadFile(filepath.Join(dir, "0001-source-hash.sh.log"))
	require.NoError(t, err)
	require.Contains(t, string(content), "/checkout/scripts/source-hash.sh")
}
