# SSL library

Serve HTTPS with certificates stored in Kubernetes Secrets. The SSL library
is enabled by default and works with the Ingress and Gateway libraries to
choose a certificate for each hostname. It also supports TLS passthrough, where
the backend terminates the encrypted connection.

To supply certificates for your domains, start with [SSL certificates](../ssl-certificates.md).
Use this reference when extending the certificate templates, configuring
Online Certificate Status Protocol (OCSP) stapling, or changing TLS defaults.

<a id="overview"></a>

Try switching one sample route from TLS termination to passthrough:

<div class="pg-embed" markdown data-scenario="all" data-facade="spec.templateSnippets.util-haproxytech-ssl-passthrough" data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="Pass an encrypted connection to the backend" data-height="440">

<p class="pg-task" markdown>In the **Resources** panel, give the `shop` Ingress the annotation `haproxy.org/ssl-passthrough: "true"` (add an `annotations:` block under its `metadata:`), then open the **haproxy.cfg** tab and watch a new `frontend ssl-tcp` appear alongside a `backend ssl-passthrough-storefront-shop`.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

The annotation adds a TCP backend for `shop.example.com`. The shared passthrough
frontend reads the connection's SNI and forwards the encrypted stream to that
backend. The **certs** tab doesn't change because HAProxy doesn't terminate TLS
for this route.

</details>

</div>

## Configuration

```yaml
controller:
  templateLibraries:
    ssl:
      enabled: true  # Enabled by default
```

### Default SSL certificate

Set a default certificate for clients whose Server Name Indication (SNI)
doesn't match a configured hostname:

```yaml
defaultSSLCertificate:
  secretName: default-ssl-cert
  namespace: haptic            # defaults to the Helm release namespace
```

The Secret must contain `tls.crt` and `tls.key`. For automatic renewal,
manual certificates, or a different namespace, follow the
[certificate guide](../ssl-certificates.md).

<a id="features"></a>

## TLS behavior

### HTTPS frontend

The default HTTPS port is 443, with HTTP/2 and HTTP/1.1 support. Change
`haproxy.ports.https` to keep the HAProxy listener and Service target port aligned.
Gateway listeners use [dedicated Services and pod ports](gateway.md#per-gateway-kubernetes-resources).

### CRT-list certificate management

The library generates a certificate list that maps hostnames to TLS certificates:

```
namespace_secretname.pem [ocsp-update on] host1.example.com host2.example.com
default.pem [ocsp-update on]
```

### OCSP stapling

Each entry includes `ocsp-update on`, which asks HAProxy to fetch and cache
Online Certificate Status Protocol (OCSP) responses for that certificate.

### Restricting frontend TLS versions and ciphers

Use [`extraContext.tls`](../ssl-certificates.md#tls-cipher-suites-and-protocol-versions)
for the shared frontend cipher and protocol policy. Gateway listeners can
[override that policy](gateway.md#gateway-tls-policies). For TLS between HAProxy
and an application, use [backend TLS annotations](haptic-annotations.md#backend-tls-to-the-upstream)
or Gateway API BackendTLSPolicy.

### SSL passthrough

Set `haproxy-haptic.org/ssl-passthrough: "true"` on an Ingress to send encrypted
connections directly to the backend selected by Server Name Indication (SNI).
The backend supplies the certificate and terminates TLS. Other hosts can
continue using HAProxy TLS termination on the same port.

## Watched resources

| Resource | API Version | Purpose |
|----------|-------------|---------|
| Secrets | v1 | Load TLS certificates (`kubernetes.io/tls` type) |

<a id="validation-tests"></a>

## Requirements

Use one of HAPTIC's [supported HAProxy versions](../operations/haproxy-versions.md).
The chart selects matching versions for configuration validation and the HAProxy pods.

## Extension points

<a id="extension-points-used"></a>

### Extension points provided

The SSL library provides infrastructure for other libraries to register TLS features:

| Data Structure | Purpose | How to Use |
|----------------|---------|------------|
| `gf["tlsCertificates"]` | Array of TLS certificates to include in CRT-list | Append `{secret_namespace, secret_name, sni_patterns[]}` |
| `gf["sslPassthroughBackends"]` | Array of SSL passthrough backends | Append `{name, sni}` |
| `https-bind-extra-*` | Additional binds in the HTTPS frontend | Provide a snippet matching the glob; use `{{ render "util-ssl-bind-options" }}` to reuse the configured certificate list and protocol settings. |

#### Adding HTTPS binds via `https-bind-extra-*`

Create a snippet whose name starts with `https-bind-extra-` and emit one bind
per additional port. Reuse `util-ssl-bind-options` to inherit the
configured certificate list and Application-Layer Protocol Negotiation (ALPN)
settings. Avoid ports already used by another bind.

Use `http-bind-extra-*` for additional plain-HTTP binds. See the
[base library extension points](base.md#extension-points) for the full list.

**Example - Registering a TLS certificate (from ingress.yaml):**

```scriggo
{%- var parts = split(tls.secretName, "/") %}
{%- var cert = map[string]any{
    "secret_namespace": len(parts) > 1 ? parts[0] : ingress.metadata.namespace,
    "secret_name": parts[len(parts)-1],
    "sni_patterns": tls.hosts,
} %}
{%- var certs []any = gf["tlsCertificates"].([]any) %}
{%- gf["tlsCertificates"] = append(certs, cert) %}
```

## See also

- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [Base Library](base.md) - Core configuration infrastructure
- [Ingress Library](ingress.md) - Ingress TLS configuration
- [haproxytech library](haproxytech.md) - SSL passthrough annotation
