// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package kindutil

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/tests/process"
)

func TestClusterNetworkRefusesExistingAndLabelsNewNetworks(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[exists], func(t *testing.T) {
			var cluster *Cluster
			created := false
			runner := clusterRunner{run: func(_ context.Context, cmd *process.Command) (process.Result, error) {
				switch cmd.Args[1] {
				case "ls":
					if exists {
						return process.Result{Stdout: "foreign-id"}, nil
					}
					return process.Result{}, nil
				case "inspect":
					return process.Result{Stdout: `{"com.docker.network.driver.mtu":"1450"}`}, nil
				case "create":
					created = true
					require.Contains(t, cmd.Args, networkOwnerLabel+"="+cluster.Owner)
					require.Contains(t, cmd.Args, "--ipv6")
					require.Contains(t, cmd.Args, "com.docker.network.driver.mtu=1450")
					require.Equal(t, "private-network", cmd.Args[len(cmd.Args)-1])
					index := slices.Index(cmd.Args, "--subnet")
					require.Regexp(t, `^fd[0-9a-f]{2}(:[0-9a-f]{4}){3}::/64$`, cmd.Args[index+1])
					return process.Result{Stdout: "owned-network"}, nil
				default:
					t.Fatalf("unexpected network command: %v", cmd.Args)
					return process.Result{}, nil
				}
			}}
			dir := t.TempDir()
			var err error
			cluster, err = NewCluster(runner, &ClusterOptions{Name: "private", Kubeconfig: filepath.Join(dir, "kubeconfig"), Artifacts: dir, Environment: map[string]string{dockerNetworkSetting: "private-network"}})
			require.NoError(t, err)
			err = cluster.createNetwork(t.Context())
			if exists {
				require.ErrorContains(t, err, "already exists")
				require.False(t, created)
			} else {
				require.NoError(t, err)
				require.True(t, created)
			}
		})
	}
}

func TestClusterNetworkCleanupRequiresOwnedEmptyNetwork(t *testing.T) {
	for _, mode := range []string{"owned", "foreign", "in use", "gone"} {
		t.Run(mode, func(t *testing.T) {
			var cluster *Cluster
			removed := false
			runner := networkCleanupRunner(t, mode, &cluster, &removed)
			dir := t.TempDir()
			var err error
			cluster, err = NewCluster(runner, &ClusterOptions{Name: "private", Kubeconfig: filepath.Join(dir, "kubeconfig"), Artifacts: dir, Environment: map[string]string{dockerNetworkSetting: "private-network"}})
			require.NoError(t, err)
			err = cluster.closeNetwork(t.Context())
			if mode == "owned" || mode == "gone" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, mode == "owned", removed)
		})
	}
}

func networkCleanupRunner(t *testing.T, mode string, cluster **Cluster, removed *bool) clusterRunner {
	t.Helper()
	return clusterRunner{run: func(_ context.Context, cmd *process.Command) (process.Result, error) {
		switch cmd.Args[1] {
		case "ls":
			require.Contains(t, cmd.Args, "label="+networkOwnerLabel+"="+(*cluster).Owner)
			if mode == "gone" {
				return process.Result{}, nil
			}
			return process.Result{Stdout: "network-id"}, nil
		case "inspect":
			owner := (*cluster).Owner
			if mode == "foreign" {
				owner = "someone else"
			}
			containers := map[string]any{}
			if mode == "in use" {
				containers["other-container"] = map[string]any{}
			}
			data, err := json.Marshal([]map[string]any{{"Id": "network-id", "Name": "private-network", "Labels": map[string]string{networkOwnerLabel: owner}, "Containers": containers}})
			require.NoError(t, err)
			return process.Result{Stdout: string(data)}, nil
		case "rm":
			require.Equal(t, []string{"network", "rm", "network-id"}, cmd.Args)
			*removed = true
			return process.Result{}, nil
		default:
			t.Fatalf("unexpected command: %v", cmd.Args)
			return process.Result{}, nil
		}
	}}
}
