# pkg/dataplane/client/enterprise - Enterprise-Only Operations

Development context for HAProxy Enterprise DataPlane API operations.

## Package Purpose

This package provides client operations for HAProxy Enterprise-only endpoints. These endpoints are not available in HAProxy Community edition and will return `ErrEnterpriseRequired` when called against a community instance.

## File Organization

Each file covers a specific enterprise feature domain:

| File | Endpoints | Description |
|------|-----------|-------------|
| `common.go` | - | Shared types, Operations struct |
| `waf.go` | 11 | WAF profiles, body rules, rulesets |
| `botmgmt.go` | 4 | Bot management profiles, CAPTCHAs |
| `udp.go` | 12 | UDP load balancers with child resources |
| `keepalived.go` | 13 | VRRP instances, sync groups, track scripts |
| `logging.go` | 5 | Log configuration, inputs, outputs |
| `git.go` | 4 | Git settings, actions |
| `dynamic_update.go` | 3 | Dynamic update rules and section |
| `aloha.go` | 3 | ALOHA features and actions |
| `misc.go` | 4 | Facts, ping, summary, structured config |

## Implementation Pattern

All operations use `DispatchEnterpriseOnly` from the parent client package:

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

    // Parse response...
    return profiles, nil
}
```

## Keepalived Transaction System

Keepalived has a separate transaction system from HAProxy configuration. Use dedicated transaction methods:

```go
keepalived := enterprise.NewKeepalivedOperations(client)

// Start Keepalived-specific transaction
txID, err := keepalived.CreateTransaction(ctx)
if err != nil {
    return err
}

// Make changes
err = keepalived.CreateVRRPInstance(ctx, txID, instance)
if err != nil {
    keepalived.DeleteTransaction(ctx, txID)
    return err
}

// Commit Keepalived transaction
err = keepalived.CommitTransaction(ctx, txID)
```

## Error Handling

All operations may return:

- `client.ErrEnterpriseRequired` - Connected to Community edition
- API-specific errors (validation, not found, etc.)
- Network/connection errors

```go
profiles, err := wafOps.GetAllProfiles(ctx, txID)
if errors.Is(err, client.ErrEnterpriseRequired) {
    log.Info("WAF features not available - using Community edition")
    return nil
}
if err != nil {
    return fmt.Errorf("failed to get WAF profiles: %w", err)
}
```

## Testing

Enterprise features require HAProxy Enterprise for integration tests:

```go
func TestWAFOperations_Integration(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    client := setupTestClient(t)
    if !client.Clientset().IsEnterprise() {
        t.Skip("WAF tests require HAProxy Enterprise")
    }

    wafOps := enterprise.NewWAFOperations(client)
    // Test operations...
}
```

Unit tests can mock the dispatch functions.
