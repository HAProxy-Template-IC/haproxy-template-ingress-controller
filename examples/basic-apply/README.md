# Basic Apply Example

A runnable Go program that drives the HAPTIC agent end to end: read a pod's state, describe a render as a `renderplan.Plan`, ask `deployplan.Diff` what the pod has to do, and send the apply.

The program lives in [`main.go`](./main.go) and targets a stand-alone HAProxy plus a `haptic agent` — no Kubernetes cluster required. Use it as a template when scripting one-off configuration pushes, or as a smoke test when investigating agent connectivity.

It applies two renders. The first is the complete file set plus a reload, because the agent has no baseline. The second adds one map entry against an identical configuration, which reaches the running worker as a runtime command with no reload — the difference the plan exists to make.

## Before you start

You need an HAProxy in master-worker mode and an agent against the same file tree. To run both in one container:

```bash
docker run --rm -d --name haptic-agent-example -p 5555:5555 \
    -e DATAPLANE_USERNAME=admin -e DATAPLANE_PASSWORD=admin \
    --entrypoint sh haproxytech/haproxy-debian:3.4 -c '
      mkdir -p /etc/haproxy/maps
      printf "global\n    stats socket /etc/haproxy/haproxy-worker.sock mode 600 level admin\n\ndefaults\n    mode http\n    timeout connect 5s\n    timeout client 30s\n    timeout server 30s\n\nfrontend boot\n    bind *:8404\n" > /etc/haproxy/haproxy.cfg
      haproxy -W -db -S /etc/haproxy/haproxy-master.sock,level,admin -- /etc/haproxy/haproxy.cfg'
```

Then copy the `haptic` binary in and start the agent:

```bash
docker cp bin/haptic haptic-agent-example:/usr/local/bin/haptic
docker exec -d haptic-agent-example /usr/local/bin/haptic agent --base-dir /etc/haproxy --listen :5555
```

Build `bin/haptic` first with `make build` if you do not have it.

## Configure and run

The example reads three environment variables with defaults; override them for your environment:

```bash
export HAPTIC_AGENT_URL=http://localhost:5555
export HAPTIC_AGENT_USER=admin
export HAPTIC_AGENT_PASS=admin

go run ./examples/basic-apply
```

## Expected output

```text
Agent 0.2.0 drives HAProxy 3.4.3, applied plan ""

Applying plan 6c099a3924ff8baf: reload (0 runtime op(s))
  reason: no baseline
  mode: reload, applied plan: 6c099a3924ff8baf
  HAProxy reloaded in 148 ms, worker pid 32

Applying plan b2b39d1b3e93690c: runtime (1 runtime op(s))
  mode: runtime, applied plan: b2b39d1b3e93690c
  op map_add: ok=true
  no reload: the change reached the running worker

Example completed successfully!
```

To clean up:

```bash
docker rm -f haptic-agent-example
```

## What the program demonstrates

### 1. The pod's own account of itself

```go
state, err := agent.State(ctx, false)
```

`GET /v1/state` answers which plan the pod applied, which one its worker runs, the digest of every file it holds, and what that worker has loaded — the maps, certificates and CA files a runtime command can address. Pass `true` to make the agent re-hash its tree first, so the digests are observations rather than its last-known set.

### 2. The render declares its own structure

```go
plan := &renderplan.Plan{
    Sections: []renderplan.Section{{Kind: renderplan.SectionKindCore, Name: "haproxy.cfg", TextDigest: ...}},
    Maps:     map[string]renderplan.Map{"maps/host.map": {Entries: renderplan.ParseMapEntries(content)}},
    Files:    []renderplan.File{...},
}
plan.ComputeID()
```

Nothing parses HAProxy configuration: the generator that emitted the text declares what it emitted. A plan ID is the digest of the plan, so two identical renders produce the same ID and the second apply is a noop.

### 3. The controller decides, the agent executes

```go
decision := deployplan.Diff(next, &deployplan.Baseline{
    Applied:   applied,
    Inventory: state.Inventory,
    Caps:      deployplan.CapsFor(state.HAProxy.Version, state.AgentOps),
})
```

`Decision.Verdict` is `runtime`, `file_only` or `reload`, `Decision.Ops` are the typed commands the agent will run verbatim, and `Decision.Reasons` names every change that could not run at runtime. This is the same function the playground runs in a browser to answer "will this change reload?".

### 4. Applying, and the answers to it

```go
result, err := agent.Apply(ctx, manifest, parts, nil)
```

Content travels only for the files the agent does not already hold: the first attempt sends none, and a `*client.MissingError` names the ones to resend. A `*client.ConflictError` means the pod is not on the baseline the ops were composed against and nothing was written — re-read the state and diff again. A rejected apply comes back as a result with `OK` false carrying HAProxy's own message, not as an error.

## See also

- [`docs/site/docs/development/agent.md`](../../docs/site/docs/development/agent.md) — the wire contract, documented from the Go types
- [`pkg/dataplane/agent/api`](../../pkg/dataplane/agent/api/) — the types both ends compile
- [`pkg/dataplane/deployplan`](../../pkg/dataplane/deployplan/) — the decision rules, table tested
- [`tests/agent`](../../tests/agent/) — the same contract against a real HAProxy in docker

## License

Apache-2.0 — see root `LICENSE`.
