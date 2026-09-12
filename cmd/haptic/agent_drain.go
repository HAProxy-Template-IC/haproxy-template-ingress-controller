// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/server"
)

var (
	agentDrainClientSocket  string
	agentDrainClientTimeout time.Duration
)

var agentDrainCmd = &cobra.Command{
	Use:   "drain",
	Short: "Wait on the agent's drain socket until the pod stopped receiving new connections",
	Long: `Wait on the agent's drain socket.

kubelet stops every container of a pod at the same time, so the agent runs this
as its own preStop hook while the HAProxy container's hook waits on the same
socket: neither container is stopped before kube-proxy stopped routing new
connections to the pod, and HAProxy's soft stop then drains what is left.`,
	RunE: runAgentDrain,
}

func init() {
	agentDrainCmd.Flags().StringVar(&agentDrainClientSocket, "socket", "/etc/haproxy/haptic-drain.sock",
		"Path of the agent's drain socket")
	agentDrainCmd.Flags().DurationVar(&agentDrainClientTimeout, "timeout", server.DefaultDrainMaxWait+5*time.Second,
		"Give up waiting after this long; keep it above the agent's --drain-max-wait")
	agentCmd.AddCommand(agentDrainCmd)
}

func runAgentDrain(cmd *cobra.Command, _ []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, agentDrainClientTimeout)
	defer cancelTimeout()
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", agentDrainClientSocket)
		},
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://drain"+api.PathDrain, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("drain over %s: %w", agentDrainClientSocket, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("drain over %s: %w", agentDrainClientSocket, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("drain over %s: %s: %s", agentDrainClientSocket, resp.Status, bytes.TrimSpace(body))
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(bytes.TrimSpace(body)))
	return err
}
