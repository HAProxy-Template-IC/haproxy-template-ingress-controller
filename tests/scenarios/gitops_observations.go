// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	api "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	agentapi "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/tests/publication"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

type gitopsSnapshot struct {
	Config  string            `json:"config"`
	Pods    []string          `json:"pods"`
	Secrets map[string]string `json:"secrets"`
}

type gitopsPublication struct {
	agents  map[string]agentapi.State
	config  *api.HAProxyCfg
	configs []api.HAProxyCfg
	secrets []corev1.Secret
}

func (s *gitopsSnapshot) equal(other *gitopsSnapshot) bool {
	return s.Config == other.Config && slices.Equal(s.Pods, other.Pods) && maps.Equal(s.Secrets, other.Secrets)
}

func (g *gitops) snapshot(ctx context.Context, phase string) (*gitopsSnapshot, error) {
	s := g.session
	config, err := s.ConfigSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	pods, err := readJSON[corev1.PodList](ctx, s, "get", "pods")
	if err != nil {
		return nil, err
	}
	secrets, err := readJSON[corev1.SecretList](ctx, s, "get", "secrets")
	if err != nil {
		return nil, err
	}
	snapshot, err := snapshotGitOps(config.Fingerprint, pods.Items, secrets.Items)
	if err != nil {
		return nil, err
	}
	if err := s.Save(phase+"-snapshot.json", snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func snapshotGitOps(fingerprint string, pods []corev1.Pod, secrets []corev1.Secret) (*gitopsSnapshot, error) {
	result := &gitopsSnapshot{Config: fingerprint, Secrets: map[string]string{}}
	counts := map[string]int{}
	for i := range pods {
		pod := &pods[i]
		component := pod.Labels["app.kubernetes.io/component"]
		if pod.DeletionTimestamp != nil || (component != "controller" && component != "loadbalancer") {
			continue
		}
		counts[component]++
		if pod.UID == "" {
			return nil, fmt.Errorf("pod %s has no UID", pod.Name)
		}
		result.Pods = append(result.Pods, string(pod.UID))
		if err := podHasNoRestarts(pod); err != nil {
			return nil, err
		}
	}
	if counts["controller"] != 2 || counts["loadbalancer"] != 2 {
		return nil, fmt.Errorf("expected two controller and two HAProxy pods, found %v", counts)
	}
	slices.Sort(result.Pods)
	for i := range secrets {
		secret := &secrets[i]
		if strings.HasPrefix(secret.Name, "sh.helm.release.") || strings.HasSuffix(secret.Name, "-pre-rollout-values") {
			continue
		}
		content, err := json.Marshal(secret.Data)
		if err != nil {
			return nil, err
		}
		result.Secrets[secret.Name] = fmt.Sprintf("%x", sha256.Sum256(content))
	}
	return result, nil
}

func (g *gitops) waitPublication(ctx context.Context, phase string, timeout, interval time.Duration) error {
	return poll(ctx, timeout, interval, phase+" publication and certificate cleanup", func(ctx context.Context) (testutil.PollResult, error) {
		observed, err := g.observePublication(ctx)
		if err != nil {
			var decode *observationDecodeError
			if errors.As(err, &decode) {
				return testutil.PollFailed, err
			}
			if saveErr := g.session.Save(phase+"-publication.json", map[string]any{"ready": false, "observation_failed": true}); saveErr != nil {
				return testutil.PollFailed, saveErr
			}
			return testutil.PollPending, err
		}
		config := observed.config
		ready := publication.Matches(config, observed.agents, observed.secrets, 2) && publication.RetainedMatches(config, observed.configs, observed.secrets)
		if err := g.session.Save(phase+"-publication.json", map[string]any{"ready": ready, "agents": agentIdentities(observed.agents), "config_resource_version": config.ResourceVersion, "checksum": config.Spec.Checksum, "auxiliary": config.Status.AuxiliaryFiles, "retained": config.Status.RetainedConfigs}); err != nil {
			return testutil.PollFailed, err
		}
		if ready {
			return testutil.PollSucceeded, nil
		}
		return testutil.PollPending, errors.New("fleet, publication, or certificate cleanup has not converged")
	})
}

func (g *gitops) observePublication(ctx context.Context) (*gitopsPublication, error) {
	s := g.session
	pods, err := s.Pods(ctx, "loadbalancer")
	if err != nil {
		return nil, err
	}
	agents := map[string]agentapi.State{}
	for i := range pods {
		pod := &pods[i]
		if pod.DeletionTimestamp != nil {
			continue
		}
		result, err := s.Kube(ctx, nil, "exec", pod.Name, "-c", "agent", "--", chartName, "agent", "state", "--output", "json", "--verify")
		if err != nil {
			return nil, err
		}
		var state agentapi.State
		if err := json.Unmarshal([]byte(result.Stdout), &state); err != nil {
			return nil, &observationDecodeError{err}
		}
		agents[string(pod.UID)] = state
	}
	config, err := readJSON[api.HAProxyCfg](ctx, s, "get", "haproxycfg", "haptic-config-haproxycfg")
	if err != nil {
		return nil, err
	}
	configs, err := readJSON[api.HAProxyCfgList](ctx, s, "get", "haproxycfgs")
	if err != nil {
		return nil, err
	}
	secrets, err := readJSON[corev1.SecretList](ctx, s, "get", "secrets")
	if err != nil {
		return nil, err
	}
	return &gitopsPublication{agents: agents, config: &config, configs: configs.Items, secrets: secrets.Items}, nil
}

func podHasNoRestarts(pod *corev1.Pod) error {
	for _, statuses := range [][]corev1.ContainerStatus{pod.Status.ContainerStatuses, pod.Status.InitContainerStatuses} {
		for i := range statuses {
			if statuses[i].RestartCount != 0 {
				return fmt.Errorf("%s/%s restarted", pod.Name, statuses[i].Name)
			}
		}
	}
	return nil
}

func agentIdentities(states map[string]agentapi.State) map[string]map[string]string {
	identities := make(map[string]map[string]string, len(states))
	for uid := range states {
		state := states[uid]
		identities[uid] = map[string]string{"applied_plan_id": state.AppliedPlanID, "running_plan_id": state.RunningPlanID, "worker_ops_plan_id": state.WorkerOpsPlanID, "reload_pending_at": state.ReloadPendingAt}
	}
	return identities
}
