# Ingress Annotations

## Overview

The controller supports annotations on Ingress resources for configuring HAProxy features. This page documents the available annotations and their usage.

## Basic Authentication

Enable HTTP basic authentication on Ingress resources using these annotations:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: protected-app
  annotations:
    haproxy.org/auth-type: "basic-auth"
    haproxy.org/auth-secret: "my-auth-secret"
    haproxy.org/auth-realm: "Protected Application"
spec:
  ingressClassName: haptic
  rules:
    - host: app.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: my-service
                port:
                  number: 80
```

**Annotations:**

| Annotation | Description | Required | Default |
|------------|-------------|----------|---------|
| `haproxy.org/auth-type` | Authentication type | Yes | - |
| `haproxy.org/auth-secret` | Secret name containing credentials | Yes | - |
| `haproxy.org/auth-realm` | HTTP auth realm shown to users | No | `"Restricted Area"` |

**Supported authentication types:**

- `basic-auth`: HTTP basic authentication with username/password

**Secret reference formats:**

- `"secret-name"`: Secret in same namespace as Ingress
- `"namespace/secret-name"`: Secret in specific namespace

### Creating Authentication Secrets

Secrets must contain username-password pairs where values are **base64-encoded crypt(3) SHA-512 password hashes**:

```bash
# Generate SHA-512 hash and encode for Kubernetes
HASH=$(openssl passwd -6 mypassword)

# Create secret with encoded hash
kubectl create secret generic my-auth-secret \
  --from-literal=admin=$(echo -n "$HASH" | base64 -w0) \
  --from-literal=user=$(echo -n "$HASH" | base64 -w0)
```

**Secret structure:**

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-auth-secret
type: Opaque
data:
  # Keys are usernames, values are base64-encoded password hashes
  admin: JDYkMVd3c2YxNmprcDBkMVBpTyRkS3FHUTF0SW0uOGF1VlJIcVA3dVcuMVV5dVNtZ3YveEc3dEFiOXdZNzc1REw3ZGE0N0hIeVB4ZllDS1BMTktZclJvMHRNQWQyQk1YUHBDd2Z5ZW03MA==
  user: JDYkbkdxOHJ1T2kyd3l4MUtyZyQ1a2d1azEzb2tKWmpzZ2Z2c3JqdmkvOVoxQjZIbDRUcGVvdkpzb2lQeHA2eGRKWUpha21wUmIwSUVHb1ZUSC8zRzZrLmRMRzBuVUNMWEZnMEhTRTJ5MA==
```

**Important:**

- Multiple Ingress resources can reference the same secret
- Secrets are fetched on-demand (requires `store: on-demand` in secrets configuration)
- Password hashes must use crypt(3) SHA-512 format for HAProxy compatibility

## Validation Sidecar

Enable the validation sidecar to test configurations before deployment:

```yaml
validation:
  enabled: true
```

This adds HAProxy + Dataplane sidecars to the controller pod for config validation.
