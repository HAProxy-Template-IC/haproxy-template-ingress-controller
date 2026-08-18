// Basic example demonstrating the HAPTIC agent contract end to end.
//
// This example shows how to:
//   - Describe a render as a renderplan.Plan
//   - Ask deployplan.Diff what a pod has to do to reach it
//   - Send the resulting apply to a `haptic agent` and read its verdict
//
// Prerequisites:
//   - HAProxy running in master-worker mode with a worker stats socket
//   - `haptic agent` running against the same file tree
//
// Configuration:
//
//	Set these environment variables or modify the code:
//	- HAPTIC_AGENT_URL: agent endpoint (default: http://localhost:5555)
//	- HAPTIC_AGENT_USER: basic auth username (default: admin)
//	- HAPTIC_AGENT_PASS: basic auth password (default: admin)
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// The pod's tree, as the chart lays it out. A manifest path is relative to the
// agent's base directory and is the same string HAProxy names the file by at
// runtime, so nothing translates paths anywhere.
const (
	configPath = "haproxy.cfg"
	hostMap    = "maps/host.map"
)

// haproxyConfig is what a render produces. `default-path origin` makes the
// map's manifest path resolve, and the worker stats socket is what carries
// every runtime command.
const haproxyConfig = `global
    log stdout format raw local0
    stats socket /etc/haproxy/haproxy-worker.sock mode 600 level admin
    default-path origin /etc/haproxy

defaults
    mode http
    timeout client 30s
    timeout server 30s
    timeout connect 5s

frontend http-in
    bind *:80
    use_backend %[req.hdr(host),lower,map(` + hostMap + `,web-servers)]

backend web-servers
    balance roundrobin
    server web1 192.168.1.10:80 check inter 2s
`

// The two renders this example applies: the second routes one more host to the
// same backend, which is a map entry and nothing else.
const (
	hostMapV1 = "shop.example.com web-servers\n"
	hostMapV2 = "shop.example.com web-servers\nblog.example.com web-servers\n"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("Error: %v", err)
	}
}

func run() error {
	agent, err := client.New(&client.Config{
		BaseURL:  getEnv("HAPTIC_AGENT_URL", "http://localhost:5555"),
		Username: getEnv("HAPTIC_AGENT_USER", "admin"),
		Password: getEnv("HAPTIC_AGENT_PASS", "admin"),
	})
	if err != nil {
		return fmt.Errorf("failed to create the agent client: %w", err)
	}
	defer agent.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// The pod's own account of itself: which plan it applied, which one its
	// worker runs, and what that worker has loaded.
	state, err := agent.State(ctx, false)
	if err != nil {
		return fmt.Errorf("failed to read the agent's state: %w", err)
	}
	fmt.Printf("Agent %s drives HAProxy %s, applied plan %q\n\n",
		state.AgentVersion, state.HAProxy.Version, state.AppliedPlanID)

	// First render. The pod has no baseline of ours, so this is the complete
	// file set plus a reload.
	first := buildPlan(hostMapV1)
	if err := apply(ctx, agent, first, nil, state); err != nil {
		return err
	}

	// Second render: one more map entry, the same configuration. That is the
	// whole point of the plan — the change reaches the running worker as
	// `add map`, and HAProxy is never reloaded.
	second := buildPlan(hostMapV2)
	state, err = agent.State(ctx, false)
	if err != nil {
		return fmt.Errorf("failed to re-read the agent's state: %w", err)
	}
	if err := apply(ctx, agent, second, first, state); err != nil {
		return err
	}

	fmt.Println("\nExample completed successfully!")
	return nil
}

// buildPlan is what a render declares about its own output: the sections of
// haproxy.cfg, the entries of every map, and the file set. Nothing here parses
// HAProxy configuration — the generator knows the structure it emitted.
func buildPlan(hostMapContent string) *renderplan.Plan {
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections: []renderplan.Section{{
			Kind:       renderplan.SectionKindCore,
			Name:       configPath,
			TextDigest: renderplan.DigestString(haproxyConfig),
			Length:     len(haproxyConfig),
		}},
		Maps: map[string]renderplan.Map{
			hostMap: {Path: hostMap, Entries: renderplan.ParseMapEntries(hostMapContent)},
		},
		Files: []renderplan.File{
			{
				Path:           configPath,
				Kind:           renderplan.FileKindConfig,
				ReloadOnChange: true,
				Digest:         renderplan.DigestString(haproxyConfig),
				Size:           int64(len(haproxyConfig)),
			},
			{
				Path:   hostMap,
				Kind:   renderplan.FileKindMap,
				Digest: renderplan.DigestString(hostMapContent),
				Size:   int64(len(hostMapContent)),
			},
		},
	}
	plan.ComputeID()
	return plan
}

// apply decides what this pod has to do to reach next, sends it, and reports
// the verdict. Content travels only for the files the agent answers it lacks.
func apply(ctx context.Context, agent *client.Client, next, applied *renderplan.Plan, state *api.State) error {
	decision := deployplan.Diff(next, &deployplan.Baseline{
		Applied:   applied,
		Running:   applied,
		Inventory: state.Inventory,
		Caps:      deployplan.CapsFor(state.HAProxy.Version, state.AgentOps),
	})
	fmt.Printf("Applying plan %s: %s (%d runtime op(s))\n", next.ID, decision.Verdict, len(decision.Ops))
	for _, reason := range decision.Reasons {
		fmt.Printf("  reason: %s\n", reason)
	}

	manifest := &api.Manifest{
		PlanID:             next.ID,
		PlanSchemaVersion:  next.SchemaVersion,
		Token:              api.Token{LeaderEpoch: 1, RenderSeq: state.AppliedToken.RenderSeq + 1},
		ExpectedPrevPlanID: state.AppliedPlanID,
		ExpectedPrevToken:  state.AppliedToken,
		Files:              decision.Files,
		Ops:                decision.Ops,
		Mode:               decision.Mode,
	}

	result, err := send(ctx, agent, manifest, contentOf(next))
	if err != nil {
		return handleApplyError(err)
	}
	displayResult(result)
	return nil
}

// send makes the apply the controller makes: the manifest first, then a second
// attempt carrying only the parts the agent answered that it does not hold.
func send(ctx context.Context, agent *client.Client, manifest *api.Manifest, content map[string]string) (*api.ApplyResult, error) {
	result, err := agent.Apply(ctx, manifest, nil, nil)
	var missing *client.MissingError
	if !errors.As(err, &missing) {
		return result, err
	}
	parts := make(map[string]io.Reader, len(missing.Missing))
	for _, path := range missing.Missing {
		parts[path] = strings.NewReader(content[path])
	}
	return agent.Apply(ctx, manifest, parts, nil)
}

// contentOf is the bytes behind each file of a plan. A real controller streams
// them from the render rather than holding them in a map.
func contentOf(plan *renderplan.Plan) map[string]string {
	content := map[string]string{configPath: haproxyConfig}
	for _, entry := range plan.Maps[hostMap].Entries {
		content[hostMap] += entry.Key + " " + entry.Value + "\n"
	}
	return content
}

// handleApplyError separates the agent's answers from a transport failure.
func handleApplyError(err error) error {
	var conflict *client.ConflictError
	if errors.As(err, &conflict) {
		log.Printf("The pod is not on the baseline this apply was composed against (%s).",
			conflict.Conflict.Reason)
		log.Printf("Re-read /v1/state and diff again; nothing was written.")
	}
	return fmt.Errorf("apply failed: %w", err)
}

// displayResult prints what the pod did with the apply.
func displayResult(result *api.ApplyResult) {
	if !result.OK {
		fmt.Printf("  rejected at stage %q: %s\n", result.Error.Stage, result.Error.Message)
		return
	}
	fmt.Printf("  mode: %s, applied plan: %s\n", result.Mode, result.AppliedPlanID)
	for _, op := range result.OpResults {
		fmt.Printf("  op %s: ok=%t %s\n", op.Kind, op.OK, op.Output)
	}
	if result.Reload != nil && result.Reload.Performed {
		fmt.Printf("  HAProxy reloaded in %d ms, worker pid %d\n", result.Reload.TookMs, result.Reload.WorkerPID)
	} else {
		fmt.Println("  no reload: the change reached the running worker")
	}
}

// getEnv retrieves an environment variable with a fallback default value.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
