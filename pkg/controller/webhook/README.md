# pkg/controller/webhook - Webhook Adapter Component

Event adapter component that bridges the pure webhook library to the controller architecture.

## Overview

The webhook adapter manages the complete lifecycle of Kubernetes admission webhooks:

- TLS certificate generation and rotation
- HTTPS webhook server lifecycle
- Dynamic ValidatingWebhookConfiguration management
- Integration with controller event bus
- Bridging webhook validation to controller validators

This is a coordination layer - the actual webhook functionality lives in `pkg/webhook` (pure library).

## Component Architecture

```
pkg/webhook/ (Pure Library)
    ├── CertificateManager
    ├── Server
    └── ConfigManager
         ↓
    wrapped by
         ↓
pkg/controller/webhook/ (Event Adapter)
    └── Component
         ├── Manages lifecycle
         ├── Publishes events
         └── Bridges to validators
```

## Usage

### Basic Setup

```go
import (
    "haproxy-template-ic/pkg/controller/webhook"
    "haproxy-template-ic/pkg/webhook"
)

// Create component
webhookComponent := webhook.New(
    kubeClient,
    eventBus,
    logger,
    webhook.Config{
        Namespace:   "default",
        ServiceName: "my-webhook-svc",
        Rules: []webhook.WebhookRule{
            {
                APIGroups:   []string{""},
                APIVersions: []string{"v1"},
                Resources:   []string{"configmaps"},
            },
        },
    },
)

// Start component (blocks until context cancelled)
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

go webhookComponent.Start(ctx)
```

### Configuration

```go
config := webhook.Config{
    // Required
    Namespace:   "default",
    ServiceName: "webhook-service",

    // Optional (defaults shown)
    WebhookConfigName: "haproxy-template-ic-webhook",
    Port:              9443,
    Path:              "/validate",
    CertRotationCheckInterval: 24 * time.Hour,

    // Validation rules
    Rules: []webhook.WebhookRule{
        {
            APIGroups:   []string{""},
            APIVersions: []string{"v1"},
            Resources:   []string{"configmaps"},
        },
    },
}
```

### Graceful Shutdown

```go
// Stop webhook component
if err := webhookComponent.Stop(ctx); err != nil {
    log.Error("Failed to stop webhook", "error", err)
}
```

## Events Published

The component publishes events to the EventBus for observability and coordination:

### Lifecycle Events

- **WebhookServerStartedEvent**: Server started successfully
- **WebhookServerStoppedEvent**: Server stopped

### Certificate Events

- **WebhookCertificatesGeneratedEvent**: Initial certificates generated
- **WebhookCertificatesRotatedEvent**: Certificates rotated

### Configuration Events

- **WebhookConfigurationCreatedEvent**: ValidatingWebhookConfiguration created
- **WebhookConfigurationUpdatedEvent**: ValidatingWebhookConfiguration updated

### Validation Events

- **WebhookValidationRequestEvent**: Admission request received
- **WebhookValidationAllowedEvent**: Resource admitted
- **WebhookValidationDeniedEvent**: Resource rejected
- **WebhookValidationErrorEvent**: Validation error

## Certificate Rotation

Certificates are automatically rotated when they approach expiration:

1. **Periodic Check**: Every 24 hours (configurable)
2. **Rotation Threshold**: 30 days before expiry (from certificate manager)
3. **Rotation Process**:
   - Generate new certificates
   - Update ValidatingWebhookConfiguration with new CA bundle
   - Restart server with new certificates
   - Publish rotation event

## Integration with Controller

The webhook component is typically started in controller initialization:

```go
// After config loaded and EventBus started
webhookComponent := webhook.New(kubeClient, eventBus, logger, webhookConfig)
go webhookComponent.Start(ctx)
```

## Kubernetes Resources Required

### Service

The webhook server needs a Kubernetes Service to receive requests from the API server:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: my-webhook-svc
  namespace: default
spec:
  selector:
    app: my-controller
  ports:
    - port: 443
      targetPort: 9443
      protocol: TCP
```

### RBAC

The controller needs permissions to manage ValidatingWebhookConfiguration:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: webhook-manager
rules:
  - apiGroups: ["admissionregistration.k8s.io"]
    resources: ["validatingwebhookconfigurations"]
    verbs: ["get", "create", "update", "patch", "delete"]
```

## Troubleshooting

### Webhook Not Receiving Requests

**Check:**

1. Service exists and selects correct pods
2. ValidatingWebhookConfiguration exists
3. CA bundle matches generated CA certificate
4. Network policies allow API server → webhook traffic

**Debug:**

```bash
# Verify service
kubectl get svc my-webhook-svc -o yaml

# Verify webhook configuration
kubectl get validatingwebhookconfigurations my-webhook -o yaml

# Check webhook server logs
kubectl logs deployment/my-controller | grep webhook
```

### Certificate Errors

**Symptoms**: `x509: certificate signed by unknown authority`

**Fix**: Ensure CA bundle in ValidatingWebhookConfiguration matches the CA certificate used to sign server certificate.

**Check:**

```bash
# View CA bundle
kubectl get validatingwebhookconfigurations my-webhook \
    -o jsonpath='{.webhooks[0].clientConfig.caBundle}' | base64 -d
```

### Port Conflicts

**Symptoms**: `address already in use`

**Fix**: Change webhook port in configuration or verify no other process uses port 9443.

## Advanced Topics

### Custom Validation

To wire webhook to existing validators (Phase 2 feature):

```go
// Create validator bridge
func (c *Component) createValidator(gvk string) webhook.ValidationFunc {
    return func(obj interface{}) (bool, string, error) {
        // Publish validation request event
        req := events.NewConfigValidationRequest(obj)

        // Use request-response pattern to gather validation results
        result, err := c.eventBus.Request(ctx, req, events.RequestOptions{
            Timeout: 5 * time.Second,
            ExpectedResponders: []string{"basic", "template"},
        })

        // Aggregate results
        if err != nil || !allValid(result) {
            return false, extractReason(result), nil
        }

        return true, "", nil
    }
}
```

### Multiple Webhook Rules

Define separate rules for different resource types:

```go
Rules: []webhook.WebhookRule{
    {
        APIGroups:   []string{""},
        APIVersions: []string{"v1"},
        Resources:   []string{"configmaps"},
        Operations:  []admissionv1.OperationType{admissionv1.Create, admissionv1.Update},
    },
    {
        APIGroups:   []string{""},
        APIVersions: []string{"v1"},
        Resources:   []string{"secrets"},
        Operations:  []admissionv1.OperationType{admissionv1.Create},
    },
}
```

### Failure Policy

Control what happens when webhook is unavailable:

```go
// Fail-closed (reject requests if webhook down)
fail := admissionv1.Fail
rule.FailurePolicy = &fail

// Fail-open (allow requests if webhook down)
ignore := admissionv1.Ignore
rule.FailurePolicy = &ignore
```

## See Also

- Pure webhook library: `pkg/webhook/README.md`
- Webhook event types: `pkg/controller/events/README.md`
- Controller architecture: `docs/development/design.md`
