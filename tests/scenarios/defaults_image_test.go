// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/haproxy-haptic/HAPTIC/tests/kindutil"
	"github.com/haproxy-haptic/HAPTIC/tests/process"
)

func TestDefaultsLoadTheImageTagRequestedByTheChart(t *testing.T) {
	const chartImage = "registry:5000/haptic:test-haproxy3.4"
	const spoaImage = "haptic-spoa:test"
	for _, source := range []string{"registry:5000/haptic:test", chartImage} {
		t.Run(source, func(t *testing.T) {
			local := map[string]bool{source: true, spoaImage: true}
			loaded := map[string]bool{}
			var cluster *kindutil.Cluster
			helmCalls := 0
			runner := scenarioRunner{run: func(_ context.Context, command *process.Command) (process.Result, error) {
				args := command.Args
				switch {
				case command.Name == "docker" && args[0] == "image":
					require.True(t, local[args[len(args)-1]])
					return process.Result{Stdout: "local-image-id"}, nil
				case command.Name == "docker" && args[0] == "tag":
					require.True(t, local[args[1]])
					local[args[2]] = true
				case command.Name == "docker" && args[0] == "ps":
					return process.Result{Stdout: "owned-node"}, nil
				case command.Name == "docker" && args[0] == "inspect":
					data, err := json.Marshal([]map[string]any{{"Id": "owned-node", "Mounts": []map[string]any{
						{"Source": cluster.Owner, "Destination": "/etc/haptic-test-owner", "RW": false},
					}}})
					return process.Result{Stdout: string(data)}, err
				case command.Name == "kind":
					for _, image := range args[4:] {
						require.True(t, local[image], image)
						loaded[image] = true
					}
				case command.Name == "helm":
					helmCalls++
					require.True(t, loaded[chartImage], "chart image must be loaded before Helm hooks run")
					require.True(t, loaded[spoaImage], "a locally built SPOA image must reach the cluster before Helm installs it")
					require.Contains(t, args, "controller.image.tag=test")
					require.Contains(t, args, "haproxyVersion=3.4")
				default:
					return process.Result{}, fmt.Errorf("unexpected command: %s %v", command.Name, args)
				}
				return process.Result{}, nil
			}}
			dir := t.TempDir()
			var err error
			cluster, err = kindutil.NewCluster(runner, &kindutil.ClusterOptions{
				Name: "owned", Kubeconfig: filepath.Join(dir, "kubeconfig"), Artifacts: dir,
			})
			require.NoError(t, err)
			session := &Session{Runner: runner, Cluster: cluster, Artifacts: dir,
				ImageRepository: "registry:5000/haptic", ImageTag: "test", HAProxyVersion: "3.4", SPOAImage: spoaImage}
			require.NoError(t, session.installDefaultsChart(t.Context(), &DefaultsOptions{Image: source}))
			require.Equal(t, 1, helmCalls)
		})
	}
}
