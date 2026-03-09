# Ingress Annotations

## Overview

HAPTIC supports annotations on Ingress resources through two template libraries, each providing compatibility with a different HAProxy ingress controller:

| Compatible with | Annotation prefix | Library docs |
|-----------------|-------------------|--------------|
| [haproxytech/kubernetes-ingress](https://github.com/haproxytech/kubernetes-ingress) (vendor IC) | `haproxy.org/` | [haproxytech library →](./libraries/haproxytech.md) |
| [jcmoraisjr/haproxy-ingress](https://haproxy-ingress.github.io/) (community IC) | `haproxy-ingress.github.io/` | [haproxy-ingress library →](./libraries/haproxy-ingress.md) |

Both libraries are enabled by default and work independently — you can use annotations from either prefix on the same Ingress. If you are migrating from a specific controller, its annotation prefix is the one you already know. If you are starting fresh, enabling both gives you the widest annotation coverage.

See [Template Libraries](./template-libraries.md) for how to enable or disable individual libraries.

## Supported Features

Both libraries cover the same HAProxy feature areas through their respective annotation prefixes:

| Feature | `haproxy.org/` | `haproxy-ingress.github.io/` |
|---------|----------------|-------------------------------|
| Basic authentication | `auth-type`, `auth-secret`, `auth-realm` | `auth-secret`, `auth-realm` |
| Allowlist / Denylist | `allowlist`, `denylist` | `allowlist-source-range`, `denylist-source-range` |
| SSL redirect | `ssl-redirect`, `ssl-redirect-code` | `ssl-redirect`, `ssl-redirect-code` |
| SSL passthrough | `ssl-passthrough` | `ssl-passthrough` |
| Backend SSL / mTLS | `server-ssl`, `server-proto`, `server-sni`, `server-ca`, `server-crt` | `secure-backends`, `backend-protocol`, `secure-sni`, `secure-verify-ca-secret`, `secure-crt-secret` |
| CORS | `cors-enable`, `cors-allow-origin`, … | `cors-enable`, `cors-allow-origin`, … |
| Load balancing | `load-balance` | `balance-algorithm` |
| Session affinity (cookies) | `cookie-persistence` | `affinity`, `session-cookie-*` |
| Rate limiting | `rate-limit-requests`, `rate-limit-period`, … | — |
| Timeouts | `timeout-server`, `timeout-connect`, … | `timeout-server`, `timeout-connect`, … |
| Health checks | `check`, `check-http`, `check-interval` | `backend-check-interval`, `health-check-uri`, … |
| HSTS | — | `hsts`, `hsts-max-age`, … |
| Request / response headers | `request-set-header`, `response-set-header` | `headers` |
| Path rewriting | `path-rewrite` | — |
| PROXY protocol | `send-proxy-protocol` | `proxy-protocol` |
| Raw backend config | `backend-config-snippet` | `config-backend` |

For the complete per-annotation reference with examples and generated HAProxy configuration output, see the library docs:

- [haproxytech library →](./libraries/haproxytech.md)
- [haproxy-ingress library →](./libraries/haproxy-ingress.md)

## Quick Start: Basic Authentication

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

Create the secret with crypt(3) SHA-512 password hashes:

```bash
HASH=$(openssl passwd -6 mypassword)
kubectl create secret generic my-auth-secret \
  --from-literal=admin=$(echo -n "$HASH" | base64 -w0)
```

See [haproxytech library — Basic Authentication](./libraries/haproxytech.md#authentication) for the full reference including secret format, cross-namespace secrets, and generated HAProxy config.
