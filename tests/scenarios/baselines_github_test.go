// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func githubUpgradeFixture(t *testing.T) map[string]githubUpgradeJob {
	t.Helper()
	jobs := map[string]githubUpgradeJob{}
	required := make([]string, 0, 4)
	for _, variant := range []string{"current", "minimum"} {
		for _, version := range []string{"0.1.0", "0.2.0-alpha.3"} {
			name := "upgrade-" + version + "-" + variant
			node := "kindest/node:" + variant
			selection, err := json.Marshal(map[string]string{
				"name": name, "suite": "upgrade", "baseline": version, "node": node,
			})
			require.NoError(t, err)
			jobs[name] = githubUpgradeJob{Name: name, Env: map[string]string{
				"PILOT_SUITE": "upgrade", "BASELINE_CHART_VERSION": version,
				"KIND_NODE_IMAGE": node, "CI_SUITE": string(selection),
			}}
			required = append(required, name)
		}
	}
	needs, err := json.Marshal(required)
	require.NoError(t, err)
	jobs["verified"] = githubUpgradeJob{Needs: needs}
	return jobs
}

func TestGitHubUpgradeMatrixRequiresBothKubernetesVersionsAndFinalGate(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(map[string]githubUpgradeJob)
		want   string
	}{
		{name: "complete"},
		{name: "missing minimum baseline", change: func(jobs map[string]githubUpgradeJob) {
			delete(jobs, "upgrade-0.2.0-alpha.3-minimum")
		}, want: "minimum Kubernetes: upgrade matrix differs"},
		{name: "missing current baseline", change: func(jobs map[string]githubUpgradeJob) {
			delete(jobs, "upgrade-0.1.0-current")
		}, want: "current Kubernetes: upgrade matrix differs"},
		{name: "missing final gate", change: func(jobs map[string]githubUpgradeJob) {
			delete(jobs, "verified")
		}, want: "requires the final verification gate"},
		{name: "not required by gate", change: func(jobs map[string]githubUpgradeJob) {
			jobs["verified"] = githubUpgradeJob{Needs: json.RawMessage(`[]`)}
		}, want: "absent from the final verification gate"},
		{name: "wrong suite", change: func(jobs map[string]githubUpgradeJob) {
			jobs["upgrade-0.1.0-current"].Env["PILOT_SUITE"] = "gitops"
		}, want: "selection, job name, and baseline environment disagree"},
		{name: "wrong baseline", change: func(jobs map[string]githubUpgradeJob) {
			jobs["upgrade-0.1.0-current"].Env["BASELINE_CHART_VERSION"] = "0.2.0"
		}, want: "selection, job name, and baseline environment disagree"},
		{name: "invalid selector", change: func(jobs map[string]githubUpgradeJob) {
			jobs["upgrade-0.1.0-current"].Env["CI_SUITE"] = "invalid JSON"
		}, want: "invalid upgrade selection"},
		{name: "mismatched minimum node", change: func(jobs map[string]githubUpgradeJob) {
			jobs["upgrade-0.1.0-minimum"].Env["KIND_NODE_IMAGE"] = "kindest/node:current"
		}, want: "minimum Kubernetes selection differs"},
		{name: "inconsistent node versions", change: func(jobs map[string]githubUpgradeJob) {
			jobs["upgrade-0.1.0-current"].Env["KIND_NODE_IMAGE"] = "kindest/node:another"
		}, want: "must use the same explicit Kubernetes image"},
		{name: "same node for both variants", change: func(jobs map[string]githubUpgradeJob) {
			for _, job := range jobs {
				if job.Env != nil {
					job.Env["KIND_NODE_IMAGE"] = "kindest/node:minimum"
				}
			}
		}, want: "use the same image"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			jobs := githubUpgradeFixture(t)
			if tt.change != nil {
				tt.change(jobs)
			}
			content, err := json.Marshal(map[string]any{"jobs": jobs})
			require.NoError(t, err)
			err = CheckGitHubUpgradeMatrix([]string{"0.1.0", "0.2.0-alpha.3"}, content)
			if tt.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.want)
			}
		})
	}
}

func TestUpgradeSelectionRejectsRepeatedOrUnknownCoverage(t *testing.T) {
	require.ErrorContains(t, checkUpgradeSelection([]string{"0.1.0"}, []string{"0.1.0", "0.1.0"}), "duplicates=[0.1.0]")
	require.ErrorContains(t, checkUpgradeSelection([]string{"0.1.0"}, []string{"0.1.0", "0.2.0"}), "unknown=[0.2.0]")
	require.Error(t, CheckGitHubUpgradeMatrix([]string{"0.1.0"}, []byte("not: [valid")))
}
