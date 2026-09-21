# Backend mTLS with SPIFFE/SPIRE

Use [SPIFFE/SPIRE](https://spiffe.io/) to give HAProxy automatic mutual TLS (mTLS) to backend services: SPIRE issues and rotates short-lived X.509 certificates, and HAPTIC wires them into the backend configuration.

## Overview

[SPIFFE](https://spiffe.io/docs/latest/spiffe-about/overview/) (Secure Production Identity Framework for Everyone) is a set of standards for securely identifying workloads in dynamic environments. [SPIRE](https://spiffe.io/docs/latest/spire-about/spire-concepts/) is the reference implementation that issues and manages SPIFFE Verifiable Identity Documents (SVIDs) — short-lived X.509 certificates that serve as workload identity.

This integration delivers certificates to HAProxy without storing them in
Kubernetes Secrets:

- **Automatic identity** — SPIRE attests HAProxy pods and issues X.509-SVIDs based on Kubernetes service account identity
- **Short-lived certificates** — each SPIFFE Verifiable Identity Document (SVID) is automatically rotated at half of its TTL (for example every 12 hours with a `24h` TTL), reducing the impact of credential compromise
- **Pod-local files** — spiffe-helper writes the certificate, private key, and trust bundle to the HAProxy pod's shared volume
- **Runtime rotation** — the cert-reloader sidecar updates loaded certificates through `set ssl cert` and `set ssl ca-file`

## Prerequisites

Before following this guide, ensure:

- **SPIRE server and agents** are deployed in your cluster
- **SPIRE Container Storage Interface (CSI) driver** (`csi.spiffe.io`) is installed for exposing the Workload API socket to pods
- **Workload registration** exists for the HAProxy pod's service account and namespace
- A HAPTIC installation you can update through [Helm values](../deploying-with-helm.md)

## Configuration

### HAProxy Pod setup

Add the following sections to your existing Helm values. Combine entries under
the same `controller`, `haproxy`, and `extraDeploy` keys; duplicate YAML keys
would replace earlier sections. Keep any sidecars or volumes you already use.

```yaml
haproxy:
  # Restart pods when spiffe-helper or other sidecar configs change
  podSpec:
    podAnnotations:
      checksum/extra-config: '{{ toJson .Values.extraDeploy | sha256sum }}'

  # Create cert directory before spiffe-helper starts
  initContainers:
    - name: create-spiffe-dir
      image: busybox:1.37
      command: ["mkdir", "-p", "/etc/haproxy/spiffe"]
      volumeMounts:
        - name: haproxy-runtime
          mountPath: /etc/haproxy
      resources:
        requests:
          cpu: 10m
          memory: 16Mi
        limits:
          memory: 16Mi
      securityContext:
        allowPrivilegeEscalation: false
        capabilities:
          drop: [ALL]
        runAsUser: 99
        runAsNonRoot: true

  sidecars:
    - name: spiffe-helper
      image: ghcr.io/spiffe/spiffe-helper:0.11.0
      args: ["-config", "/etc/spiffe-helper/helper.conf"]
      volumeMounts:
        - name: spiffe-workload-api
          mountPath: /spiffe-workload-api
          readOnly: true
        - name: haproxy-runtime
          mountPath: /etc/haproxy
        - name: spiffe-helper-config
          mountPath: /etc/spiffe-helper
          readOnly: true
      livenessProbe:
        httpGet:
          path: /live
          port: 8081
        initialDelaySeconds: 5
        periodSeconds: 15
      readinessProbe:
        httpGet:
          path: /ready
          port: 8081
        initialDelaySeconds: 5
        periodSeconds: 10
      resources:
        requests:
          cpu: 10m
          memory: 32Mi
        limits:
          memory: 64Mi
      securityContext:
        allowPrivilegeEscalation: false
        capabilities:
          drop: [ALL]
        # Must match HAProxy UID (99) for file ownership
        runAsUser: 99
        runAsNonRoot: true
    - name: cert-reloader
      image: haproxytech/haproxy-debian:3.4
      command: ["sh", "-c"]
      args:
        - |
          CERT=/etc/haproxy/spiffe/svid.pem
          KEY=/etc/haproxy/spiffe/svid.pem.key
          BUNDLE=/etc/haproxy/spiffe/bundle.pem
          SOCK=/etc/haproxy/haproxy-master.sock
          previous_digest=""
          runtime_command() {
            printf '%s\n\n' "$1" | socat -t 5 - "unix-connect:$SOCK"
          }
          install_pem() {
            kind=$1
            path=$2
            pem=$3
            command=$(printf '@1 set ssl %s %s <<\n%s' "$kind" "$path" "$pem")
            response=$(runtime_command "$command") || return 1
            if ! printf '%s' "$response" | grep -qi transaction; then
              runtime_command "@1 abort ssl $kind $path" >/dev/null
              return 1
            fi
            response=$(runtime_command "@1 commit ssl $kind $path") || return 1
            if ! printf '%s' "$response" | grep -q 'Success!'; then
              runtime_command "@1 abort ssl $kind $path" >/dev/null
              return 1
            fi
          }
          echo "cert-reloader: polling for cert changes"
          while true; do
            sleep 5
            [ -f "$CERT" ] && [ -f "$KEY" ] && [ -f "$BUNDLE" ] || continue
            digest=$(sha256sum "$CERT" "$KEY" "$BUNDLE") || continue
            [ "$digest" = "$previous_digest" ] && continue
            cert_pem=$(cat "$CERT" "$KEY") || continue
            ca_pem=$(cat "$BUNDLE") || continue
            [ "$digest" = "$(sha256sum "$CERT" "$KEY" "$BUNDLE")" ] || continue
            if install_pem cert "$CERT" "$cert_pem" &&
               install_pem ca-file "$BUNDLE" "$ca_pem"; then
              previous_digest=$digest
              echo "cert-reloader: certificates updated via runtime API"
            else
              echo "cert-reloader: update failed; retrying in 5 seconds" >&2
            fi
          done
      volumeMounts:
        - name: haproxy-runtime
          mountPath: /etc/haproxy
      resources:
        requests:
          cpu: 10m
          memory: 16Mi
        limits:
          memory: 32Mi
      securityContext:
        allowPrivilegeEscalation: false
        capabilities:
          drop: [ALL]
        runAsUser: 99
        runAsNonRoot: true

  extraVolumes:
    - name: spiffe-workload-api
      csi:
        driver: csi.spiffe.io
        readOnly: true
    - name: spiffe-helper-config
      configMap:
        name: '{{ include "haptic.fullname" . }}-spiffe-helper-config'
```

!!! note
    Both spiffe-helper and cert-reloader must run as **UID 99** (matching HAProxy) so that certificate files have the correct ownership.

!!! note
    The spiffe-helper container image tags do **not** use a `v` prefix — use `0.11.0`, not `v0.11.0`.

The cert-reloader sidecar reuses the `haproxytech/haproxy-debian` image, which includes `socat` and `sha256sum`. Use the same image tag as your HAProxy container if you want both containers to share the downloaded image. It uses the `@1` prefix to route Runtime API commands to the current HAProxy worker process via the master socket. It checks HAProxy's responses and retries failed updates, including the first update after startup. Until an Ingress uses the certificate, HAProxy has no certificate store to update and the sidecar reports retries.

### `spiffe-helper` configuration

Create a ConfigMap with the spiffe-helper configuration using `extraDeploy`. The configuration format is [HashiCorp Configuration Language (HCL)](https://github.com/hashicorp/hcl) (not TOML or `.ini` syntax):

```yaml
extraDeploy:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: '{{ include "haptic.fullname" . }}-spiffe-helper-config'
      labels:
        app.kubernetes.io/name: haptic
        app.kubernetes.io/instance: '{{ .Release.Name }}'
        app.kubernetes.io/component: spiffe-helper
    data:
      helper.conf: |
        agent_address = "/spiffe-workload-api/spire-agent.sock"
        cert_dir = "/etc/haproxy/spiffe"
        svid_file_name = "svid.pem"
        svid_key_file_name = "svid.pem.key"
        svid_bundle_file_name = "bundle.pem"
        daemon_mode = true

        health_checks {
          listener_enabled = true
          bind_port = "8081"
          liveness_path = "/live"
          readiness_path = "/ready"
        }
```

!!! warning
    The `health_checks` block uses **HCL block syntax** (`health_checks { ... }`), not TOML section syntax (`[health_checks]`). Using the wrong format causes a parse error.

### Backend mTLS via custom annotation

To enable per-Ingress backend mTLS using the SPIRE certificates, add a custom `templateSnippet` that processes an annotation (for example, `example.com/server-mtls-spire`):

```yaml
controller:
  config:
    templateSnippets:
      backend-directives-800-server-mtls-spire:
        template: |
          {%- if ingress != nil %}
            {%- var spireMtls = ingress | dig("metadata", "annotations",
                "example.com/server-mtls-spire") | fallback("") | tostring() %}
            {%- if spireMtls == "true" %}
              {%- var ns = ingress | dig("metadata", "namespace")
                  | fallback("") | tostring() %}
              {%- var name = ingress | dig("metadata", "name")
                  | fallback("") | tostring() %}
              {%- var key = ns + "/" + name %}

              {#- Conflict detection -#}
              {%- var serverSsl = ingress | dig("metadata", "annotations",
                  "haproxy.org/server-ssl") | fallback("") | tostring() %}
              {%- var serverCrt = ingress | dig("metadata", "annotations",
                  "haproxy.org/server-crt") | fallback("") | tostring() %}
              {%- var serverCa = ingress | dig("metadata", "annotations",
                  "haproxy.org/server-ca") | fallback("") | tostring() %}
              {%- if serverSsl == "true" %}
                {{- fail("Ingress '" + key +
                    "': server-mtls-spire conflicts with server-ssl") -}}
              {%- end %}
              {%- if serverCrt != "" %}
                {{- fail("Ingress '" + key +
                    "': server-mtls-spire conflicts with server-crt") -}}
              {%- end %}
              {%- if serverCa != "" %}
                {{- fail("Ingress '" + key +
                    "': server-mtls-spire conflicts with server-ca") -}}
              {%- end %}

              {#- Add SPIRE mTLS flags to default-server -#}
              {%- var serviceDns = tostring(svcName) + "." +
                  tostring(ns) + ".svc" %}
              {%- serverOpts["flags"] = append(serverOpts["flags"].([]any),
                  "ssl verify required " +
                  "ca-file /etc/haproxy/spiffe/bundle.pem " +
                  "crt /etc/haproxy/spiffe/svid.pem " +
                  "sni str(" + serviceDns + ")") %}
            {%- end %}
          {%- end %}
```

This snippet:

- Runs at **priority 800** (before `backend-directives-900-haproxytech-advanced`), so conflicts are detected before the built-in annotations are processed
- Uses **absolute paths** for the certificate files because HAProxy's `crt-base` directive points to the `ssl/` directory, and the SPIRE certs are in `/etc/haproxy/spiffe/`. HAProxy auto-discovers the private key at `<certfile>.key` (here `svid.pem.key`), so no explicit `key` keyword is needed
- **Fails the render** if the annotation is used together with `haproxy.org/server-ssl`, `haproxy.org/server-crt`, or `haproxy.org/server-ca`, since these configure conflicting SSL modes
- Sets **`sni str(<service>.<namespace>.svc)`** to send the Kubernetes service DNS name as SNI, enabling hostname verification against DNS Subject Alternative Name (SAN) entries populated by SPIRE's `autoPopulateDNSNames` (see [DNS SAN configuration](#dns-san-configuration) below)

!!! note "Why explicit SNI matters"
    HAProxy 3.3+ automatically sends the server address as SNI (`sni-auto`). In Kubernetes, backends are addressed by pod IP, so the verify callback tries to match the IP against DNS-type SANs — which SPIFFE certificates don't have. Setting `sni str(...)` explicitly overrides `sni-auto` on all HAProxy versions and provides proper hostname verification via the service DNS name.

To use it, annotate your Ingress:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: my-backend
  annotations:
    example.com/server-mtls-spire: "true"
spec:
  ingressClassName: haptic
  rules:
    - host: my-backend.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: my-backend
                port:
                  number: 443
```

This produces the following `default-server` line in the generated HAProxy config:

```haproxy
backend default_my-backend_svc_my-backend_https
    default-server check ssl verify required ca-file /etc/haproxy/spiffe/bundle.pem crt /etc/haproxy/spiffe/svid.pem sni str(my-backend.default.svc)
```

### DNS SAN configuration

The `sni str(...)` directive in the snippet above requires that backend SVIDs include DNS SANs matching the Kubernetes service name. Enable [`autoPopulateDNSNames`](https://github.com/spiffe/spire-controller-manager/blob/main/docs/clusterspiffeid-crd.md) on the default ClusterSPIFFEID so that SPIRE automatically adds service DNS names (for example `my-backend`, `my-backend.default.svc`, `my-backend.default.svc.cluster.local`) as DNS SANs in all SVIDs:

```yaml
apiVersion: spire.spiffe.io/v1alpha1
kind: ClusterSPIFFEID
metadata:
  name: spire-default
spec:
  spiffeIDTemplate: "spiffe://{{ .TrustDomain }}/ns/{{ .PodMeta.Namespace }}/sa/{{ .PodSpec.ServiceAccountName }}"
  autoPopulateDNSNames: true
```

If you use the [SPIRE Helm chart](https://artifacthub.io/packages/helm/spiffe/spire), set this via values:

```yaml
spire-server:
  controllerManager:
    identities:
      clusterSPIFFEIDs:
        default:
          autoPopulateDNSNames: true
```

!!! note
    `autoPopulateDNSNames` populates DNS SANs based on the Kubernetes services each pod is an endpoint of. Both HAProxy and backend pods receive DNS SANs for their respective services. Since certificate updates are pushed via the Runtime API without process restarts, using the default SVID TTL (typically `1h`) is fine.

## Controller validation

The HAPTIC controller checks rendered configuration with its local `haproxy -c` binary. Since the SPIRE certificates only exist on the HAProxy pods (managed by spiffe-helper), the controller pod needs placeholder files at the same absolute paths so that validation passes.

Mount a ConfigMap with dummy PEM files on the **controller** pod:

```yaml
# Dummy certs for controller-side "haproxy -c" validation
# (not real secrets — see ConfigMap below)
controller:
  extraVolumes:
    - name: spiffe-validation-certs
      configMap:
        name: spiffe-validation-certs

  extraVolumeMounts:
    - name: spiffe-validation-certs
      mountPath: /etc/haproxy/spiffe
      readOnly: true
```

Create the validation ConfigMap before applying those Helm values. These files
are validation fixtures, never credentials for backend connections; mount them
only on the controller. The HAProxy pods continue to obtain real credentials
from SPIRE.

```bash
HAPTIC_VALIDATION_DIR=$(mktemp -d)
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
  -keyout "$HAPTIC_VALIDATION_DIR/tls.key" \
  -out "$HAPTIC_VALIDATION_DIR/tls.crt" -days 3650 -nodes \
  -subj '/CN=validation-placeholder'
kubectl create configmap spiffe-validation-certs --namespace haptic \
  --from-file=svid.pem="$HAPTIC_VALIDATION_DIR/tls.crt" \
  --from-file=svid.pem.key="$HAPTIC_VALIDATION_DIR/tls.key" \
  --from-file=bundle.pem="$HAPTIC_VALIDATION_DIR/tls.crt" \
  --dry-run=client -o yaml | kubectl apply -f -
rm -r "$HAPTIC_VALIDATION_DIR"
```

The controller's `extraVolumes` and `extraVolumeMounts` are separate from
`haproxy.extraVolumes`. Apply the combined Helm values through your release
workflow, keeping pre-rollout and admission validation enabled.

## Verification

After deploying, verify the integration is working:

```bash
# Check spiffe-helper received certificates
kubectl -n haptic logs deployment/haptic-haproxy -c spiffe-helper

# Expected output:
# level=info msg="Received update" spiffe_id="spiffe://..." system=spiffe-helper
# level=info msg="X.509 certificates updated" system=spiffe-helper
```

```bash
# Verify certificate files exist on the HAProxy pod
kubectl -n haptic exec deployment/haptic-haproxy -c haproxy -- ls -la /etc/haproxy/spiffe/

# Expected: svid.pem, svid.pem.key, bundle.pem owned by UID 99
```

```bash
# Inspect the SPIFFE ID in the issued certificate
kubectl -n haptic exec deployment/haptic-haproxy -c haproxy -- \
  openssl x509 -in /etc/haproxy/spiffe/svid.pem -noout -text \
  | grep -A1 "Subject Alternative Name"

# Expected: URI:spiffe://<trust-domain>/ns/<namespace>/sa/<service-account>
```

```bash
# Verify the backend mTLS annotation is reflected in HAProxy config
kubectl -n haptic exec deployment/haptic-haproxy -c haproxy -- \
  cat /etc/haproxy/haproxy.cfg | grep -A2 'default-server.*ssl.*verify'
```

```bash
# Check cert-reloader is running and updating certificates
kubectl -n haptic logs deployment/haptic-haproxy -c cert-reloader

# Expected output after a rotation:
# cert-reloader: polling for cert changes
# cert-reloader: certificates updated via runtime API
```

## Troubleshooting

<a id="spiffe-helper-cannot-connect-to-spire-agent"></a>

### `spiffe-helper` can't connect to SPIRE agent

```
Error while watching x509 context: ... dial unix /spiffe-workload-api/agent.sock: no such file or directory
```

The SPIRE CSI driver creates the socket as `spire-agent.sock`, not `agent.sock`. Verify the correct socket name:

```bash
kubectl -n haptic exec deployment/haptic-haproxy -c spiffe-helper -- ls /spiffe-workload-api/
```

Update `agent_address` in your spiffe-helper config to match.

### `spiffe-helper` config parse error

```
failed to parse configuration ... got: LBRACK
```

spiffe-helper uses **HCL** syntax, not TOML. Replace `[section]` with `section { ... }`:

```hcl
# Wrong (TOML)
[health_checks]
listener_enabled = true

# Correct (HCL)
health_checks {
  listener_enabled = true
}
```

<a id="certificate-directory-does-not-exist"></a>

### Certificate directory doesn't exist

```
Unable to dump bundle ... open /etc/haproxy/spiffe/svid.pem: no such file or directory
```

The `haproxy-runtime` emptyDir doesn't include the `spiffe/` subdirectory by default. Ensure the init container is configured to create it before spiffe-helper starts. The example init container above already sets `resources.requests` and `resources.limits`; keep them in place if you customized it, so the init container isn't rejected by a namespace ResourceQuota.

### `ImagePullBackOff` for `spiffe-helper`

```
Back-off pulling image "ghcr.io/spiffe/spiffe-helper:v0.11.0"
```

The spiffe-helper container image uses tags **without** the `v` prefix. Use `0.11.0`, not `v0.11.0`.

### Controller rejects config with cert path errors

If the controller logs show validation failures referencing `/etc/haproxy/spiffe/*.pem`, the validation placeholder ConfigMap isn't mounted on the controller pod. Verify:

```bash
kubectl -n haptic exec <controller-pod> -- ls /etc/haproxy/spiffe/
# Should list: bundle.pem  svid.pem  svid.pem.key
```

## See also

- [Security Guide](./security.md) — TLS configuration and credential management
- [Chart Values Reference](../reference.md) — `haproxy.sidecars`, `haproxy.initContainers`, `extraDeploy`
- [SPIFFE/SPIRE Documentation](https://spiffe.io/docs/latest/) — SPIFFE concepts, SPIRE deployment, workload registration
- [spiffe-helper on GitHub](https://github.com/spiffe/spiffe-helper) — Configuration reference and release notes
- [Templating Guide](../templating.md) — Writing custom `templateSnippets`
