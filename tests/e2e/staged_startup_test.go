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

//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/e2ecluster"
)

const stagedControllerManifestEnv = "HAPTIC_E2E_STAGED_CONTROLLER_MANIFEST"

func runStagedPostRenderer() {
	target := os.Getenv(stagedControllerManifestEnv)
	if target == "" {
		return
	}
	if err := renderStagedController(target); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func renderStagedController(targetPath string) error {
	manifests, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	staged, target, err := e2ecluster.StageControllerStartup(manifests)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		return err
	}
	if err := os.WriteFile(targetPath, encoded, 0o600); err != nil {
		return err
	}
	_, err = os.Stdout.Write(staged)
	return err
}

func installControllerRelease(ctx context.Context, args []string) error {
	mode := os.Getenv("HAPTIC_E2E_CONTROLLER_STARTUP")
	if mode == "" || mode == "parallel" {
		return runControllerInstallCommand(ctx, nil, "helm", args...)
	}
	if mode != "staged" {
		return fmt.Errorf("controller startup must be parallel or staged, got %q", mode)
	}
	directory, err := os.MkdirTemp("", "haptic-staged-startup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	targetPath := filepath.Join(directory, "controller.json")
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultHelmInstallTimeout)
	defer cancel()
	args = append(args, "--post-renderer", executable)
	environment := append(os.Environ(), stagedControllerManifestEnv+"="+targetPath)
	if err := runControllerInstallCommand(ctx, environment, "helm", args...); err != nil {
		return err
	}
	encoded, err := os.ReadFile(targetPath)
	if err != nil {
		return fmt.Errorf("read requested controller replicas: %w", err)
	}
	var target e2ecluster.ControllerScale
	if err := json.Unmarshal(encoded, &target); err != nil {
		return err
	}
	if target.Namespace == "" {
		target.Namespace = ControllerNamespace
	}
	return restoreControllerReplicas(ctx, target)
}

func restoreControllerReplicas(ctx context.Context, target e2ecluster.ControllerScale) error {
	common := []string{kubeconfigFlag, kubeconfigPath, "--namespace", target.Namespace}
	for replicas := int64(2); replicas <= target.Replicas; replicas++ {
		fmt.Fprintf(os.Stderr, "e2e: starting controller replica %d/%d after readiness\n", replicas, target.Replicas)
		if err := runControllerInstallCommand(ctx, nil, "kubectl", append(common, "scale", "deployment/"+target.Name, "--replicas="+strconv.FormatInt(replicas, 10))...); err != nil {
			return err
		}
		if err := runControllerInstallCommand(ctx, nil, "kubectl", append(common, "rollout", "status", "deployment/"+target.Name, "--timeout="+DefaultHelmInstallTimeout.String())...); err != nil {
			return err
		}
	}
	return nil
}

func runControllerInstallCommand(ctx context.Context, environment []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = environment
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("controller installation %s: %w", name, err)
	}
	return nil
}
