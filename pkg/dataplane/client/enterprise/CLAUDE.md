# pkg/dataplane/client/enterprise - Enterprise-Only Operations

Development context for HAProxy Enterprise DataPlane API operations.

## Package Purpose

This package provides client operations for HAProxy Enterprise-only endpoints. These endpoints are not available in HAProxy Community edition; calling them against a Community instance returns `client.ErrEnterpriseRequired`.

## File Organization

Each file covers a feature domain. Per-file method counts drift quickly as new endpoints land — for the current count run:

```sh
for f in pkg/dataplane/client/enterprise/*.go; do
  case "$f" in *_test.go) continue ;; esac
  printf '%s: %d methods\n' "$(basename "$f")" "$(grep -cE '^func \(' "$f")"
done
```

| File | Description |
|------|-------------|
| `common.go` | `Operations` entry point, `IsAvailable`, `Capabilities`, `ErrNotFound` |
| `response.go` | `decodeResponse[T]`, `decodeResponseOr404[T]`, `decodeSliceResponse[T]`, `checkResponseStatus` — generic JSON decode/error helpers reused across files |
| `waf.go` | WAF profiles (CRUD, version, replace) |
| `waf_body_rules.go` | WAF body-inspection rules per profile |
| `waf_rulesets.go` | WAF rulesets and the rules inside them |
| `botmgmt.go` | Bot-management profiles and CAPTCHA configuration |
| `udp.go` | UDP load balancers and their child resources (binds, server templates, ACLs, switching rules) |
| `keepalived.go` | Keepalived transaction lifecycle and VRRP instances |
| `keepalived_vrrp_groups.go` | VRRP sync groups and track scripts (split out of `keepalived.go` to keep file size manageable) |
| `logging.go` | Advanced logging configuration |
| `git.go` | Git settings and actions |
| `dynamic_update.go` | Dynamic-update section + rules |
| `aloha.go` | ALOHA endpoints and actions |
| `misc.go` | Facts, ping, summary, structured config |

## Implementation Pattern

All operations route through `DispatchEnterpriseOnly` from the parent `client` package. `EnterpriseCallFunc[T]` exposes only `V30EE`, `V31EE`, `V32EE` — there is no `V33EE`; the Enterprise generated client does not yet ship a v3.3 variant even though Community does. Pin to `V32EE` when the endpoint is only available in 3.2+ and the dispatcher will return an error on older versions.

```go
func (w *WAFOperations) GetAllProfiles(ctx context.Context, txID string) ([]WafProfile, error) {
    resp, err := w.client.DispatchEnterpriseOnly(ctx, client.EnterpriseCallFunc[*http.Response]{
        V32EE: func(c *v32ee.Client) (*http.Response, error) {
            params := &v32ee.GetWafProfilesParams{TransactionId: &txID}
            return c.GetWafProfiles(ctx, params)
        },
        V31EE: func(c *v31ee.Client) (*http.Response, error) {
            params := &v31ee.GetWafProfilesParams{TransactionId: &txID}
            return c.GetWafProfiles(ctx, params)
        },
        V30EE: func(c *v30ee.Client) (*http.Response, error) {
            params := &v30ee.GetWafProfilesParams{TransactionId: &txID}
            return c.GetWafProfiles(ctx, params)
        },
    })
    if err != nil {
        return nil, fmt.Errorf("failed to get WAF profiles: %w", err)
    }
    defer resp.Body.Close()

    return decodeSliceResponse[WafProfile](resp, "GetAllWAFProfiles")
}
```

For decoding the body, prefer the generic helpers in `response.go` rather than hand-rolling `json.NewDecoder(resp.Body).Decode(...)` blocks — the helpers also normalise non-2xx statuses into the package's error model.

## Keepalived Transaction System

Keepalived has a separate transaction system from HAProxy configuration. The methods live on `KeepalivedOperations`:

```go
keepalived := enterprise.NewKeepalivedOperations(dpClient)

// Open Keepalived-specific transaction (note: StartTransaction, not CreateTransaction).
txID, err := keepalived.StartTransaction(ctx)
if err != nil {
    return err
}

// Make changes
if err := keepalived.CreateVRRPInstance(ctx, txID, instance); err != nil {
    _ = keepalived.DeleteTransaction(ctx, txID) // abort
    return err
}

// Commit Keepalived transaction
if err := keepalived.CommitTransaction(ctx, txID); err != nil {
    return err
}
```

The HAProxy `client.VersionAdapter` does **not** wrap Keepalived transactions — there is no equivalent retry loop here. Callers own the lifecycle. If you need to extend this, mirror the four entry points (`StartTransaction`, `CommitTransaction`, `DeleteTransaction`, `GetTransaction`) in `keepalived.go`.

## Error Handling

Every operation can surface:

- `client.ErrEnterpriseRequired` — the endpoint requires HAProxy Enterprise but the underlying `Clientset` reports Community edition. Returned by the dispatcher (`pkg/dataplane/client/dispatcher.go`) before any HTTP call is made.
- `enterprise.ErrNotFound` — the endpoint returned 404 (mainly used by the `decodeResponseOr404` helper).
- API-specific errors — schema violations, conflicts, etc.
- Network / context errors.

```go
profiles, err := wafOps.GetAllProfiles(ctx, txID)
switch {
case errors.Is(err, client.ErrEnterpriseRequired):
    log.Info("WAF features not available - using Community edition")
    return nil
case errors.Is(err, enterprise.ErrNotFound):
    return nil // empty result is fine
case err != nil:
    return fmt.Errorf("failed to get WAF profiles: %w", err)
}
```

## Testing

Enterprise features need an Enterprise HAProxy for integration; gate on `IsEnterprise` (or `Operations.IsAvailable`) so the test skips cleanly on Community fixtures:

```go
func TestWAFOperations_Integration(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    dpClient := setupTestClient(t) // *client.DataplaneClient
    if !dpClient.Clientset().IsEnterprise() {
        t.Skip("WAF tests require HAProxy Enterprise")
    }

    wafOps := enterprise.NewWAFOperations(dpClient)
    // … exercise the operations
}
```

For unit tests, mock at the dispatch level: build the same `EnterpriseCallFunc[*http.Response]` value but route through a fake `*client.DataplaneClient` whose `Clientset` returns a stub. The per-file `*_test.go` files in this directory show the pattern.
