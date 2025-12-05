# pkg/webhook - Admission Webhook Library

Development context for the admission webhook library.

**API Documentation**: See `pkg/webhook/README.md`
**Architecture**: See `/docs/development/design.md` (Webhook section - to be added)

## When to Work Here

Modify this package when:

- Improving certificate generation or rotation logic
- Enhancing webhook server performance
- Adding new webhook configuration options
- Fixing AdmissionReview parsing issues
- Improving error handling in validation flow

**DO NOT** modify this package for:

- Validation business logic → Use `pkg/controller/webhook`
- Event handling → Use `pkg/controller/webhook`
- Configuration types → Use `pkg/core/config`
- Metrics → Use `pkg/controller/webhook`

## Key Design Principle

This is a **pure library** with NO dependencies on other project packages. It could be extracted and used in any Kubernetes controller project.

Dependencies: Only standard library + k8s.io/api + k8s.io/apimachinery + k8s.io/client-go

## Package Structure

```
pkg/webhook/
├── types.go         # Core interfaces and types
├── certs.go         # Certificate generation and rotation
├── server.go        # HTTPS webhook server
├── config.go        # Dynamic webhook configuration management
├── certs_test.go    # Certificate tests
├── server_test.go   # Server tests
├── config_test.go   # Configuration manager tests
├── README.md        # User documentation
└── CLAUDE.md        # This file
```

## Core Concepts

### 1. Certificate Management

**Self-Signed CA Approach:**

- Generate CA certificate (valid for 1 year)
- Sign server certificates with CA
- Inject CA bundle into ValidatingWebhookConfiguration
- Rotate server cert when < 30 days until expiry

**Why self-signed?**

- No external dependencies (cert-manager, etc.)
- Full automation
- Suitable for internal cluster communication
- CA cert never leaves the cluster

**DNS SANs:**
Server certificates include all possible DNS names for the service:

- `service-name`
- `service-name.namespace`
- `service-name.namespace.svc`
- `service-name.namespace.svc.cluster.local`

This ensures the webhook works regardless of how the API server connects.

### 2. Webhook Server

**HTTPS Only:**
Kubernetes requires webhooks to use HTTPS with valid certificates. This is enforced by the API server.

**AdmissionReview Handling:**

1. Receive POST request with AdmissionReview (v1)
2. Extract AdmissionRequest
3. Parse resource object from request.Object.Raw
4. Call registered validator for resource type
5. Build AdmissionResponse with result
6. Return AdmissionReview with response

**GVK Mapping:**
Resources are identified by "group/version.Kind" strings:

- Core types: "v1.Pod", "v1.Service"
- Named groups: "networking.k8s.io/v1.Ingress", "apps/v1.Deployment"

### 3. Dynamic Configuration

**No Static Manifests:**
The webhook configuration is created and managed programmatically. No manual YAML files.

**Update Flow:**

1. Create ValidatingWebhookConfiguration with initial rules
2. When rules change, call CreateOrUpdate()
3. Existing configuration is patched with new rules
4. When CA cert rotates, update CA bundle and call CreateOrUpdate()

**Multiple Webhooks:**
Each webhook rule becomes a separate webhook in the configuration. This allows different failure policies and timeouts per resource type.

## Testing Approach

### Unit Tests

Test each component in isolation without dependencies:

```go
func TestCertificateManager_Generate(t *testing.T) {
 certMgr := NewCertificateManager(CertConfig{
  Namespace:   "test",
  ServiceName: "test-webhook",
 })

 certs, err := certMgr.Generate()

 require.NoError(t, err)
 assert.NotNil(t, certs.CACert)
 assert.NotNil(t, certs.ServerCert)

 // Verify CA cert is valid
 caCert, err := ParseCertificatePEM(certs.CACert)
 require.NoError(t, err)
 assert.True(t, caCert.IsCA)

 // Verify server cert is signed by CA
 serverCert, err := ParseCertificatePEM(certs.ServerCert)
 require.NoError(t, err)
 assert.Contains(t, serverCert.DNSNames, "test-webhook.test.svc")
}
```

### Integration Tests

Test interactions between components:

```go
func TestWebhookEndToEnd(t *testing.T) {
 // Generate certificates
 certMgr := NewCertificateManager(CertConfig{
  Namespace:   "test",
  ServiceName: "test-webhook",
 })
 certs, err := certMgr.Generate()
 require.NoError(t, err)

 // Create server
 server := NewServer(ServerConfig{
  Port:    9443,
  CertPEM: certs.ServerCert,
  KeyPEM:  certs.ServerKey,
 })

 // Register validator
 called := false
 server.RegisterValidator("v1.ConfigMap", func(obj interface{}) (bool, string, error) {
  called = true
  return true, "", nil
 })

 // Start server
 ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
 defer cancel()

 go server.Start(ctx)

 // Wait for server to start
 time.Sleep(100 * time.Millisecond)

 // Make test request
 // ... HTTP client with TLS ...

 assert.True(t, called)
}
```

## Common Pitfalls

### Certificate Expiry

**Problem**: Not checking certificate validity before starting server.

```go
// Bad - start server with expired cert
server := NewServer(ServerConfig{
 CertPEM: oldCerts.ServerCert,  // Might be expired!
 KeyPEM:  oldCerts.ServerKey,
})
server.Start(ctx)
```

**Solution**: Check expiry before starting.

```go
// Good - verify cert validity
if certMgr.NeedsRotation(certs) {
 certs, err = certMgr.Generate()
 if err != nil {
  log.Fatal(err)
 }
}

server := NewServer(ServerConfig{
 CertPEM: certs.ServerCert,
 KeyPEM:  certs.ServerKey,
})
```

### Missing CA Bundle

**Problem**: Creating webhook configuration without CA bundle.

```go
// Bad - CA bundle not set
configMgr := NewConfigManager(client, WebhookConfigSpec{
 Name: "my-webhook",
 // CABundle not set!
 Rules: []WebhookRule{...},
})
```

**Solution**: Always provide CA bundle.

```go
// Good - CA bundle from certificates
configMgr := NewConfigManager(client, WebhookConfigSpec{
 Name:     "my-webhook",
 CABundle: certs.CACert,  // Required!
 Rules:    []WebhookRule{...},
})
```

### Wrong Service Name

**Problem**: Service name in cert doesn't match actual service.

```go
// Bad - service name mismatch
certMgr := NewCertificateManager(CertConfig{
 Namespace:   "default",
 ServiceName: "webhook",  // But actual service is "my-webhook-svc"
})
```

**Solution**: Ensure names match exactly.

```go
// Good - consistent naming
serviceName := "my-webhook-svc"

certMgr := NewCertificateManager(CertConfig{
 Namespace:   "default",
 ServiceName: serviceName,
})

configMgr := NewConfigManager(client, WebhookConfigSpec{
 ServiceName: serviceName,  // Same name
})
```

### Blocking Validation

**Problem**: Validation function takes too long.

```go
// Bad - slow validation
func validateResource(obj interface{}) (bool, string, error) {
 time.Sleep(15 * time.Second)  // Exceeds webhook timeout!
 return true, "", nil
}
```

**Solution**: Keep validation fast (< 1 second).

```go
// Good - fast validation
func validateResource(obj interface{}) (bool, string, error) {
 // Quick checks only
 if err := validateBasicRules(obj); err != nil {
  return false, err.Error(), nil
 }
 return true, "", nil
}
```

### Not Handling Nil Values

**Problem**: Validation panics on nil values in resource.

```go
// Bad - panics if spec is nil
func validateResource(obj interface{}) (bool, string, error) {
 resource := obj.(map[string]interface{})
 spec := resource["spec"].(map[string]interface{})  // Panic if nil!
 return true, "", nil
}
```

**Solution**: Check for nil values.

```go
// Good - safe nil handling
func validateResource(obj interface{}) (bool, string, error) {
 resource, ok := obj.(map[string]interface{})
 if !ok {
  return false, "", fmt.Errorf("invalid object type")
 }

 spec, ok := resource["spec"].(map[string]interface{})
 if !ok {
  return false, "spec is required", nil
 }

 return true, "", nil
}
```

## Performance Optimization

### Certificate Generation

Certificate generation is CPU-intensive (RSA key generation). Consider:

```go
// Cache certificates between restarts
func loadOrGenerateCerts(certMgr *CertificateManager) (*Certificates, error) {
 // Try to load from Secret
 certs, err := loadCertsFromSecret()
 if err == nil && !certMgr.NeedsRotation(certs) {
  return certs, nil
 }

 // Generate new certificates
 certs, err = certMgr.Generate()
 if err != nil {
  return nil, err
 }

 // Save to Secret
 if err := saveCertsToSecret(certs); err != nil {
  return nil, err
 }

 return certs, nil
}
```

### Concurrent Validations

The server handles concurrent requests. Avoid shared state in validators:

```go
// Bad - shared state
var validationCache = make(map[string]bool)

func validateWithCache(obj interface{}) (bool, string, error) {
 key := computeKey(obj)
 if valid, exists := validationCache[key]; exists {  // Race condition!
  return valid, "", nil
 }
 // ...
}

// Good - no shared state or use mutex
var (
 validationCache = make(map[string]bool)
 cacheMu         sync.RWMutex
)

func validateWithCache(obj interface{}) (bool, string, error) {
 key := computeKey(obj)

 cacheMu.RLock()
 valid, exists := validationCache[key]
 cacheMu.RUnlock()

 if exists {
  return valid, "", nil
 }
 // ...
}
```

## Certificate Rotation Strategy

### Rotation Triggers

1. **Periodic Check**: Monitor certificate expiry (every 24 hours)
2. **On Startup**: Check if rotation needed before starting server
3. **Manual Trigger**: API for forcing rotation

### Rotation Flow

```go
func (cm *CertificateManager) RotateIfNeeded(current *Certificates) (*Certificates, bool, error) {
 if !cm.NeedsRotation(current) {
  return current, false, nil
 }

 // Generate new certificates
 newCerts, err := cm.Generate()
 if err != nil {
  return nil, false, err
 }

 return newCerts, true, nil
}

// Usage in controller
ticker := time.NewTicker(24 * time.Hour)
defer ticker.Stop()

for {
 select {
 case <-ticker.C:
  newCerts, rotated, err := certMgr.RotateIfNeeded(certs)
  if err != nil {
   log.Error("rotation failed", "error", err)
   continue
  }

  if rotated {
   log.Info("certificates rotated")

   // Update server (requires restart)
   // Update webhook configuration
   // Save to Secret

   certs = newCerts
  }
 case <-ctx.Done():
  return
 }
}
```

## Webhook Configuration Best Practices

### Failure Policy

```go
// Fail-closed (default) - reject requests if webhook unavailable
// Use for critical validation
fail := admissionv1.Fail
spec.FailurePolicy = &fail

// Fail-open - allow requests if webhook unavailable
// Use for non-critical validation or debugging
ignore := admissionv1.Ignore
spec.FailurePolicy = &ignore
```

### Scope

```go
// Validate only namespaced resources
namespaced := admissionv1.NamespacedScope
rule.Scope = &namespaced

// Validate only cluster-scoped resources
clusterScoped := admissionv1.ClusterScope
rule.Scope = &clusterScoped

// Validate all resources (default)
allScopes := admissionv1.AllScopes
rule.Scope = &allScopes
```

### Operations

```go
// Validate only creation
rule.Operations = []admissionv1.OperationType{admissionv1.Create}

// Validate creation and updates (most common)
rule.Operations = []admissionv1.OperationType{
 admissionv1.Create,
 admissionv1.Update,
}

// Validate all operations including deletion
rule.Operations = []admissionv1.OperationType{
 admissionv1.Create,
 admissionv1.Update,
 admissionv1.Delete,
}
```

## Troubleshooting

### Debug Checklist

1. **Certificates**
   - Are certificates valid (not expired)?
   - Do DNS SANs include service name?
   - Does CA bundle match CA cert?

2. **Service**
   - Is service pointing to correct pods?
   - Are pods running and ready?
   - Is port correct (usually 9443)?

3. **Webhook Configuration**
   - Does ValidatingWebhookConfiguration exist?
   - Are rules correct (APIGroups, Resources)?
   - Is CA bundle correct?
   - Is service reference correct (namespace, name, path)?

4. **Network**
   - Can API server reach webhook service?
   - Are there network policies blocking traffic?
   - Is TLS handshake succeeding?

### Common Errors

**"x509: certificate signed by unknown authority"**

- CA bundle in webhook configuration doesn't match actual CA cert
- Fix: Update CA bundle with correct CA certificate

**"no such host"**

- Service name in certificate doesn't match actual service
- Fix: Regenerate certificates with correct service name

**"context deadline exceeded"**

- Webhook server not responding within timeout
- Validation function taking too long
- Fix: Reduce validation complexity, increase timeout

**"connection refused"**

- Webhook server not running
- Wrong port
- Fix: Verify server is started and listening on correct port

## Extension Considerations

### Adding Mutating Webhooks

Current implementation supports only ValidatingWebhooks. To add mutating webhooks:

1. Add MutatingWebhookConfiguration support to ConfigManager
2. Add patch generation to server (return JSONPatch in AdmissionResponse)
3. Register mutating functions alongside validators

### Custom Admission Logic

For complex admission logic requiring multiple validators:

```go
type ValidatorChain struct {
 validators []ValidationFunc
}

func (vc *ValidatorChain) Validate(obj interface{}) (bool, string, error) {
 for _, validator := range vc.validators {
  allowed, reason, err := validator(obj)
  if err != nil || !allowed {
   return allowed, reason, err
  }
 }
 return true, "", nil
}
```

### Async Validation

For validation requiring external APIs:

```go
func asyncValidator(obj interface{}) (bool, string, error) {
 ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
 defer cancel()

 resultCh := make(chan validationResult, 1)

 go func() {
  // Expensive validation in goroutine
  valid, reason := checkExternalAPI(ctx, obj)
  resultCh <- validationResult{valid, reason}
 }()

 select {
 case result := <-resultCh:
  return result.valid, result.reason, nil
 case <-ctx.Done():
  return false, "", fmt.Errorf("validation timeout")
 }
}
```

## Resources

- API documentation: `pkg/webhook/README.md`
- Kubernetes webhook docs: <https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/>
- AdmissionReview reference: <https://kubernetes.io/docs/reference/config-api/apiserver-webhooks.v1/>
- TLS with Go: <https://pkg.go.dev/crypto/tls>
