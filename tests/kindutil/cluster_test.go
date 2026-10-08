// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package kindutil

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	kindconfig "sigs.k8s.io/kind/pkg/apis/config/v1alpha4"
	"sigs.k8s.io/yaml"

	"gitlab.com/haproxy-haptic/haptic/tests/process"
)

type clusterRunner struct {
	run func(context.Context, *process.Command) (process.Result, error)
}

func (r clusterRunner) Run(ctx context.Context, cmd *process.Command) (process.Result, error) {
	return r.run(ctx, cmd)
}
func (clusterRunner) Start(context.Context, *process.Command) (process.Running, error) {
	panic("unexpected async execution")
}

func TestOwnedClusterConfigPreservesSettingsAndMarksEveryNode(t *testing.T) {
	for _, dockerHost := range []string{"", "tcp://docker:2376", "tcp://[::1]:2376"} {
		t.Run(dockerHost, func(t *testing.T) {
			input := strings.Replace(BaseKindConfig, "nodes:\n", "featureGates:\n  TestFeature: true\nnodes:\n- role: worker\n", 1)
			data, err := ownedKindConfig([]byte(input), "/private/owner", dockerHost)
			require.NoError(t, err)
			var config kindconfig.Cluster
			require.NoError(t, yaml.UnmarshalStrict(data, &config))
			require.True(t, config.FeatureGates["TestFeature"])
			require.Len(t, config.Nodes, 2)
			for _, node := range config.Nodes {
				require.Contains(t, node.ExtraMounts, kindconfig.Mount{HostPath: "/private/owner", ContainerPath: ownerMount, Readonly: true})
			}
			require.Contains(t, config.Nodes[1].KubeadmConfigPatches[0], "maxPods: 500")
			if dockerHost == "" {
				require.Equal(t, "127.0.0.1", config.Networking.APIServerAddress)
				require.Len(t, config.KubeadmConfigPatchesJSON6902, 2)
			} else {
				require.Equal(t, "0.0.0.0", config.Networking.APIServerAddress)
				require.Len(t, config.KubeadmConfigPatchesJSON6902, 4)
				for _, patch := range config.KubeadmConfigPatchesJSON6902[2:] {
					require.Contains(t, []string{"v1beta3", "v1beta4"}, patch.Version)
					require.Contains(t, patch.Patch, "/apiServer/certSANs/-")
				}
			}
		})
	}
}

func TestOwnedClusterConfigRejectsDroppedOrUnownedSettings(t *testing.T) {
	for _, input := range []string{BaseKindConfig + "unknownField: true\n", "kind: Other\napiVersion: kind.x-k8s.io/v1alpha4", "kind: Cluster\napiVersion: kind.x-k8s.io/v1alpha4\nnodes:\n- role: control-plane\n- role: control-plane\n"} {
		_, err := ownedKindConfig([]byte(input), "/private/owner", "")
		require.Error(t, err)
	}
}

func clusterKubeconfig(name string) string {
	return "apiVersion: v1\nkind: Config\nclusters:\n- name: kind-" + name + "\n  cluster:\n    server: https://127.0.0.1:6443\ncontexts:\n- name: kind-" + name + "\n  context:\n    cluster: kind-" + name + "\ncurrent-context: kind-" + name + "\n"
}

func TestPrivateKubeconfigChecksIdentityAndPatchesOnlyOwnedAPI(t *testing.T) {
	data, err := patchOwnedKubeconfig([]byte(clusterKubeconfig("private")), "private", "tcp://[::1]:2376")
	require.NoError(t, err)
	require.Contains(t, string(data), "https://[::1]:6443")
	_, err = patchOwnedKubeconfig([]byte(clusterKubeconfig("foreign")), "private", "")
	require.ErrorContains(t, err, "owned Kind cluster")
}

func TestClusterNeverDeletesAnExistingClusterOrOverwritesKubeconfig(t *testing.T) {
	for _, existing := range []string{"node", "kubeconfig"} {
		t.Run(existing, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "private.kubeconfig")
			if existing == "kubeconfig" {
				require.NoError(t, os.WriteFile(path, []byte("user config"), 0o600))
			}
			runner := clusterRunner{run: func(_ context.Context, cmd *process.Command) (process.Result, error) {
				require.Equal(t, "docker", cmd.Name)
				require.Equal(t, "ps", cmd.Args[0])
				if existing == "node" {
					return process.Result{Stdout: "foreign-id\n"}, nil
				}
				return process.Result{}, nil
			}}
			cluster, err := NewCluster(runner, &ClusterOptions{Name: "private", Kubeconfig: path, Artifacts: dir})
			require.NoError(t, err)
			require.Error(t, cluster.Create(t.Context(), "kindest/node:fixed"))
			if existing == "kubeconfig" {
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, "user config", string(data))
			}
		})
	}
}

func TestClusterRequiresOwnershipBeforeEveryNodeMutation(t *testing.T) {
	for _, operation := range []string{"close", "blackhole", "load"} {
		t.Run(operation, func(t *testing.T) {
			dir := t.TempDir()
			runner := clusterRunner{run: func(_ context.Context, cmd *process.Command) (process.Result, error) {
				require.Equal(t, "docker", cmd.Name)
				switch cmd.Args[0] {
				case "ps":
					return process.Result{Stdout: "foreign-id\n"}, nil
				case "inspect":
					return process.Result{Stdout: `[{"Id":"foreign-id","Mounts":[]}]`}, nil
				default:
					t.Fatalf("mutated foreign node: %v", cmd.Args)
					return process.Result{}, nil
				}
			}}
			cluster, err := NewCluster(runner, &ClusterOptions{Name: "private", Kubeconfig: filepath.Join(dir, "private.kubeconfig"), Artifacts: dir})
			require.NoError(t, err)
			switch operation {
			case "close":
				err = cluster.Close(t.Context())
			case "blackhole":
				err = cluster.Blackhole(t.Context())
			case "load":
				err = cluster.LoadImages(t.Context(), "haptic:test")
			}
			require.ErrorContains(t, err, "foreign node")
		})
	}
}

func TestClusterCleansOnlyItsIDsAfterPartialStartup(t *testing.T) {
	dir := t.TempDir()
	var cluster *Cluster
	created, removed := false, false
	failure := errors.New("API failed to start")
	runner := clusterRunner{run: func(_ context.Context, cmd *process.Command) (process.Result, error) {
		if cmd.Name == "kind" {
			require.Contains(t, cmd.Args, "--retain")
			require.Contains(t, cmd.Args, "kindest/node:fixed")
			index := slices.Index(cmd.Args, "--kubeconfig")
			require.GreaterOrEqual(t, index, 0)
			require.Equal(t, cluster.Kubeconfig, cmd.Args[index+1])
			created = true
			return process.Result{Stderr: "startup failed"}, failure
		}
		switch cmd.Args[0] {
		case "ps":
			if created {
				return process.Result{Stdout: "owned-id\n"}, nil
			}
			return process.Result{}, nil
		case "inspect":
			return process.Result{Stdout: nodeInspection(t, "owned-id", cluster.Owner)}, nil
		case "rm":
			require.Equal(t, []string{"rm", "--force", "--volumes", "owned-id"}, cmd.Args)
			removed = true
			return process.Result{}, nil
		default:
			t.Fatalf("unexpected command: %v", cmd)
			return process.Result{}, nil
		}
	}}
	var err error
	cluster, err = NewCluster(runner, &ClusterOptions{Name: "private", Kubeconfig: filepath.Join(dir, "private.kubeconfig"), Artifacts: dir})
	require.NoError(t, err)
	require.ErrorIs(t, cluster.Create(t.Context(), "kindest/node:fixed"), failure)
	require.NoError(t, cluster.Close(t.Context()))
	require.True(t, removed)
}

func nodeInspection(t *testing.T, id, owner string) string {
	t.Helper()
	data, err := json.Marshal([]map[string]any{{"Id": id, "Mounts": []map[string]any{{"Source": owner, "Destination": ownerMount, "RW": false}}}})
	require.NoError(t, err)
	return string(data)
}

func TestClusterIsolatesEveryNodeUsingOneRangeList(t *testing.T) {
	dir := t.TempDir()
	var cluster *Cluster
	var routes [][]string
	runner := clusterRunner{run: func(_ context.Context, cmd *process.Command) (process.Result, error) {
		switch cmd.Args[0] {
		case "ps":
			return process.Result{Stdout: "owned-id\n"}, nil
		case "inspect":
			return process.Result{Stdout: nodeInspection(t, "owned-id", cluster.Owner)}, nil
		case "exec":
			routes = append(routes, slices.Clone(cmd.Args))
			return process.Result{}, nil
		default:
			t.Fatalf("unexpected command: %v", cmd)
			return process.Result{}, nil
		}
	}}
	var err error
	cluster, err = NewCluster(runner, &ClusterOptions{Name: "private", Kubeconfig: filepath.Join(dir, "private.kubeconfig"), Artifacts: dir})
	require.NoError(t, err)
	require.NoError(t, cluster.Blackhole(t.Context()))
	require.Len(t, routes, 4)
	for i, cidr := range []string{"10.0.0.0/12", "192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24"} {
		require.Equal(t, []string{"exec", "owned-id", "ip", "route", "replace", "blackhole", cidr}, routes[i])
	}
}

func TestOwnedPortMappingsUseLoopbackLocallyAndRemoteDockerAddress(t *testing.T) {
	for _, remote := range []bool{false, true} {
		config := kindconfig.Cluster{Nodes: []kindconfig.Node{{ExtraPortMappings: []kindconfig.PortMapping{{ContainerPort: 80}, {ContainerPort: 443, ListenAddress: allInterfaces}, {ContainerPort: 8404, ListenAddress: "127.0.0.2"}}}}}
		configurePortMappings(&config, remote)
		for i, mapping := range config.Nodes[0].ExtraPortMappings {
			expected := "127.0.0.1"
			if i == 2 {
				expected = "127.0.0.2"
			}
			if remote {
				expected = allInterfaces
			}
			require.Equal(t, expected, mapping.ListenAddress)
		}
	}
}

func TestClusterCreationKeepsDefaultAndExplicitImagesAcrossDockerHosts(t *testing.T) {
	for _, dockerHost := range []string{"", "tcp://docker:2376"} {
		for _, image := range []string{"", "kindest/node:v1.33.0@sha256:fixture"} {
			t.Run(dockerHost+image, func(t *testing.T) {
				var cluster *Cluster
				runner := successfulClusterRunner(t, image, &cluster)
				dir := t.TempDir()
				var err error
				cluster, err = NewCluster(runner, &ClusterOptions{Name: "matrix", Kubeconfig: filepath.Join(dir, "kubeconfig"), Artifacts: dir, Environment: map[string]string{"DOCKER_HOST": dockerHost}})
				require.NoError(t, err)
				require.NoError(t, cluster.Create(t.Context(), image))
				content, err := os.ReadFile(cluster.Kubeconfig)
				require.NoError(t, err)
				if dockerHost != "" {
					require.Contains(t, string(content), "https://docker:6443")
				}
			})
		}
	}
}

func successfulClusterRunner(t *testing.T, image string, cluster **Cluster) clusterRunner {
	t.Helper()
	created := false
	return clusterRunner{run: func(_ context.Context, cmd *process.Command) (process.Result, error) {
		if cmd.Name == "kind" {
			index := slices.Index(cmd.Args, "--image")
			if image == "" {
				require.Equal(t, -1, index)
			} else {
				require.Positive(t, index)
				require.Equal(t, image, cmd.Args[index+1])
			}
			created = true
			return process.Result{}, os.WriteFile((*cluster).Kubeconfig, []byte(clusterKubeconfig("matrix")), 0o600)
		}
		if cmd.Name == "kubectl" {
			return process.Result{}, nil
		}
		switch cmd.Args[0] {
		case "ps":
			if created {
				return process.Result{Stdout: "owned-id"}, nil
			}
		case "inspect":
			return process.Result{Stdout: nodeInspection(t, "owned-id", (*cluster).Owner)}, nil
		case "exec":
			require.True(t, created)
		default:
			t.Fatalf("unexpected command: %v", cmd)
		}
		return process.Result{}, nil
	}}
}
