# Backend mTLS with SPIFFE/SPIRE

Use [SPIFFE/SPIRE](https://spiffe.io/) to give HAProxy automatic mutual TLS (mTLS) to backend services: SPIRE issues and rotates short-lived X.509 certificates, and HAPTIC wires them into the backend configuration.

## Overview

[SPIFFE](https://spiffe.io/docs/latest/spiffe-about/overview/) (Secure Production Identity Framework for Everyone) is a set of standards for securely identifying workloads in dynamic environments. [SPIRE](https://spiffe.io/docs/latest/spire-about/spire-concepts/) is the reference implementation that issues and manages SPIFFE Verifiable Identity Documents (SVIDs) — short-lived X.509 certificates that serve as workload identity.

When integrated with HAPTIC, SPIRE enables zero-trust mTLS to backends without managing certificates manually:

- **Automatic identity** — SPIRE attests HAProxy pods and issues X.509-SVIDs based on Kubernetes service account identity
- **Short-lived certificates** — each SPIFFE Verifiable Identity Document (SVID) is automatically rotated at half of its TTL (for example every 12 hours with a `24h` TTL), reducing the impact of credential compromise
- **No secrets in cluster** — Private keys are generated in-memory by the SPIRE agent and never stored as Kubernetes Secrets
- **Zero-reload rotation** — Certificate updates are pushed to HAProxy via the Runtime API (`set ssl cert`/`set ssl ca-file`), avoiding process restarts entirely

## Prerequisites

Before following this guide, ensure:

- **SPIRE server and agents** are deployed in your cluster
- **SPIRE Container Storage Interface (CSI) driver** (`csi.spiffe.io`) is installed for exposing the Workload API socket to pods
- **Workload registration** exists for the HAProxy pod's service account and namespace
- **HAPTIC Helm chart** version with `podAnnotations` and `sidecars` support

## Architecture

The integration uses four components working together inside the HAProxy pod:

```
┌───────────────────────────────────────────────────────────────┐
│ HAProxy Pod                                                   │
│                                                               │
│  ┌──────────┐   ┌───────────────┐   ┌────────────────┐        │
│  │ init:    │   │  haproxy      │   │ spiffe-helper  │        │
│  │ create-  │   │               │   │                │        │
│  │ spiffe-  │──▶│ Reads certs   │◀──│ Fetches SVIDs  │        │
│  │ dir      │   │ from shared   │   │ from SPIRE     │        │
│  │          │   │ volume        │   │ agent via CSI  │        │
│  └──────────┘   │               │   │                │        │
│                 │ /etc/haproxy/ │   │ Writes certs   │        │
│                 │   spiffe/     │   │ to shared vol  │        │
│                 │   ├ svid.pem  │   └────────────────┘        │
│                 │   ├ svid.pem  │                             │
│                 │   │   .key    │   ┌────────────────┐        │
│                 │   └ bundle    │   │ cert-reloader  │        │
│                 │       .pem    │   │                │        │
│                 │               │   │ Polls certs,   │        │
│                 │  master sock  │◀──│ pushes updates │        │
│                 │  (Runtime API)│   │ via Runtime API│        │
│                 └───────────────┘   └────────────────┘        │
│                                                               │
│  CSI Volume: /spiffe-workload-api/spire-agent.sock            │
└───────────────────────────────────────────────────────────────┘
```

**How it works:**

1. An **init container** creates the `/etc/haproxy/spiffe/` directory on the shared `haproxy-runtime` emptyDir volume
2. The **spiffe-helper** sidecar connects to the SPIRE agent via the CSI-mounted Workload API socket
3. SPIRE attests the pod's identity and issues an X.509-SVID
4. spiffe-helper writes the certificate, private key, and trust bundle to the shared volume
5. The **cert-reloader** sidecar polls for file changes every 5 seconds and pushes updated certificates to HAProxy via the Runtime API (`set ssl cert`, `set ssl ca-file`) — no process restart required
6. HAProxy uses these certificates for mTLS connections to backend services

## Configuration

### HAProxy Pod setup

Add the following to your Helm values to configure the HAProxy pod with spiffe-helper:

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
          PREV_MTIME=""
          echo "cert-reloader: polling for cert changes"
          while true; do
            sleep 5
            [ -f "$CERT" ] && [ -f "$KEY" ] && [ -f "$BUNDLE" ] || continue
            MTIME=$(stat -c %Y "$CERT" "$KEY" "$BUNDLE" 2>/dev/null | tr '\n' ':')
            [ "$MTIME" = "$PREV_MTIME" ] && continue
            [ -z "$PREV_MTIME" ] && { PREV_MTIME="$MTIME"; continue; }
            PREV_MTIME="$MTIME"
            sleep 1
            LOADED=$(echo "@1 show ssl cert $CERT" | socat - unix-connect:$SOCK 2>/dev/null | grep -c "^Filename:")
            if [ "$LOADED" -eq 0 ]; then
              echo "cert-reloader: cert not loaded in HAProxy, skipping runtime update"
              continue
            fi
            printf "@1 set ssl cert $CERT <<\n$(cat $CERT)\n$(cat $KEY)\n\n" | socat - unix-connect:$SOCK
            echo "@1 commit ssl cert $CERT" | socat - unix-connect:$SOCK
            CA_LOADED=$(echo "@1 show ssl ca-file $BUNDLE" | socat - unix-connect:$SOCK 2>/dev/null | grep -c "^Filename:")
            if [ "$CA_LOADED" -gt 0 ]; then
              printf "@1 set ssl ca-file $BUNDLE <<\n$(cat $BUNDLE)\n\n" | socat - unix-connect:$SOCK
              echo "@1 commit ssl ca-file $BUNDLE" | socat - unix-connect:$SOCK
            fi
            echo "cert-reloader: certificates updated via runtime API at $(date -Iseconds)"
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

The cert-reloader sidecar reuses the `haproxytech/haproxy-debian` image, which includes `socat` and `stat`. Pin its tag to the same version as `haproxyVersion` (the example uses `3.4`, the chart default) so the sidecar shares the image already pulled for the main container and avoids version skew. It uses the `@1` prefix to route Runtime API commands to the current HAProxy worker process via the master socket. If the SPIFFE certificate isn't loaded in HAProxy (for example, no Ingress uses the annotation), it logs a skip message and waits for the next change.

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

The HAPTIC controller validates HAProxy configuration by running `haproxy -c` locally before deploying it. Since the SPIRE certificates only exist on the HAProxy pods (managed by spiffe-helper), the controller pod needs placeholder files at the same absolute paths so that validation passes.

Mount a ConfigMap with dummy PEM files on the **controller** pod:

```yaml
# Dummy certs for controller-side "haproxy -c" validation
# (not real secrets — see ConfigMap below)
controller:
  extraVolumes:
    - name: spiffe-validation-certs
      configMap:
        name: '{{ include "haptic.fullname" . }}-spiffe-validation-certs'

  extraVolumeMounts:
    - name: spiffe-validation-certs
      mountPath: /etc/haproxy/spiffe
      readOnly: true
```

Generate the dummy certificate and add it as a ConfigMap via `extraDeploy`:

```bash
# Generate a self-signed dummy cert (valid 100 years, never used for real TLS)
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 \
  -keyout /dev/stdout -out /dev/stdout -days 36500 -nodes \
  -subj '/CN=validation-placeholder-NOT-A-REAL-SECRET' 2>/dev/null
```

```yaml
extraDeploy:
  # ================================================================
  # VALIDATION PLACEHOLDERS — NOT REAL SECRETS
  # ================================================================
  # These dummy PEM files are mounted ONLY on the controller pod so
  # that "haproxy -c" config validation passes. They are never
  # deployed to the HAProxy pods. On the HAProxy pods, spiffe-helper
  # independently manages the real SPIRE-issued certs.
  # ================================================================
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: '{{ include "haptic.fullname" . }}-spiffe-validation-certs'
      labels:
        app.kubernetes.io/name: haptic
        app.kubernetes.io/instance: '{{ .Release.Name }}'
        app.kubernetes.io/component: validation
    data:
      # DUMMY CERT — validation placeholder, not a real secret
      svid.pem: |
        <paste generated certificate PEM here>
      # DUMMY KEY — validation placeholder, not a real secret
      svid.pem.key: |
        <paste generated private key PEM here>
      # DUMMY CA — validation placeholder, not a real secret
      bundle.pem: |
        <paste generated certificate PEM here (same as svid.pem)>
```

!!! note
    The `extraVolumes` and `extraVolumeMounts` under `controller:` (as in the snippet above) apply to the **controller** pod. The HAProxy pod's volumes are configured under `haproxy.extraVolumes`.

## Verification

After deploying, verify the integration is working:

```bash
# Check spiffe-helper received certificates
kubectl -n haptic logs <haproxy-pod> -c spiffe-helper

# Expected output:
# level=info msg="Received update" spiffe_id="spiffe://..." system=spiffe-helper
# level=info msg="X.509 certificates updated" system=spiffe-helper
```

```bash
# Verify certificate files exist on the HAProxy pod
kubectl -n haptic exec <haproxy-pod> -c haproxy -- ls -la /etc/haproxy/spiffe/

# Expected: svid.pem, svid.pem.key, bundle.pem owned by UID 99
```

```bash
# Inspect the SPIFFE ID in the issued certificate
kubectl -n haptic exec <haproxy-pod> -c haproxy -- \
  openssl x509 -in /etc/haproxy/spiffe/svid.pem -noout -text \
  | grep -A1 "Subject Alternative Name"

# Expected: URI:spiffe://<trust-domain>/ns/<namespace>/sa/<service-account>
```

```bash
# Verify the backend mTLS annotation is reflected in HAProxy config
kubectl -n haptic exec <haproxy-pod> -c haproxy -- \
  cat /etc/haproxy/haproxy.cfg | grep -A2 'default-server.*ssl.*verify'
```

```bash
# Check cert-reloader is running and updating certificates
kubectl -n haptic logs <haproxy-pod> -c cert-reloader

# Expected output after a rotation:
# cert-reloader: polling for cert changes
# Transaction created for certificate /etc/haproxy/spiffe/svid.pem!
# Committing /etc/haproxy/spiffe/svid.pem..........
# Success!
# cert-reloader: certificates updated via runtime API at <timestamp>
```

## Troubleshooting

<a id="spiffe-helper-cannot-connect-to-spire-agent"></a>

### `spiffe-helper` can't connect to SPIRE agent

```
Error while watching x509 context: ... dial unix /spiffe-workload-api/agent.sock: no such file or directory
```

The SPIRE CSI driver creates the socket as `spire-agent.sock`, not `agent.sock`. Verify the correct socket name:

```bash
kubectl -n haptic exec <haproxy-pod> -c spiffe-helper -- ls /spiffe-workload-api/
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
