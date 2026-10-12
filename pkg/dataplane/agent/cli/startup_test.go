package cli_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/cli"
)

func TestClientConstructionDoesNotProbeHAProxy(t *testing.T) {
	client, model := newClient(t)
	require.Empty(t, model.Sent())

	_, err := client.Info()
	require.NoError(t, err)
	_, err = client.ShowProc()
	require.NoError(t, err)
	_, err = client.Reload()
	require.NoError(t, err)
	require.Contains(t, model.Sent(), "reload")
}

func TestClientConstructionRequiresSockets(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  cli.Config
	}{
		{name: "worker", cfg: cli.Config{MasterSocket: "master.sock"}},
		{name: "master", cfg: cli.Config{WorkerSocket: "worker.sock"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cli.New(t.Context(), tc.cfg)
			require.ErrorContains(t, err, tc.name+" socket is required")
		})
	}
}
