# Ingress annotations

## Overview

HAPTIC supports annotations on Ingress resources through template libraries. Start with HAPTIC's own native vocabulary; three vendor libraries additionally provide drop-in compatibility with the annotation prefixes of specific upstream ingress controllers, for migrations:

| Library | Annotation prefix | Library docs |
|---------|-------------------|--------------|
| HAPTIC native (best-of-breed **superset** of all three below) | `haproxy-haptic.org/` | [haptic-annotations library →](./libraries/haptic-annotations.md) |
| [haproxytech/kubernetes-ingress](https://github.com/haproxytech/kubernetes-ingress) (vendor ingress controller) | `haproxy.org/` | [haproxytech library →](./libraries/haproxytech.md) |
| [jcmoraisjr/haproxy-ingress](https://haproxy-ingress.github.io/) (community ingress controller) | `haproxy-ingress.github.io/` | [haproxy-ingress library →](./libraries/haproxy-ingress.md) |
| [kubernetes/ingress-nginx](https://kubernetes.github.io/ingress-nginx/) (nginx ingress controller) | `nginx.ingress.kubernetes.io/` | [nginx-ingress library →](./libraries/nginx-ingress.md) |

All libraries work independently and coexist — you can mix prefixes on the same Ingress, as long as each *feature* is configured through a single family. Configuring one feature from two enabled families on the same Ingress is rejected by the admission webhook, and warned about on a live render. **For new configuration, use the native `haproxy-haptic.org/*` vocabulary**: one clean annotation per capability, covering everything the vendor libraries do. Only the native library is **enabled by default**; the three vendor libraries are **opt-in** migration aids. If you're coming from one of those controllers, enable the matching vendor library (`controller.templateLibraries.<name>.enabled: true`) to keep using its annotation prefix — then either stay on it, migrate to `haproxy-haptic.org/*` at your own pace, or run a mix of both.

See [Template Libraries](./template-libraries.md) for how to enable or disable individual libraries.

See the nginx-ingress compatibility verdict render live:

<div class="pg-embed" markdown data-scenario="nginx-ingress" data-facade="resources" data-tab="migration" data-controls="tabs" data-title="nginx-ingress annotation migration report" data-height="440">

</div>

## Supported features

The three vendor libraries cover the following HAProxy feature areas. The native `haproxy-haptic.org/*` library implements a **superset** of every row — see the [haptic-annotations reference](./libraries/haptic-annotations.md) for its canonical annotation per capability.

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

`kubectl` base64-encodes `--from-literal` values into the Secret's `data` for you, and the library decodes them once — so pass the raw hash, not a pre-`base64`'d copy (double-encoding makes the hash unparseable and auth silently fails).

See [haproxytech library — Basic Authentication](./libraries/haproxytech.md#authentication) for the full reference including secret format, cross-namespace secrets, and generated HAProxy config.
