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
	"github.com/spf13/cobra"

	agentclient "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
)

func init() {
	agentCmd.AddCommand(&cobra.Command{
		Use:   "health",
		Short: "Check agent process liveness through its local socket",
		Args:  cobra.NoArgs,
		RunE:  runAgentHealth,
	})
}

func runAgentHealth(cmd *cobra.Command, _ []string) error {
	cfg, err := agentLocalClientConfig()
	if err != nil {
		return err
	}
	agent, err := agentclient.New(cfg)
	if err != nil {
		return err
	}
	defer agent.Close()
	return agent.Health(cmd.Context())
}
