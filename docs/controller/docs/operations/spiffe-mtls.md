# Backend mTLS with SPIFFE/SPIRE

This guide explains how to configure HAPTIC to use [SPIFFE/SPIRE](https://spiffe.io/) for automatic mutual TLS (mTLS) between HAProxy and backend services using short-lived X.509 certificates.

## Overview

[SPIFFE](https://spiffe.io/docs/latest/spiffe-about/overview/) (Secure Production Identity Framework for Everyone) is a set of standards for securely identifying workloads in dynamic environments. [SPIRE](https://spiffe.io/docs/latest/spire-about/spire-concepts/) is the reference implementation that issues and manages SPIFFE Verifiable Identity Documents (SVIDs) — short-lived X.509 certificates that serve as workload identity.

When integrated with HAPTIC, SPIRE enables zero-trust mTLS to backends without managing certificates manually:

- **Automatic identity** — SPIRE attests HAProxy pods and issues X.509-SVIDs based on Kubernetes service account identity
- **Short-lived certificates** — SVIDs are automatically rotated at half of their TTL (e.g. every 12 hours with a 24h TTL), reducing the impact of credential compromise
- **No secrets in cluster** — Private keys are generated in-memory by the SPIRE agent and never stored as Kubernetes Secrets
- **Seamless reload** — Certificate rotation triggers a graceful HAProxy reload via SIGUSR2 with no connection drops

## Prerequisites

Before following this guide, ensure:

- **SPIRE server and agents** are deployed in your cluster
- **SPIRE CSI driver** (`csi.spiffe.io`) is installed for exposing the Workload API socket to pods
- **Workload registration** exists for the HAProxy pod's service account and namespace
- **HAPTIC Helm chart** version with `shareProcessNamespace` and `podAnnotations` support

## Architecture

The integration uses four components working together inside the HAProxy pod:

```
┌─────────────────────────────────────────────────────────┐
│ HAProxy Pod (shareProcessNamespace: true)               │
│                                                         │
│  ┌──────────┐   ┌───────────────┐   ┌────────────────┐  │
│  │ init:    │   │  haproxy      │   │ spiffe-helper  │  │
│  │ create-  │   │               │   │                │  │
│  │ spiffe-  │──▶│ Reads certs   │◀──│ Fetches SVIDs  │  │
│  │ dir      │   │ from shared   │   │ from SPIRE     │  │
│  │          │   │ volume        │   │ agent via CSI  │  │
│  └──────────┘   │               │   │                │  │
│                 │ /etc/haproxy/ │   │ Writes certs   │  │
│                 │   spiffe/     │   │ to shared vol  │  │
│                 │   ├ svid.pem  │   │                │  │
│                 │   ├ svid-key  │   │ Sends SIGUSR2  │──┤
│                 │   └ bundle    │   │ on renewal     │  │
│                 └───────────────┘   └────────────────┘  │
│                                                         │
│  CSI Volume: /spiffe-workload-api/spire-agent.sock      │
└─────────────────────────────────────────────────────────┘
```

**How it works:**

1. An **init container** creates the `/etc/haproxy/spiffe/` directory on the shared `haproxy-runtime` emptyDir volume
2. The **spiffe-helper** sidecar connects to the SPIRE agent via the CSI-mounted Workload API socket
3. SPIRE attests the pod's identity and issues an X.509-SVID
4. spiffe-helper writes the certificate, private key, and trust bundle to the shared volume
5. On certificate renewal, spiffe-helper sends **SIGUSR2** to HAProxy (requires `shareProcessNamespace: true`) which triggers a graceful reload
6. HAProxy uses these certificates for mTLS connections to backend services

## Configuration

### HAProxy Pod Setup

Add the following to your Helm values to configure the HAProxy pod with spiffe-helper:

```yaml
haproxy:
  # Required: allows spiffe-helper to send SIGUSR2 to HAProxy for cert rotation
  shareProcessNamespace: true

  # Restart pods when spiffe-helper or other sidecar configs change
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
        # Must match HAProxy UID (99) for SIGUSR2 signal delivery
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

!!! warning
    The spiffe-helper container must run as **UID 99** (the same as HAProxy). Without `shareProcessNamespace` and matching UIDs, spiffe-helper cannot send SIGUSR2 to HAProxy for certificate rotation reloads. No `CAP_KILL` capability is needed when both processes share the same UID.

!!! note
    The spiffe-helper container image tags do **not** use a `v` prefix — use `0.11.0`, not `v0.11.0`.

### PID File for Signal Delivery

spiffe-helper needs HAProxy's PID file to send SIGUSR2. Add a `templateSnippet` that writes the PID file in the HAProxy global section:

```yaml
controller:
  config:
    templateSnippets:
      global-settings-050-pidfile:
        template: |
          pidfile /etc/haproxy/haproxy.pid
```

### spiffe-helper Configuration

Create a ConfigMap with the spiffe-helper configuration using `extraDeploy`. The configuration format is [HCL](https://github.com/hashicorp/hcl) (not TOML or INI):

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
        svid_key_file_name = "svid-key.pem"
        svid_bundle_file_name = "bundle.pem"
        daemon_mode = true
        pid_file_name = "/etc/haproxy/haproxy.pid"
        renew_signal = "SIGUSR2"

        health_checks {
          listener_enabled = true
          bind_port = "8081"
          liveness_path = "/live"
          readiness_path = "/ready"
        }
```

!!! warning
    The `health_checks` block uses **HCL block syntax** (`health_checks { ... }`), not TOML section syntax (`[health_checks]`). Using the wrong format causes a parse error.

### Backend mTLS via Custom Annotation

To enable per-Ingress backend mTLS using the SPIRE certificates, add a custom `templateSnippet` that processes an annotation (e.g., `example.com/server-mtls-spire`):

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
              {%- serverOpts["flags"] = append(serverOpts["flags"].([]any),
                  "ssl verify required " +
                  "ca-file /etc/haproxy/spiffe/bundle.pem " +
                  "crt /etc/haproxy/spiffe/svid.pem " +
                  "key /etc/haproxy/spiffe/svid-key.pem") %}
            {%- end %}
          {%- end %}
```

This snippet:

- Runs at **priority 800** (before `backend-directives-900-haproxytech-advanced`), so conflicts are detected before the built-in annotations are processed
- Uses **absolute paths** for the certificate files because HAProxy's `crt-base` directive points to the `ssl/` directory, and the SPIRE certs are in `/etc/haproxy/spiffe/`
- **Fails the render** if the annotation is used together with `haproxy.org/server-ssl`, `haproxy.org/server-crt`, or `haproxy.org/server-ca`, since these configure conflicting SSL modes

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
    default-server check ssl verify required ca-file /etc/haproxy/spiffe/bundle.pem crt /etc/haproxy/spiffe/svid.pem key /etc/haproxy/spiffe/svid-key.pem
```

### Tuning SVID Rotation Frequency

SPIRE rotates SVIDs at 50% of their TTL (the "half-life"). The default server TTL is 1h, meaning HAProxy reloads via SIGUSR2 roughly every 30 minutes. To reduce reload frequency, create a dedicated [`ClusterSPIFFEID`](https://github.com/spiffe/spire-controller-manager/blob/main/docs/clusterspiffeid-crd.md) with an extended `ttl` for the HAProxy pods:

```yaml
apiVersion: spire.spiffe.io/v1alpha1
kind: ClusterSPIFFEID
metadata:
  name: <release-name>-haproxy
spec:
  spiffeIDTemplate: "spiffe://{{ .TrustDomain }}/ns/{{ .PodMeta.Namespace }}/sa/{{ .PodSpec.ServiceAccountName }}"
  podSelector:
    matchLabels:
      app.kubernetes.io/name: haptic
      app.kubernetes.io/component: loadbalancer
  ttl: "24h"
```

With a 24h TTL, SVID rotation (and thus HAProxy reload) happens roughly every 12 hours. The `spiffeIDTemplate` should match the one used by your SPIRE deployment's default ClusterSPIFFEID. Adjust the `podSelector` labels to match your HAProxy pods.

!!! note
    This ClusterSPIFFEID can coexist with the default one created by the SPIRE Helm chart. When both match the same pods, the SPIRE controller manager masks the duplicate entry. Verify the TTL is applied by checking the certificate validity period on the HAProxy pod (see [Verification](#verification)).

## Controller Validation

The HAPTIC controller validates HAProxy configuration by running `haproxy -c` locally before deploying it. Since the SPIRE certificates only exist on the HAProxy pods (managed by spiffe-helper), the controller pod needs placeholder files at the same absolute paths so that validation passes.

Mount a ConfigMap with dummy PEM files on the **controller** pod:

```yaml
# Dummy certs for controller-side "haproxy -c" validation
# (not real secrets — see ConfigMap below)
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
      svid-key.pem: |
        <paste generated private key PEM here>
      # DUMMY CA — validation placeholder, not a real secret
      bundle.pem: |
        <paste generated certificate PEM here (same as svid.pem)>
```

!!! note
    The `extraVolumes` and `extraVolumeMounts` at the top level (not under `haproxy:`) apply to the **controller** pod. The HAProxy pod's volumes are configured under `haproxy.extraVolumes`.

## Verification

After deploying, verify the integration is working:

```bash
# Check spiffe-helper received certificates
kubectl -n <namespace> logs <haproxy-pod> -c spiffe-helper

# Expected output:
# level=info msg="Received update" spiffe_id="spiffe://..." system=spiffe-helper
# level=info msg="X.509 certificates updated" system=spiffe-helper
```

```bash
# Verify certificate files exist on the HAProxy pod
kubectl -n <namespace> exec <haproxy-pod> -c haproxy -- ls -la /etc/haproxy/spiffe/

# Expected: svid.pem, svid-key.pem, bundle.pem owned by UID 99
```

```bash
# Inspect the SPIFFE ID in the issued certificate
kubectl -n <namespace> exec <haproxy-pod> -c haproxy -- \
  openssl x509 -in /etc/haproxy/spiffe/svid.pem -noout -text \
  | grep -A1 "Subject Alternative Name"

# Expected: URI:spiffe://<trust-domain>/ns/<namespace>/sa/<service-account>
```

```bash
# Verify the backend mTLS annotation is reflected in HAProxy config
kubectl -n <namespace> exec <haproxy-pod> -c haproxy -- \
  cat /etc/haproxy/haproxy.cfg | grep -A2 'default-server.*ssl.*verify'
```

## Troubleshooting

### spiffe-helper cannot connect to SPIRE agent

```
Error while watching x509 context: ... dial unix /spiffe-workload-api/agent.sock: no such file or directory
```

The SPIRE CSI driver creates the socket as `spire-agent.sock`, not `agent.sock`. Verify the correct socket name:

```bash
kubectl -n <namespace> exec <haproxy-pod> -c spiffe-helper -- ls /spiffe-workload-api/
```

Update `agent_address` in your spiffe-helper config to match.

### spiffe-helper config parse error

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

### Certificate directory does not exist

```
Unable to dump bundle ... open /etc/haproxy/spiffe/svid.pem: no such file or directory
```

The `haproxy-runtime` emptyDir does not include the `spiffe/` subdirectory by default. Ensure the init container is configured to create it before spiffe-helper starts. If the init container fails due to ResourceQuota, add `resources.requests` and `resources.limits`.

### ImagePullBackOff for spiffe-helper

```
Back-off pulling image "ghcr.io/spiffe/spiffe-helper:v0.11.0"
```

The spiffe-helper container image uses tags **without** the `v` prefix. Use `0.11.0`, not `v0.11.0`.

### Controller rejects config with cert path errors

If the controller logs show validation failures referencing `/etc/haproxy/spiffe/*.pem`, the validation placeholder ConfigMap is not mounted on the controller pod. Verify:

```bash
kubectl -n <namespace> exec <controller-pod> -- ls /etc/haproxy/spiffe/
# Should list: bundle.pem  svid-key.pem  svid.pem
```

## See Also

- [Security Guide](./security.md) — TLS configuration and credential management
- [Helm Chart Reference](https://haproxy-haptic.org/helm-chart/latest/) — `haproxy.shareProcessNamespace`, `haproxy.sidecars`, `haproxy.initContainers`, `extraDeploy`
- [SPIFFE/SPIRE Documentation](https://spiffe.io/docs/latest/) — SPIFFE concepts, SPIRE deployment, workload registration
- [spiffe-helper on GitHub](https://github.com/spiffe/spiffe-helper) — Configuration reference and release notes
- [Templating Guide](../templating.md) — Writing custom `templateSnippets`
