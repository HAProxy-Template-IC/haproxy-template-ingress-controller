// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package kindutil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/tests/process"
)

// ResumeCluster returns nil for a cluster without this harness's ownership mount.
func ResumeCluster(ctx context.Context, runner process.Runner, name string, environment map[string]string) (*Cluster, bool, error) {
	if runner == nil || !clusterNamePattern.MatchString(name) {
		return nil, false, errors.New("cluster runner and valid name are required")
	}
	probe := &Cluster{Name: name, Environment: normalizedDockerEnvironment(environment), runner: runner}
	ids, err := probe.nodes(ctx)
	if err != nil || len(ids) == 0 {
		return nil, false, err
	}
	inspection, err := probe.run(ctx, "docker", append([]string{dockerInspect}, ids...)...)
	if err != nil {
		return nil, false, err
	}
	var identities []nodeIdentity
	if err := json.Unmarshal([]byte(inspection.Stdout), &identities); err != nil {
		return nil, false, err
	}
	if len(identities) != len(ids) {
		return nil, false, errors.New("incomplete Kind node ownership response")
	}
	owner, err := clusterOwner(identities)
	if err != nil {
		return nil, false, err
	}
	if owner == "" {
		return nil, false, nil
	}
	if !filepath.IsAbs(owner) || !strings.HasPrefix(filepath.Base(owner), "cluster-owner-") {
		return nil, false, errors.New("kind ownership mount has no test record")
	}
	root, err := os.OpenRoot(owner)
	if err != nil {
		return nil, false, err
	}
	defer root.Close()
	content, err := root.ReadFile("cluster.json")
	if err != nil {
		return nil, false, err
	}
	var recovered Cluster
	if err := json.Unmarshal(content, &recovered); err != nil {
		return nil, false, err
	}
	if !matchesClusterRecord(&recovered, name, owner, environment) {
		return nil, false, fmt.Errorf("kind ownership record does not identify cluster %s on this Docker host", name)
	}
	recovered.runner = runner
	if _, err := recovered.ownedNodes(ctx); err != nil {
		return nil, false, err
	}
	return &recovered, true, nil
}

func clusterOwner(identities []nodeIdentity) (string, error) {
	owner := ""
	for i := range identities {
		for _, mount := range identities[i].Mounts {
			if mount.Destination == ownerMount {
				if owner != "" && owner != mount.Source {
					return "", errors.New("kind nodes belong to different test runs")
				}
				owner = mount.Source
			}
		}
	}
	return owner, nil
}

func ClusterExists(ctx context.Context, runner process.Runner, name string, environment map[string]string) (bool, error) {
	if runner == nil || !clusterNamePattern.MatchString(name) {
		return false, fmt.Errorf("invalid cluster name %q", name)
	}
	probe := &Cluster{Name: name, Environment: normalizedDockerEnvironment(environment), runner: runner}
	nodes, err := probe.nodes(ctx)
	return len(nodes) > 0, err
}

func ClusterKubeconfig(ctx context.Context, runner process.Runner, name string, environment map[string]string) (string, error) {
	if runner == nil || !clusterNamePattern.MatchString(name) {
		return "", errors.New("cluster runner and valid name are required")
	}
	probe := &Cluster{Name: name, Environment: normalizedDockerEnvironment(environment), runner: runner}
	result, err := probe.run(ctx, "kind", "get", "kubeconfig", "--name", name)
	if err != nil {
		return "", err
	}
	content, err := patchOwnedKubeconfig([]byte(result.Stdout), name, environment["DOCKER_HOST"])
	return string(content), err
}

func matchesClusterRecord(recovered *Cluster, name, owner string, environment map[string]string) bool {
	return recovered.Name == name && recovered.Owner == owner && filepath.IsAbs(recovered.Kubeconfig) && filepath.IsAbs(recovered.Artifacts) && recovered.Environment["DOCKER_HOST"] == environment["DOCKER_HOST"]
}
