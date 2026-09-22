# Ingress annotations

<a id="overview"></a>

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

## Quick start: Basic authentication

This example requires HAPTIC and a Service named `my-service` on port 80 in your
current namespace. Use an HTTPS endpoint before sending real credentials; see
[SSL certificates](ssl-certificates.md).

Create the credentials Secret. OpenSSL prompts for the password:

```bash
HAPTIC_AUTH_HASH=$(openssl passwd -6)
kubectl create secret generic my-auth-secret \
  --from-literal=admin="$HAPTIC_AUTH_HASH"
```

Pass the raw password hash to `--from-literal`. `kubectl` encodes it for the Secret;
encoding it yourself first makes the stored hash unusable for authentication.

Save this Ingress as `protected-app.yaml`:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: protected-app
  annotations:
    haproxy-haptic.org/auth-type: "basic"
    haproxy-haptic.org/auth-secret: "my-auth-secret"
    haproxy-haptic.org/auth-secret-type: "auth-map"
    haproxy-haptic.org/auth-realm: "Protected Application"
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

Apply it in the same namespace as the Service and Secret:

```bash
kubectl apply -f protected-app.yaml
```

### Check authentication

Forward the default installation's HAProxy Service to your terminal:

```bash
kubectl port-forward --namespace haptic svc/haptic-haproxy 8080:80
```

In another terminal, send a request without credentials:

```bash
curl -i -H 'Host: app.example.com' http://localhost:8080/
```

Expect `401 Unauthorized`. Repeat with the username; curl prompts for its password:

```bash
curl -i --user admin -H 'Host: app.example.com' http://localhost:8080/
```

With the correct password, the request reaches `my-service`. Stop port forwarding
with **Ctrl+C** when finished. For an unexpected result, use
[routing troubleshooting](troubleshooting.md#routing-issues).

See [native authentication annotations](./libraries/haptic-annotations.md#authentication-mtls-and-waf)
for Secret formats and other authentication settings.

<a id="supported-features"></a>

## Keep annotations from another controller

Use the [compatibility comparison](annotation-compatibility.md) to choose a
vendor library and check which features it supports. Enable it through
[Helm values](template-libraries.md#enabling-and-disabling-libraries) before
moving the corresponding Ingresses to HAPTIC.

Configure each feature through one annotation family. Conflicting settings
from two enabled families cause admission rejection and a warning during live
rendering. See the [migration guide](migrating.md) for a staged cutover.
