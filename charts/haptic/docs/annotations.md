# Ingress Annotations

## Overview

HAPTIC supports annotations on Ingress resources through three template libraries, each providing compatibility with a different ingress controller:

| Compatible with | Annotation prefix | Library docs |
|-----------------|-------------------|--------------|
| [haproxytech/kubernetes-ingress](https://github.com/haproxytech/kubernetes-ingress) (vendor IC) | `haproxy.org/` | [haproxytech library →](./libraries/haproxytech.md) |
| [jcmoraisjr/haproxy-ingress](https://haproxy-ingress.github.io/) (community IC) | `haproxy-ingress.github.io/` | [haproxy-ingress library →](./libraries/haproxy-ingress.md) |
| [kubernetes/ingress-nginx](https://kubernetes.github.io/ingress-nginx/) (nginx IC) | `nginx.ingress.kubernetes.io/` | [nginx-ingress library →](./libraries/nginx-ingress.md) |

The two HAProxy libraries are enabled by default and work independently — you can use annotations from either prefix on the same Ingress. The nginx-ingress library is disabled by default and must be explicitly enabled. If you are migrating from a specific controller, its annotation prefix is the one you already know. If you are starting fresh, enabling the HAProxy libraries gives you the widest annotation coverage.

See [Template Libraries](./template-libraries.md) for how to enable or disable individual libraries.

## Supported Features

The libraries cover the following HAProxy feature areas through their respective annotation prefixes:

| Feature | `haproxy.org/` | `haproxy-ingress.github.io/` | `nginx.ingress.kubernetes.io/` |
|---------|----------------|-------------------------------|--------------------------------|
| Basic authentication | `auth-type`, `auth-secret`, `auth-realm` | `auth-secret`, `auth-realm` | `auth-type`, `auth-secret`, `auth-realm` |
| Allowlist / Denylist | `allowlist`, `denylist` | `allowlist-source-range`, `denylist-source-range` | `whitelist-source-range` |
| SSL redirect | `ssl-redirect`, `ssl-redirect-code` | `ssl-redirect`, `ssl-redirect-code` | `ssl-redirect` |
| SSL passthrough | `ssl-passthrough` | `ssl-passthrough` | `ssl-passthrough` |
| Backend SSL / mTLS | `server-ssl`, `server-proto`, `server-sni`, `server-ca`, `server-crt` | `secure-backends`, `backend-protocol`, `secure-sni`, `secure-verify-ca-secret`, `secure-crt-secret` | `backend-protocol` |
| CORS | `cors-enable`, `cors-allow-origin`, … | `cors-enable`, `cors-allow-origin`, … | `enable-cors`, `cors-allow-origin`, … |
| Load balancing | `load-balance` | `balance-algorithm` | `load-balance` |
| Session affinity (cookies) | `cookie-persistence` | `affinity`, `session-cookie-*` | `affinity`, `session-cookie-*` |
| Rate limiting | `rate-limit-requests`, `rate-limit-period`, … | — | — |
| Timeouts | `timeout-server`, `timeout-connect`, … | `timeout-server`, `timeout-connect`, … | `proxy-connect-timeout`, `proxy-read-timeout`, `proxy-send-timeout` |
| Health checks | `check`, `check-http`, `check-interval` | `backend-check-interval`, `health-check-uri`, … | — |
| HSTS | — | `hsts`, `hsts-max-age`, … | — |
| Request / response headers | `request-set-header`, `response-set-header` | `headers` | `custom-request-headers`, `custom-response-headers` |
| Path rewriting | `path-rewrite` | — | `rewrite-target` |
| PROXY protocol | `send-proxy-protocol` | `proxy-protocol` | — |
| Raw backend config | `backend-config-snippet` | `config-backend` | `configuration-snippet` |

For the complete per-annotation reference with examples and generated HAProxy configuration output, see the library docs:

- [haproxytech library →](./libraries/haproxytech.md)
- [haproxy-ingress library →](./libraries/haproxy-ingress.md)
- [nginx-ingress library →](./libraries/nginx-ingress.md)

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
