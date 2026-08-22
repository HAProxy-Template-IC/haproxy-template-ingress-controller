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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	agentclient "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
)

var (
	agentStateURL    string
	agentStateVerify bool
	agentStateOutput string
	agentStateFiles  bool
)

var agentStateCmd = &cobra.Command{
	Use:   "state",
	Short: "Print what this pod's HAPTIC agent holds and runs",
	Long: `Print the agent's state: the plans it applied, runs and can fall back to, what
its worker has loaded, what it still has to delete, and how the last apply went.

Run it in the pod, where the credentials the agent authenticates with are
already in the environment:

  kubectl exec -n <namespace> <pod> -c agent -- haptic agent state

Example usage:
  # What this pod holds and runs
  haptic agent state

  # Re-hash the tree first, so the digests are observations
  haptic agent state --verify

  # Every file with its digest and size
  haptic agent state --files

  # The raw /v1/state response
  haptic agent state --output json`,
	RunE: runAgentState,
}

func init() {
	agentStateCmd.Flags().StringVar(&agentStateURL, "url", "",
		"Agent base URL (default: http://127.0.0.1:<--listen port>)")
	agentStateCmd.Flags().BoolVar(&agentStateVerify, "verify", false,
		"Make the agent re-hash its tree, so the reported digests are observations rather than its last-known set")
	agentStateCmd.Flags().StringVarP(&agentStateOutput, "output", "o", outputHuman,
		"Output format: human, json")
	agentStateCmd.Flags().BoolVar(&agentStateFiles, "files", false,
		"List every file the agent holds with its digest and size")
	agentCmd.AddCommand(agentStateCmd)
}

func runAgentState(cmd *cobra.Command, _ []string) error {
	state, err := fetchAgentState(cmd.Context(), agentStateURL)
	if err != nil {
		return err
	}
	return printAgentState(os.Stdout, state)
}

// fetchAgentState reads /v1/state from the agent at url, or from the local one
// when url is empty. The credentials are the ones the agent itself was given.
func fetchAgentState(ctx context.Context, url string) (*api.State, error) {
	username, password := os.Getenv(agentUsernameEnv), os.Getenv(agentPasswordEnv)
	if username == "" || password == "" {
		return nil, errors.New("no agent credentials in the environment: " + agentUsernameEnv + " and " +
			agentPasswordEnv + " are set in the pod's agent container, which is where this command runs")
	}
	if url == "" {
		url = localAgentURL(agentListen)
	}
	agent, err := agentclient.New(&agentclient.Config{BaseURL: url, Username: username, Password: password})
	if err != nil {
		return nil, err
	}
	defer agent.Close()

	state, err := agent.State(ctx, agentStateVerify)
	if err != nil {
		return nil, fmt.Errorf("reading %s from %s: %w", api.PathState, url, err)
	}
	return state, nil
}

// localAgentURL turns the agent's listen address into a URL for this pod. A
// wildcard bind is reached on loopback, which is the only interface an exec
// into the container has.
func localAgentURL(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func printAgentState(w io.Writer, state *api.State) error {
	if agentStateOutput == outputJSON {
		encoded, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return fmt.Errorf("encoding the state: %w", err)
		}
		_, err = fmt.Fprintln(w, string(encoded))
		return err
	}
	if agentStateOutput != outputHuman {
		return fmt.Errorf("unknown output format %q; use human or json", agentStateOutput)
	}

	fmt.Fprintf(w, "agent %s, api v%d, plan schema %d\n",
		orDash(state.AgentVersion), state.APIVersion, state.PlanSchemaVersion)
	fmt.Fprintf(w, "haproxy %s, worker pid %d\n", orDash(state.HAProxy.Version), state.HAProxy.WorkerPID)
	if state.InvariantViolation != "" {
		fmt.Fprintf(w, "invariant violated: %s\n", state.InvariantViolation)
	}

	fmt.Fprintf(w, "\nplans (generation %d, token %d/%d)\n",
		state.Generation, state.AppliedToken.LeaderEpoch, state.AppliedToken.RenderSeq)
	fmt.Fprintf(w, "  applied     %s\n", orDash(state.AppliedPlanID))
	fmt.Fprintf(w, "  running     %s\n", orDash(state.RunningPlanID))
	fmt.Fprintf(w, "  worker ops  %s\n", orDash(state.WorkerOpsPlanID))
	fmt.Fprintf(w, "  last good   %s\n", orDash(state.LKGPlanID))

	fmt.Fprintf(w, "\nfiles %d, reload pending %s\n", len(state.Files), orDash(state.ReloadPendingAt))
	fmt.Fprintf(w, "pending deletes: %d servers, %d backends\n",
		len(state.PendingDeletes.Servers), len(state.PendingDeletes.Backends))
	printPendingDeletes(w, "server", state.PendingDeletes.Servers)
	printPendingDeletes(w, "backend", state.PendingDeletes.Backends)

	fmt.Fprintf(w, "\ninventory (generation %d): %s\n",
		state.Inventory.Generation, strings.Join(inventoryCounts(&state.Inventory), ", "))

	printLastApply(w, state.LastApply)
	if agentStateFiles {
		printAgentFiles(w, state.Files)
	}
	return nil
}

func printPendingDeletes(w io.Writer, kind string, names []string) {
	for _, name := range names {
		fmt.Fprintf(w, "  %s %s\n", kind, name)
	}
}

func inventoryCounts(inventory *api.Inventory) []string {
	return []string{
		"maps " + strconv.Itoa(len(inventory.Maps)),
		"certs " + strconv.Itoa(len(inventory.Certs)),
		"ca files " + strconv.Itoa(len(inventory.CAFiles)),
		"crl files " + strconv.Itoa(len(inventory.CRLFiles)),
		"crt-lists " + strconv.Itoa(len(inventory.CRTLists)),
	}
}

func printLastApply(w io.Writer, apply *api.ApplyResult) {
	if apply == nil {
		fmt.Fprintln(w, "\nlast apply: none since this agent started")
		return
	}
	outcome := "NACK"
	if apply.OK {
		outcome = "ok"
	}
	fmt.Fprintf(w, "\nlast apply %s: plan %s, mode %s, %s\n",
		outcome, orDash(apply.PlanID), orDash(apply.Mode), orDash(apply.At))
	if apply.Error != nil {
		fmt.Fprintf(w, "  error at %s: %s\n", orDash(apply.Error.Stage), apply.Error.Message)
	}
	if reload := apply.Reload; reload != nil {
		fmt.Fprintf(w, "  reload %s\n", describeReload(reload))
	}
	if rollback := apply.Rollback; rollback != nil && rollback.Performed {
		fmt.Fprintf(w, "  rolled back, reloaded %t\n", rollback.Reloaded)
	}
}

func describeReload(reload *api.ReloadInfo) string {
	if !reload.Performed {
		if reload.ScheduledAt != "" {
			return "scheduled for " + reload.ScheduledAt
		}
		return "not performed"
	}
	outcome := "failed"
	if reload.OK {
		outcome = "ok"
	}
	return fmt.Sprintf("%s in %dms, worker pid %d", outcome, reload.TookMs, reload.WorkerPID)
}

func printAgentFiles(w io.Writer, files map[string]api.FileAt) {
	fmt.Fprintf(w, "\nfiles (%d)\n", len(files))
	for _, path := range slices.Sorted(maps.Keys(files)) {
		at := files[path]
		fmt.Fprintf(w, "  %s  %s  %d bytes\n", at.Digest, path, at.Size)
	}
}

func orDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
