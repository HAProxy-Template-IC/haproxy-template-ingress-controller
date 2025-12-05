# pkg/webhook - Kubernetes Admission Webhook Library

Pure Go library for implementing Kubernetes admission webhooks with flexible validation handlers.

## Overview

This package provides a lightweight, production-ready solution for Kubernetes admission webhooks:

- **HTTPS Webhook Server**: Handles AdmissionReview requests from the Kubernetes API server
- **Flexible Validation Interface**: Simple function signature for implementing custom validation logic
- **Pure Library**: No dependencies on other project packages (only standard library + k8s.io/*)
- **Thread-Safe**: Concurrent request handling with proper synchronization

## Features

- Thread-safe concurrent request handling
- Graceful shutdown with context cancellation
- Flexible ValidationFunc interface with full admission context
- Support for CREATE, UPDATE, DELETE operations
- Proper AdmissionReview v1 request/response handling

## Quick Start

```go
package main

import (
 "context"
 "fmt"
 "log"

 "haproxy-template-ic/pkg/webhook"
 "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func main() {
 ctx := context.Background()

 // Load TLS certificates from external source (Kubernetes Secret, cert-manager, etc.)
 certPEM, keyPEM := loadCertificates()

 // Create webhook server
 server := webhook.NewServer(&webhook.ServerConfig{
  Port:    9443,
  CertPEM: certPEM,
  KeyPEM:  keyPEM,
  Path:    "/validate",
 })

 // Register validators by GVK (Group/Version.Kind)
 server.RegisterValidator("networking.k8s.io/v1.Ingress", validateIngress)
 server.RegisterValidator("v1.ConfigMap", validateConfigMap)

 // Start server
 log.Println("Starting webhook server on :9443")
 if err := server.Start(ctx); err != nil {
  log.Fatal(err)
 }
}

// Validation function with full admission context
func validateIngress(ctx *webhook.ValidationContext) (bool, string, error) {
 // Access the resource object (already parsed as unstructured.Unstructured)
 if ctx.Object == nil {
  return false, "", fmt.Errorf("object is nil")
 }

 // Extract spec using unstructured helpers
 spec, found, err := unstructured.NestedMap(ctx.Object.Object, "spec")
 if err != nil || !found {
  return false, "spec is required", nil
 }

 // Validate rules exist
 rules, found, err := unstructured.NestedSlice(spec, "rules")
 if err != nil || !found || len(rules) == 0 {
  return false, "at least one rule is required", nil
 }

 // For UPDATE operations, compare with old object
 if ctx.Operation == "UPDATE" && ctx.OldObject != nil {
  // Implement immutability checks or migration validation
 }

 return true, "", nil
}

func validateConfigMap(ctx *webhook.ValidationContext) (bool, string, error) {
 // Simple validation example
 return true, "", nil
}
```

## TLS Certificate Management

This library **does not** include certificate generation or management. Certificates must be provided from external sources:

### Option 1: Kubernetes Secret (Recommended)

Use cert-manager or manually created certificates stored in a Kubernetes Secret:

```go
import (
 corev1 "k8s.io/api/core/v1"
 metav1 "k8s.io/apimachinery/pkg/v1"
)

// Fetch certificates from Kubernetes Secret
secret, err := client.CoreV1().Secrets("default").Get(ctx, "webhook-certs", metav1.GetOptions{})
if err != nil {
 log.Fatal(err)
}

certPEM := secret.Data["tls.crt"]
keyPEM := secret.Data["tls.key"]

server := webhook.NewServer(&webhook.ServerConfig{
 Port:    9443,
 CertPEM: certPEM,
 KeyPEM:  keyPEM,
})
```

### Option 2: cert-manager Integration

Let cert-manager handle certificate lifecycle:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: webhook-cert
  namespace: default
spec:
  secretName: webhook-tls
  dnsNames:
    - my-webhook-service.default.svc
    - my-webhook-service.default.svc.cluster.local
  issuerRef:
    name: selfsigned-issuer
    kind: Issuer
```

Then load from the generated Secret:

```go
secret, err := client.CoreV1().Secrets("default").Get(ctx, "webhook-tls", metav1.GetOptions{})
if err != nil {
 log.Fatal(err)
}

certPEM := secret.Data["tls.crt"]
keyPEM := secret.Data["tls.key"]
```

### Option 3: Helm-Managed Certificates

Configure certificates via Helm values and mount as files:

```yaml
# values.yaml
webhook:
  tls:
    cert: |
      -----BEGIN CERTIFICATE-----
      ...
      -----END CERTIFICATE-----
    key: |
      -----BEGIN PRIVATE KEY-----
      ...
      -----END PRIVATE KEY-----
```

```go
// Load from mounted files
certPEM, err := ioutil.ReadFile("/etc/webhook/certs/tls.crt")
keyPEM, err := ioutil.ReadFile("/etc/webhook/certs/tls.key")
```

## Webhook Server

### Configuration

```go
server := webhook.NewServer(&webhook.ServerConfig{
 Port:         9443,                  // HTTPS port
 BindAddress:  "0.0.0.0",             // Listen address
 CertPEM:      certPEM,               // Server certificate
 KeyPEM:       keyPEM,                // Server private key
 Path:         "/validate",           // Webhook endpoint path
 ReadTimeout:  10 * time.Second,      // Request read timeout
 WriteTimeout: 10 * time.Second,      // Response write timeout
})
```

### Registering Validators

Validators are registered by GVK (Group/Version.Kind) string:

```go
// Core API group (empty group prefix)
server.RegisterValidator("v1.Pod", validatePod)
server.RegisterValidator("v1.Service", validateService)
server.RegisterValidator("v1.ConfigMap", validateConfigMap)

// Named API groups
server.RegisterValidator("networking.k8s.io/v1.Ingress", validateIngress)
server.RegisterValidator("apps/v1.Deployment", validateDeployment)
```

### ValidationContext Structure

The ValidationContext provides complete admission request information:

```go
type ValidationContext struct {
 // Object is the resource being validated (*unstructured.Unstructured)
 // For CREATE: the object being created
 // For UPDATE: the new version
 // For DELETE: the object being deleted
 Object *unstructured.Unstructured

 // OldObject is the existing version (UPDATE/DELETE only)
 OldObject *unstructured.Unstructured

 // Operation type: "CREATE", "UPDATE", "DELETE", "CONNECT"
 Operation string

 // Resource metadata
 Namespace string  // Empty for cluster-scoped resources
 Name      string  // May be empty for CREATE with generateName
 UID       string  // Unique request identifier

 // User information for authorization decisions
 UserInfo interface{}
}
```

### Validation Function Signature

```go
type ValidationFunc func(ctx *ValidationContext) (allowed bool, reason string, err error)

// allowed: Whether the resource should be admitted
// reason: Human-readable reason for denial (empty if allowed)
// err: Internal error during validation (results in 500 response)
```

### Graceful Shutdown

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

// Start server in goroutine
go func() {
 if err := server.Start(ctx); err != nil {
  log.Printf("Server error: %v", err)
 }
}()

// Wait for shutdown signal
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
<-sigCh

// Cancel context to trigger graceful shutdown
cancel()
```

## ValidatingWebhookConfiguration Management

This library does not include dynamic webhook configuration management. The ValidatingWebhookConfiguration should be created via:

1. **Helm Chart** (Recommended for production)
2. **kubectl apply** with static manifests
3. **Kubernetes client** in your controller initialization

### Example Helm Template

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingWebhookConfiguration
metadata:
  name: {{ .Release.Name }}-webhook
webhooks:
  - name: ingress.validation.example.com
    clientConfig:
      service:
        name: {{ .Values.webhook.serviceName }}
        namespace: {{ .Release.Namespace }}
        path: /validate
      caBundle: {{ .Values.webhook.caBundle | b64enc }}
    rules:
      - apiGroups: ["networking.k8s.io"]
        apiVersions: ["v1"]
        operations: ["CREATE", "UPDATE"]
        resources: ["ingresses"]
    admissionReviewVersions: ["v1"]
    sideEffects: None
    failurePolicy: Fail
    timeoutSeconds: 10
```

### Example Static Manifest

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingWebhookConfiguration
metadata:
  name: my-webhook
webhooks:
  - name: validate-ingress.example.com
    clientConfig:
      service:
        name: my-webhook-service
        namespace: default
        path: /validate
      # CA bundle from certificate source (base64 encoded)
      caBundle: LS0tLS1CRUdJTi...
    rules:
      - apiGroups: ["networking.k8s.io"]
        apiVersions: ["v1"]
        operations: ["CREATE", "UPDATE"]
        resources: ["ingresses"]
    admissionReviewVersions: ["v1"]
    sideEffects: None
```

## Advanced Validation Patterns

### Operation-Specific Validation

```go
func validateResource(ctx *webhook.ValidationContext) (bool, string, error) {
 switch ctx.Operation {
 case "CREATE":
  return validateCreate(ctx.Object)
 case "UPDATE":
  return validateUpdate(ctx.OldObject, ctx.Object)
 case "DELETE":
  return validateDelete(ctx.Object)
 default:
  return true, "", nil
 }
}
```

### Immutability Checks

```go
func validateUpdate(old, new *unstructured.Unstructured) (bool, string, error) {
 // Extract immutable field
 oldValue, _, _ := unstructured.NestedString(old.Object, "spec", "immutableField")
 newValue, _, _ := unstructured.NestedString(new.Object, "spec", "immutableField")

 if oldValue != newValue {
  return false, "field spec.immutableField is immutable", nil
 }

 return true, "", nil
}
```

### Conditional Validation

```go
func validateIngress(ctx *webhook.ValidationContext) (bool, string, error) {
 // Extract annotations
 annotations := ctx.Object.GetAnnotations()

 // Skip validation if annotation present
 if _, skip := annotations["skip-validation"]; skip {
  return true, "", nil
 }

 // Continue with validation...
 return validateIngressRules(ctx.Object)
}
```

### Context-Aware Validation with External APIs

```go
func validateWithAPI(valCtx *webhook.ValidationContext) (bool, string, error) {
 // Create timeout context for external call
 ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
 defer cancel()

 // Call external API for validation
 valid, err := externalAPI.Validate(ctx, valCtx.Object)
 if err != nil {
  // Internal error - return as error
  return false, "", fmt.Errorf("external validation failed: %w", err)
 }

 if !valid {
  // Validation failed - deny with reason
  return false, "resource rejected by external policy", nil
 }

 return true, "", nil
}
```

### Multi-Stage Validation

```go
type ValidationChain struct {
 validators []webhook.ValidationFunc
}

func (vc *ValidationChain) Validate(ctx *webhook.ValidationContext) (bool, string, error) {
 for _, validator := range vc.validators {
  allowed, reason, err := validator(ctx)
  if err != nil || !allowed {
   return allowed, reason, err
  }
 }
 return true, "", nil
}

// Usage
chain := &ValidationChain{
 validators: []webhook.ValidationFunc{
  validateStructure,
  validateBusinessRules,
  validateSecurity,
 },
}

server.RegisterValidator("v1.Ingress", chain.Validate)
```

## Error Handling

### Validation Errors vs Internal Errors

```go
func validateResource(ctx *webhook.ValidationContext) (bool, string, error) {
 // Validation failure (deny admission with message)
 if !isValid(ctx.Object) {
  return false, "resource does not meet requirements", nil
 }

 // Internal error (HTTP 500, triggers webhook retry)
 if err := checkExternalDependency(ctx.Object); err != nil {
  return false, "", fmt.Errorf("dependency check failed: %w", err)
 }

 return true, "", nil
}
```

### Recovering from Panics

The webhook server does not automatically recover from panics in validation functions.
Wrap validation logic if needed:

```go
func safeValidator(ctx *webhook.ValidationContext) (allowed bool, reason string, err error) {
 defer func() {
  if r := recover(); r != nil {
   allowed = false
   reason = ""
   err = fmt.Errorf("validation panic: %v", r)
  }
 }()

 // Validation logic that might panic
 return riskyValidation(ctx)
}
```

## Testing

### Testing Validators

```go
func TestValidateIngress(t *testing.T) {
 tests := []struct {
  name    string
  ctx     *webhook.ValidationContext
  allowed bool
  reason  string
  wantErr bool
 }{
  {
   name: "valid ingress",
   ctx: &webhook.ValidationContext{
    Object: &unstructured.Unstructured{
     Object: map[string]interface{}{
      "spec": map[string]interface{}{
       "rules": []interface{}{
        map[string]interface{}{"host": "example.com"},
       },
      },
     },
    },
    Operation: "CREATE",
   },
   allowed: true,
  },
  {
   name: "missing spec",
   ctx: &webhook.ValidationContext{
    Object: &unstructured.Unstructured{
     Object: map[string]interface{}{},
    },
    Operation: "CREATE",
   },
   allowed: false,
   reason:  "spec is required",
  },
 }

 for _, tt := range tests {
  t.Run(tt.name, func(t *testing.T) {
   allowed, reason, err := validateIngress(tt.ctx)

   if tt.wantErr && err == nil {
    t.Error("expected error, got nil")
   }
   if !tt.wantErr && err != nil {
    t.Errorf("unexpected error: %v", err)
   }
   if allowed != tt.allowed {
    t.Errorf("allowed = %v, want %v", allowed, tt.allowed)
   }
   if reason != tt.reason {
    t.Errorf("reason = %q, want %q", reason, tt.reason)
   }
  })
 }
}
```

### Testing Server (Integration Test)

```go
func TestWebhookServer(t *testing.T) {
 // Load test certificates
 certPEM, keyPEM := loadTestCertificates()

 // Create server
 server := webhook.NewServer(&webhook.ServerConfig{
  Port:    0, // Random port for testing
  CertPEM: certPEM,
  KeyPEM:  keyPEM,
 })

 // Register test validator
 server.RegisterValidator("v1.ConfigMap", func(ctx *webhook.ValidationContext) (bool, string, error) {
  return true, "", nil
 })

 // Start server
 ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
 defer cancel()

 go server.Start(ctx)

 // Test webhook endpoint
 // ... make HTTPS request to server ...
}
```

## Performance Considerations

### Validation Timeout

Validation functions should complete quickly (typically < 1 second). The default webhook timeout is 10 seconds.

```go
// Good - fast validation
func fastValidator(ctx *webhook.ValidationContext) (bool, string, error) {
 // Quick checks only
 return checkBasicRules(ctx.Object)
}

// Bad - slow validation
func slowValidator(ctx *webhook.ValidationContext) (bool, string, error) {
 // Avoid expensive operations
 time.Sleep(5 * time.Second)  // DON'T DO THIS
 return checkComplexRules(ctx.Object)
}
```

### Concurrent Requests

The server handles multiple requests concurrently. Ensure validation functions are thread-safe:

```go
// Bad - race condition
var cache map[string]bool  // Shared state without synchronization

func racyValidator(ctx *webhook.ValidationContext) (bool, string, error) {
 cache[getKey(ctx.Object)] = true  // Race condition!
 return true, "", nil
}

// Good - thread-safe
var (
 cache = make(map[string]bool)
 mu    sync.RWMutex
)

func safeValidator(ctx *webhook.ValidationContext) (bool, string, error) {
 key := getKey(ctx.Object)

 mu.Lock()
 cache[key] = true
 mu.Unlock()

 return true, "", nil
}
```

## Troubleshooting

### Webhook Not Called

1. Check ValidatingWebhookConfiguration exists:

   ```bash
   kubectl get validatingwebhookconfigurations
   ```

2. Verify CA bundle is correct in webhook config

3. Check Service and Pod are running:

   ```bash
   kubectl get svc,pods
   ```

4. Test webhook endpoint directly:

   ```bash
   kubectl port-forward pod/webhook-pod 9443:9443
   curl -k https://localhost:9443/healthz
   ```

### Certificate Errors

1. Verify certificate validity:

   ```bash
   openssl x509 -in server.crt -text -noout
   ```

2. Check DNS SANs match service name:

   ```bash
   openssl x509 -in server.crt -text -noout | grep DNS
   ```

3. Verify CA bundle in webhook configuration matches CA cert that signed server cert

### Validation Failures

1. Check webhook server logs for validation errors

2. Test validator function independently with unit tests

3. Verify AdmissionReview request format matches expectations

## See Also

- [Kubernetes Admission Controllers](https://kubernetes.io/docs/reference/access-authn-authz/admission-controllers/)
- [Dynamic Admission Control](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/)
- [ValidatingWebhookConfiguration](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.28/#validatingwebhookconfiguration-v1-admissionregistration-k8s-io)
