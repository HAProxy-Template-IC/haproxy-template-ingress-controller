# Basic Sync Example

A runnable Go program that drives the `pkg/dataplane` `Client` end-to-end: connect, sync a small HAProxy config, inspect the structured result, and preview an additional change with `DryRun`.

The program lives in [`main.go`](./main.go) and targets a stand-alone HAProxy + Dataplane API — no Kubernetes cluster required. Use it as a template when scripting one-off configuration pushes, or as a smoke test when investigating Dataplane API connectivity.

## Prerequisites

1. An HAProxy binary with a running Dataplane API. Inside the HAProxy config:

   ```haproxy
   program api
       command dataplaneapi -f /etc/haproxy/dataplaneapi.yml
   ```

2. A `dataplaneapi.yml` that exposes the API on a port this program can reach:

   ```yaml
   dataplaneapi:
     host: 0.0.0.0
     port: 5555
     user:
       - insecure: true
         password: admin
         name: admin
   ```

## Configure and Run

The example reads three environment variables with sensible defaults; override them for your environment:

```bash
export HAPROXY_URL=http://localhost:5555/v3   # /v3 for Dataplane API 3.x; /v2 still accepted
export HAPROXY_USER=admin
export HAPROXY_PASS=admin

go run main.go
```

## Expected Output

```text
Creating dataplane client...
Connected to HAProxy at http://localhost:5555/v3

Syncing HAProxy configuration...

Sync completed successfully!
Duration: 1.234s
Operations applied: 5
HAProxy reloaded: reload-123

Applied operations:
  1. [create] web-servers: Created backend
  2. [create] web-servers/web1: Created server
  3. [create] web-servers/web2: Created server
  4. [create] http-in: Created frontend
  5. [update] global: Updated global settings

--- Dry Run Example ---
Would apply 1 operations:
  1. [create] web-servers/web3: Would create server

Example completed successfully!
```

`HAProxy reloaded` only appears when the Dataplane API returns HTTP 202 (structural change requiring a reload); purely runtime-API updates (weight/address/port/maintenance) print "No HAProxy reload required".

## What the Program Demonstrates

All four patterns below are exactly what `pkg/dataplane` exposes publicly — if you're writing code against the controller's sync library elsewhere, the snippets in `main.go` are good starting points.

### 1. Reusable client

```go
client, err := dataplane.NewClient(ctx, &endpoint)
if err != nil {
    return err
}
defer client.Close()

r1, _ := client.Sync(ctx, config1, nil, nil)
r2, _ := client.Sync(ctx, config2, nil, nil)
```

`NewClient` takes a pointer (`*Endpoint`) and performs one round-trip to `/v3/info` to detect the Dataplane API version. Reuse the returned `*Client` for every subsequent call.

### 2. Structured errors

```go
result, err := client.Sync(ctx, desired, nil, opts)
if err != nil {
    var syncErr *dataplane.SyncError
    if errors.As(err, &syncErr) {
        log.Printf("failed at %s: %s", syncErr.Stage, syncErr.Message)
        for _, hint := range syncErr.Hints {
            log.Printf("  hint: %s", hint)
        }
    }
    return err
}
```

`SyncError.Stage` enumerates the phase that failed (`connect`, `fetch`, `parse-current`, `parse-desired`, `compare`, `apply`, `commit`, `fallback`). `Hints` are actionable suggestions emitted by the library.

### 3. Inspecting the result

```go
fmt.Printf("applied %d operations in %v\n", len(result.AppliedOperations), result.Duration)

if result.ReloadTriggered {
    fmt.Printf("reload: %s\n", result.ReloadID)
}
if result.Retries > 0 {
    fmt.Printf("%d version-conflict retries\n", result.Retries)
}
if result.UsedRawPush() {
    fmt.Println("fell back to raw config push")
}

for _, op := range result.AppliedOperations {
    fmt.Printf("[%s] %s: %s\n", op.Type, op.Resource, op.Description)
}
```

### 4. Dry run

```go
diff, err := client.DryRun(ctx, candidateConfig)
if err != nil {
    return err
}
if diff.HasChanges {
    fmt.Printf("would apply %d operations\n", len(diff.PlannedOperations))
}
```

`DryRun` runs the full compare pipeline but skips the apply step; handy for CI preflight checks.

## See Also

- [`pkg/dataplane`](../../pkg/dataplane/) — complete API, including `AuxiliaryFiles`, `SyncOptions`, and version/capability detection
- [`tests/integration`](../../tests/integration/) — more realistic tests, including auxiliary files, transactions, Enterprise sections
- [HAProxy Dataplane API docs](https://www.haproxy.com/documentation/haproxy-data-plane-api/)

## License

Apache-2.0 — see root `LICENSE`.
