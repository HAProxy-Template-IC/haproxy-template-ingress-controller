# Ingress annotations

## Overview

Use Ingress annotations to configure route behavior without writing templates.
The native `haproxy-haptic.org/*` library is enabled by default. For migrations,
three optional libraries support annotations from other ingress controllers:

| Library | Annotation prefix | Library docs |
|---------|-------------------|--------------|
| HAPTIC native | `haproxy-haptic.org/` | [haptic-annotations library →](./libraries/haptic-annotations.md) |
| [haproxytech/kubernetes-ingress](https://github.com/haproxytech/kubernetes-ingress) (vendor ingress controller) | `haproxy.org/` | [haproxytech library →](./libraries/haproxytech.md) |
| [jcmoraisjr/haproxy-ingress](https://haproxy-ingress.github.io/) (community ingress controller) | `haproxy-ingress.github.io/` | [haproxy-ingress library →](./libraries/haproxy-ingress.md) |
| [kubernetes/ingress-nginx](https://kubernetes.github.io/ingress-nginx/) (nginx ingress controller) | `nginx.ingress.kubernetes.io/` | [nginx-ingress library →](./libraries/nginx-ingress.md) |

For new configuration, use the native annotations. To retain existing annotations
during a migration, enable the matching vendor library. Check its supported
annotations and limits before switching traffic.

You can mix prefixes on one Ingress, but configure each feature through one
annotation family. Configuring the same feature through two enabled families
causes admission rejection and a warning during live rendering.

See [Template Libraries](./template-libraries.md) for how to enable or disable individual libraries.

See the nginx-ingress compatibility verdict render live:

<div class="pg-embed" markdown data-scenario="nginx-ingress" data-facade="resources" data-tab="migration" data-controls="tabs" data-title="nginx-ingress annotation migration report" data-height="440">

</div>

## Supported features

Compare the vendor libraries below. For native annotations and additional
capabilities, see the [HAPTIC annotation reference](./libraries/haptic-annotations.md).

| Feature | `haproxy.org/` | `haproxy-ingress.github.io/` | `nginx.ingress.kubernetes.io/` |
|---------|----------------|-------------------------------|--------------------------------|
| Basic authentication | `auth-type`, `auth-secret`, `auth-realm` | `auth-secret`, `auth-realm` | `auth-type`, `auth-secret`, `auth-secret-type`, `auth-realm`, `satisfy` |
| External authentication ([Stream Processing Offload Agent (SPOA) hub](operations/spoa-hub.md)) | — | `auth-url`, `auth-signin`, `auth-method`, `auth-headers-request`, `auth-headers-succeed`, `auth-headers-fail` | `auth-url`, `auth-signin`, `auth-method`, `auth-response-headers` |
| OAuth2 proxy | — | `oauth`, `oauth-uri-prefix`, `oauth-headers` | — |
| Client certificate (incoming mTLS) | — | `auth-tls-secret`, `auth-tls-verify-client`, `auth-tls-error-page`, `auth-tls-cert-header` | `auth-tls-secret`, `auth-tls-verify-client`, `auth-tls-error-page`, `auth-tls-pass-certificate-to-upstream` |
| Allowlist / Denylist | `allow-list`, `deny-list` | `allowlist-source-range`, `denylist-source-range` | `whitelist-source-range`, `denylist-source-range` |
| SSL redirect | `ssl-redirect`, `ssl-redirect-code` | `ssl-redirect`, `ssl-redirect-code` | `ssl-redirect`, `force-ssl-redirect` |
| URL redirects | `request-redirect`, `request-redirect-code` | `redirect-to`, `redirect-to-code`, `app-root`, `default-backend-redirect`, … | `permanent-redirect`, `temporal-redirect`, `from-to-www-redirect`, `app-root`, … |
| SSL passthrough | `ssl-passthrough` | `ssl-passthrough` | `ssl-passthrough` |
| Backend SSL / mTLS | `server-ssl`, `server-proto`, `server-ca`, `server-crt` | `secure-backends`, `backend-protocol`, `secure-sni`, `secure-verify-ca-secret`, `secure-crt-secret`, `ssl-ciphers-backend`, … | `backend-protocol`, `proxy-ssl-secret`, `proxy-ssl-verify`, `proxy-ssl-name`, … |
| Cross-Origin Resource Sharing (CORS) | `cors-enable`, `cors-allow-origin`, … | `cors-enable`, `cors-allow-origin`, … | `enable-cors`, `cors-allow-origin`, … |
| Load balancing | `load-balance` | `balance-algorithm` | `load-balance`, `upstream-hash-by` |
| Session affinity / sticky sessions (cookies) | `cookie-persistence` | `affinity`, `session-cookie-*` | `affinity`, `session-cookie-*` |
| Rate limiting | `rate-limit-requests`, `rate-limit-period`, … | `limit-rps`, `limit-rpm`, `limit-whitelist` | `limit-rps`, `limit-rpm`, `limit-connections`, `limit-whitelist` |
| Bandwidth throttling | — | — | `limit-rate`, `limit-rate-after` |
| Request body size limit | — | `proxy-body-size` | `proxy-body-size` |
| Timeouts | `timeout-server`, `timeout-connect`, … | `timeout-server`, `timeout-connect`, … | `proxy-connect-timeout`, `proxy-read-timeout`, `proxy-send-timeout` |
| Retries | — | — | `proxy-next-upstream`, `proxy-next-upstream-tries` |
| Health checks | `check`, `check-http`, `check-interval` | `backend-check-interval`, `health-check-uri`, … | — |
| Agent checks | — | `agent-check-port`, `agent-check-addr`, … | — |
| HTTP Strict Transport Security (HSTS) | — | `hsts`, `hsts-max-age`, … | `hsts`, `hsts-max-age`, … |
| Request / response headers | `request-set-header`, `response-set-header` | `headers`, `forwardfor` | `custom-request-headers`, `custom-response-headers` |
| Path rewriting | `path-rewrite` | `rewrite-target` | `rewrite-target` |
| Server aliases | — | `server-alias`, `server-alias-regex` | `server-alias` |
| Per-host default backend | — | — | `default-backend` |
| Canary deployments | — | — | `canary`, `canary-by-header`, `canary-weight`, … |
| Request mirroring | — | — | `mirror-target` |
| Web Application Firewall (WAF) / ModSecurity | — | `waf`, `waf-mode` | `modsecurity-snippet`, `enable-modsecurity` |
| PROXY protocol | `send-proxy-protocol` | `proxy-protocol` | `use-proxy-protocol` |
| Raw backend config | `backend-config-snippet` | `config-backend` | `configuration-snippet` |
| Raw global / frontend / defaults config | — | `config-global`, `config-frontend`, `config-defaults` | — |

For the complete per-annotation reference with examples and generated HAProxy configuration output, see the library docs:

- [haproxytech library →](./libraries/haproxytech.md)
- [haproxy-ingress library →](./libraries/haproxy-ingress.md)
- [nginx-ingress library →](./libraries/nginx-ingress.md)

## Quick start: Basic authentication

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
  --from-literal=admin="$HASH"
```

Pass the raw password hash to `--from-literal`. `kubectl` encodes it for the Secret;
encoding it yourself first makes the stored hash unusable for authentication.

See [haproxytech library — Basic Authentication](./libraries/haproxytech.md#authentication) for the full reference including secret format, cross-namespace secrets, and generated HAProxy config.
