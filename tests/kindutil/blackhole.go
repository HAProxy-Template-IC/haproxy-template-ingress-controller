// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package kindutil

import (
	"context"
	"errors"

	"gitlab.com/haproxy-haptic/haptic/tests/process"
)

// SyntheticBackendRanges keeps fixture health checks from reaching the host network.
func SyntheticBackendRanges() []string {
	return []string{"10.0.0.0/12", "192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24"}
}

// BlackholeCluster isolates a caller-selected, already-created Kind cluster without taking ownership.
func BlackholeCluster(ctx context.Context, runner process.Runner, name string, environment map[string]string) error {
	if runner == nil || !clusterNamePattern.MatchString(name) {
		return errors.New("cluster runner and valid name are required")
	}
	cluster := &Cluster{Name: name, Environment: normalizedDockerEnvironment(environment), runner: runner}
	nodes, err := cluster.nodes(ctx)
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		return errors.New("selected Kind cluster has no nodes")
	}
	return cluster.blackholeNodes(ctx, nodes)
}

func (c *Cluster) blackholeNodes(ctx context.Context, nodes []string) error {
	for _, node := range nodes {
		for _, cidr := range SyntheticBackendRanges() {
			if _, err := c.run(ctx, "docker", "exec", node, "ip", "route", "replace", "blackhole", cidr); err != nil {
				return err
			}
		}
	}
	return nil
}
