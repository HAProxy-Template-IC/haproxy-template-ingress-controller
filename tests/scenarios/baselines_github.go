// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"
)

const (
	currentKubernetes = "current"
	minimumKubernetes = "minimum"
)

type githubUpgradeJob struct {
	Name  string            `json:"name"`
	Env   map[string]string `json:"env"`
	Needs json.RawMessage   `json:"needs"`
}

func CheckGitHubUpgradeMatrix(published []string, content []byte) error {
	var workflow struct {
		Jobs map[string]githubUpgradeJob `json:"jobs"`
	}
	if err := yaml.Unmarshal(content, &workflow); err != nil {
		return err
	}
	var required []string
	if err := json.Unmarshal(workflow.Jobs["verified"].Needs, &required); err != nil {
		return fmt.Errorf("GitHub upgrade coverage requires the final verification gate: %w", err)
	}
	matrices := map[string][]string{currentKubernetes: nil, minimumKubernetes: nil}
	nodes := make(map[string]string, len(matrices))
	for id, job := range workflow.Jobs {
		if job.Env["PILOT_SUITE"] != helmUpgrade && !strings.HasPrefix(job.Name, "upgrade-") {
			continue
		}
		variant, err := githubUpgradeVariant(job)
		if err != nil {
			return fmt.Errorf("GitHub job %s: %w", id, err)
		}
		if !slices.Contains(required, id) {
			return fmt.Errorf("GitHub upgrade job %s is absent from the final verification gate", id)
		}
		node := job.Env["KIND_NODE_IMAGE"]
		if node == "" || (nodes[variant] != "" && nodes[variant] != node) {
			return fmt.Errorf("GitHub %s upgrade jobs must use the same explicit Kubernetes image", variant)
		}
		nodes[variant] = node
		matrices[variant] = append(matrices[variant], job.Env["BASELINE_CHART_VERSION"])
	}
	for _, variant := range []string{currentKubernetes, minimumKubernetes} {
		if err := checkUpgradeSelection(published, matrices[variant]); err != nil {
			return fmt.Errorf("GitHub %s Kubernetes: %w", variant, err)
		}
	}
	if nodes[currentKubernetes] == nodes[minimumKubernetes] {
		return errors.New("GitHub current and minimum Kubernetes upgrade jobs use the same image")
	}
	return nil
}

func githubUpgradeVariant(job githubUpgradeJob) (string, error) {
	var selection struct {
		Name     string `json:"name"`
		Suite    string `json:"suite"`
		Baseline string `json:"baseline"`
		Node     string `json:"node"`
	}
	if err := json.Unmarshal([]byte(job.Env["CI_SUITE"]), &selection); err != nil {
		return "", fmt.Errorf("invalid upgrade selection: %w", err)
	}
	baseline := job.Env["BASELINE_CHART_VERSION"]
	if selection.Suite != helmUpgrade || job.Env["PILOT_SUITE"] != helmUpgrade ||
		selection.Name != job.Name || selection.Baseline != baseline || baseline == "" {
		return "", errors.New("upgrade selection, job name, and baseline environment disagree")
	}
	variant := strings.TrimPrefix(job.Name, "upgrade-"+baseline+"-")
	if variant != currentKubernetes && variant != minimumKubernetes {
		return "", errors.New("upgrade job must select current or minimum Kubernetes")
	}
	if variant == minimumKubernetes && selection.Node != job.Env["KIND_NODE_IMAGE"] {
		return "", errors.New("minimum Kubernetes selection differs from the runner environment")
	}
	return variant, nil
}
