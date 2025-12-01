# Security Guide

This guide covers security best practices for deploying and operating the HAProxy Template Ingress Controller.

## Overview

The controller follows security best practices including:
- Principle of least privilege for RBAC
- Read-only filesystem for controller containers
- Secure credential management via Kubernetes Secrets
- Support for TLS throughout the stack

## RBAC Configuration

### Controller Service Account

The Helm chart creates a service account with minimal required permissions:

```yaml
# Automatically created by Helm
apiVersion: v1
kind: ServiceAccount
metadata:
  name: haproxy-template-ic
```

### ClusterRole Permissions

The controller requires these Kubernetes API permissions:

| Resource | Verbs | Purpose |
|----------|-------|---------|
| pods, namespaces | get, list, watch | Discover HAProxy pods and namespaces |
| ingresses | get, list, watch | Watch Ingress resources |
| services, endpoints, endpointslices | get, list, watch | Discover backend endpoints |
| secrets | get, list, watch | Load TLS certificates and credentials |
| leases (coordination.k8s.io) | get, create, update | Leader election for HA deployments |
| haproxytemplateconfigs | get, list, watch | Watch configuration CRD |

**Customizing RBAC**:

```yaml
# values.yaml
rbac:
  create: true  # Set to false to manage RBAC manually
```

If you manage RBAC manually, ensure the service account has access to all resources defined in your `watchedResources` configuration.

### Restricting Namespace Access

By default, the controller watches resources cluster-wide. To restrict to specific namespaces:

```yaml
# HAProxyTemplateConfig CRD
spec:
  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      namespaceSelector:
        matchNames:
          - production
          - staging
```

## Credential Management

### DataPlane API Credentials

The controller uses credentials to authenticate with the HAProxy DataPlane API:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: haproxy-credentials
type: Opaque
stringData:
  dataplane_username: admin
  dataplane_password: <strong-password>
  # For validation sidecar (optional)
  validation_username: validator
  validation_password: <validation-password>
```

**Best practices:**
- Use strong, randomly generated passwords
- Rotate credentials periodically
- Use different credentials for production and validation sidecars
- Consider using a secrets manager (Vault, External Secrets Operator)

### External Secrets Integration

Integrate with external secrets managers:

```yaml
# External Secrets Operator example
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: haproxy-credentials
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: vault-backend
    kind: ClusterSecretStore
  target:
    name: haproxy-credentials
  data:
    - secretKey: dataplane_username
      remoteRef:
        key: haproxy/dataplane
        property: username
    - secretKey: dataplane_password
      remoteRef:
        key: haproxy/dataplane
        property: password
```

### Debug Endpoint Security

The debug endpoints do NOT expose actual credentials:

```bash
# /debug/vars/credentials returns only metadata
curl http://localhost:6060/debug/vars/credentials
```

Response:
```json
{
  "version": "12345",
  "has_dataplane_creds": true
}
```

Actual passwords are never exposed through debug endpoints.

## Container Security

### Read-Only Filesystem

The controller runs with a read-only root filesystem:

```yaml
# Enabled by default in Helm chart
securityContext:
  readOnlyRootFilesystem: true
```

Temporary files (for validation) are written to `/tmp` which is mounted as `emptyDir`.

### Security Context

Recommended security context:

```yaml
# values.yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 1000
  runAsGroup: 1000
  readOnlyRootFilesystem: true
  allowPrivilegeEscalation: false
  capabilities:
    drop:
      - ALL
```

### Pod Security Standards

The controller is compatible with Kubernetes Pod Security Standards at the "restricted" level:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: haproxy-template-ic
  labels:
    pod-security.kubernetes.io/enforce: restricted
    pod-security.kubernetes.io/audit: restricted
    pod-security.kubernetes.io/warn: restricted
```

## Network Policies

### Restricting Controller Traffic

Limit controller network access:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: haproxy-template-ic
  namespace: haproxy-template-ic
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: haproxy-template-ic
  policyTypes:
    - Ingress
    - Egress
  ingress:
    # Allow health checks
    - from: []
      ports:
        - port: 8080  # healthz
        - port: 9090  # metrics
  egress:
    # Allow Kubernetes API access
    - to:
        - namespaceSelector: {}
          podSelector:
            matchLabels:
              component: kube-apiserver
      ports:
        - port: 443
    # Allow HAProxy DataPlane API access
    - to:
        - podSelector:
            matchLabels:
              app: haproxy
      ports:
        - port: 5555  # DataPlane API
```

### Restricting Debug Endpoint Access

If you enable the debug port, restrict access:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: haproxy-template-ic-debug
  namespace: haproxy-template-ic
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: haproxy-template-ic
  policyTypes:
    - Ingress
  ingress:
    # Only allow debug access from specific namespace
    - from:
        - namespaceSelector:
            matchLabels:
              name: monitoring
      ports:
        - port: 6060  # debug
```

## TLS Configuration

### TLS for Ingress Traffic

Configure TLS termination through Ingress resources:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: my-app
spec:
  tls:
    - hosts:
        - myapp.example.com
      secretName: myapp-tls
  rules:
    - host: myapp.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: my-app
                port:
                  number: 80
```

### TLS Certificates from Secrets

The controller loads TLS certificates from Kubernetes Secrets:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: myapp-tls
type: kubernetes.io/tls
data:
  tls.crt: <base64-encoded-certificate>
  tls.key: <base64-encoded-private-key>
```

### Certificate Management with cert-manager

Integrate with cert-manager for automatic certificate management:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: myapp-tls
spec:
  secretName: myapp-tls
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
  dnsNames:
    - myapp.example.com
```

### HAProxy DataPlane API TLS

For production deployments, enable TLS for the DataPlane API:

```yaml
# HAProxy sidecar configuration
dataplane:
  insecure: false  # Require TLS
  ssl_certificate: /etc/haproxy/ssl/dataplane.crt
  ssl_key: /etc/haproxy/ssl/dataplane.key
```

## Secrets in Templates

### Secure Handling

When using secrets in templates, follow these practices:

```jinja2
{#- Load secret data - automatically base64 decoded -#}
{%- for secret in resources.secrets.List() %}
{%- if secret.metadata.name == "auth-users" %}
  {#- Use secret.data fields - they're decoded automatically -#}
  userlist authenticated_users
    user admin password {{ secret.data.password_hash }}
{%- endif %}
{%- endfor %}
```

**Best practices:**
- Never log secret values
- Use password hashes, not plaintext passwords
- Limit secret access to specific namespaces
- Rotate secrets regularly

### Password Hash Format

For HAProxy authentication, store password hashes (not plaintext):

```bash
# Generate bcrypt hash
htpasswd -nbB admin mypassword | cut -d: -f2

# Store in secret (hash only, not username:hash)
kubectl create secret generic auth-users \
  --from-literal=password_hash='$2y$05$...'
```

## Audit Logging

### Controller Logs

The controller logs security-relevant events:

```bash
# View security-related logs
kubectl logs -n haproxy-template-ic deployment/haproxy-template-ic | grep -E "auth|credential|secret"
```

### Kubernetes Audit Policy

Include controller operations in Kubernetes audit policy:

```yaml
apiVersion: audit.k8s.io/v1
kind: Policy
rules:
  # Audit secret access
  - level: Metadata
    resources:
      - group: ""
        resources: ["secrets"]
    users: ["system:serviceaccount:haproxy-template-ic:haproxy-template-ic"]

  # Audit configuration changes
  - level: RequestResponse
    resources:
      - group: "haproxy-template-ic.github.io"
        resources: ["haproxytemplateconfigs"]
```

## Security Checklist

### Deployment Checklist

- [ ] Use strong, unique passwords for DataPlane API credentials
- [ ] Enable read-only root filesystem
- [ ] Run as non-root user
- [ ] Drop all capabilities
- [ ] Enable network policies to restrict traffic
- [ ] Use TLS for all external endpoints
- [ ] Restrict RBAC to required namespaces
- [ ] Enable Kubernetes audit logging

### Operational Checklist

- [ ] Rotate credentials periodically
- [ ] Monitor for unauthorized access attempts
- [ ] Review RBAC permissions after configuration changes
- [ ] Keep controller image updated for security patches
- [ ] Use image scanning in CI/CD pipeline

### Production Hardening

For high-security environments:

```yaml
# values.yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 65534  # nobody
  runAsGroup: 65534
  fsGroup: 65534
  readOnlyRootFilesystem: true
  allowPrivilegeEscalation: false
  seccompProfile:
    type: RuntimeDefault
  capabilities:
    drop:
      - ALL

controller:
  debugPort: 0  # Disable debug endpoint

rbac:
  create: true
```

## See Also

- [Monitoring Guide](./monitoring.md) - Monitor security-related metrics
- [High Availability](./high-availability.md) - Secure HA deployments
- [Debugging Guide](./debugging.md) - Secure debugging practices
