// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package kindutil

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/tests/process"
)

func TestResumeRequiresMatchingDurableAndContainerOwnership(t *testing.T) {
	for _, mode := range []string{"owned", "foreign", "writable mount", "different name", "different Docker host"} {
		t.Run(mode, func(t *testing.T) {
			var original *Cluster
			runner := resumedClusterRunner(t, mode, &original)
			dir := t.TempDir()
			var err error
			original, err = NewCluster(runner, &ClusterOptions{Name: "private", Kubeconfig: filepath.Join(dir, "kubeconfig"), Artifacts: dir})
			require.NoError(t, err)
			if mode == "different name" {
				original.Name = "other"
				content, err := json.Marshal(original)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(original.Owner, "cluster.json"), content, 0o600))
			}
			environment := map[string]string{}
			if mode == "different Docker host" {
				environment["DOCKER_HOST"] = "tcp://other:2376"
			}
			recovered, found, err := ResumeCluster(t.Context(), runner, "private", environment)
			switch mode {
			case "owned":
				require.NoError(t, err)
				require.True(t, found)
				require.Equal(t, original.Owner, recovered.Owner)
			case "foreign":
				require.NoError(t, err)
				require.False(t, found)
				require.Nil(t, recovered)
			default:
				require.Error(t, err)
				require.False(t, found)
				require.Nil(t, recovered)
			}
		})
	}
}

func resumedClusterRunner(t *testing.T, mode string, original **Cluster) clusterRunner {
	t.Helper()
	return clusterRunner{run: func(_ context.Context, command *process.Command) (process.Result, error) {
		require.Equal(t, "", command.Env["DOCKER_CONTEXT"])
		if command.Args[0] == "ps" {
			return process.Result{Stdout: "container-id"}, nil
		}
		require.Equal(t, "inspect", command.Args[0])
		mounts := []map[string]any{{"Source": (*original).Owner, "Destination": ownerMount, "RW": mode == "writable mount"}}
		if mode == "foreign" {
			mounts = nil
		}
		content, err := json.Marshal([]map[string]any{{"Id": "container-id", "Mounts": mounts}})
		require.NoError(t, err)
		return process.Result{Stdout: string(content)}, nil
	}}
}
