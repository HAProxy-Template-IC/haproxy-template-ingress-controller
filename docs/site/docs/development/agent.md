# HAPTIC agent

The HAPTIC agent is the container that owns an HAProxy pod's file tree and its
runtime socket. The controller sends it one apply per sync; the agent writes the
files, runs the runtime commands the controller composed, reloads when it has
to, and reports what it did.

Both ends compile one package, `pkg/dataplane/agent/api`. It defines the two
calls (`GET /v1/state`, `POST /v1/apply`), the manifest, the op kinds, the
result shapes and every limit both sides assert. The controller's end is
`pkg/dataplane/agent/client`.

## Testing

Three layers cover the agent, and each answers a different question.

| Layer | Question it answers | Where |
| --- | --- | --- |
| Unit tests | Does the client hold the contract's limits and classify every answer? | `pkg/dataplane/agent/client` |
| Fake agent | Does the controller's deployer react correctly to fencing, conflicts and rejections? | `pkg/dataplane/agent/agenttest` |
| Docker suite | Does a real HAProxy do what the contract says it does? | `tests/agent` |

### The in-process fake agent

`pkg/dataplane/agent/agenttest` is an `httptest` server that speaks the contract
without a container. It models what the deployer reasons about — the file set at
digest granularity, the four plan ids, the fencing token, the runtime inventory —
and records every apply for assertions. It runs no HAProxy commands and writes no
files.

```go
agent := agenttest.New(t)
c, err := client.New(&client.Config{
    BaseURL:  agent.URL(),
    Username: agent.Username(),
    Password: agent.Password(),
})
require.NoError(t, err)

result, err := c.Apply(context.Background(), manifest, parts, nil)
require.NoError(t, err)
require.True(t, result.OK)
require.Equal(t, api.ResultReload, result.Mode)
require.Len(t, agent.Applies(), 1)
```

Drive the paths a deployer has to survive with `agent.SetReloadPending(true)`
(the apply comes back `scheduled`, with only the in-place ops executed),
`agent.RejectOp(api.OpServerAdd)` (the apply is rejected and the pod's baseline
is invalidated), and the `WithAgentOps` option (the skew check reports missing
op kinds). A manifest's file digests must be `renderplan.Digest` of the content;
the fake verifies them, as the real agent does.

### The Docker suite

`tests/agent` brings up the chart's topology in containers — HAProxy in
master-worker mode with both sockets, the agent in its own container against the
same mounts, `general/` on a mount of its own — and drives it through the
controller's client. It imports no agent package, so it tests the wire contract
rather than the implementation.

Run it against one HAProxy version:

```bash
make test-agent-docker HAPROXY_VERSION=3.4
```

The suite builds the `haptic` binary itself and lays it into the HAProxy image,
so no image build is needed first. To test a binary you already have, point it
at one:

```bash
HAPTIC_BINARY=$PWD/bin/haptic make test-agent-docker HAPROXY_VERSION=3.0
```

The suite skips with a message when docker is unreachable or when the binary has
no `agent` subcommand. CI runs it on HAProxy 3.0 and 3.4 — the two versions
whose runtime CLI differs — with no Kubernetes cluster involved.

Under `default-path origin`, HAProxy names a map, a certificate and a crt-list at
runtime by the literal base-relative string the configuration references. That
string is also the manifest's `File.Path` and an op's `Path`, so no component
translates paths. `TestRuntimeNamesAreTheManifestPaths` pins that.

To debug one test, keep its containers and read their logs:

```bash
go test -tags=agentdocker -run TestMapOpsRunAtRuntimeAndKeepEveryByte -v ./tests/agent/
```

Each test dumps the agent's and HAProxy's logs when it fails, then removes its
containers and volumes.
