# HAProxy versions

## Overview

HAPTIC supports multiple HAProxy major.minor series simultaneously. The `haproxyVersion` value selects the series and controls two things:

- The **controller image** tag suffix (for example `-haproxy3.2`) — must match a version built by CI
- The **HAProxy pod image** tag — defaults to the latest tested patch for that series

The controller image uses major.minor only (`-haproxy3.2`, not `-haproxy3.2.x`) because CI builds one image per supported series. Patch versions within a series are API-compatible with the controller.

## Supported versions

| Series | Status | Community image | Enterprise image |
|--------|--------|-----------------|------------------|
| 3.0 | Supported (LTS) | `haproxytech/haproxy-debian:3.0.x` | `...:3.0r1` |
| 3.1 | Supported (non-LTS) | `haproxytech/haproxy-debian:3.1.x` | `...:3.1r1` |
| 3.2 | Supported (LTS) | `haproxytech/haproxy-debian:3.2.x` | `...:3.2r1` |
| 3.3 | Supported (non-LTS) | `haproxytech/haproxy-debian:3.3.x` | — |
| 3.4 | Supported (LTS, default) | `haproxytech/haproxy-debian:3.4.x` | — |

HAProxy's even-numbered series (3.0, 3.2, 3.4) are LTS with about five years of support; odd-numbered series (3.1, 3.3) get a shorter maintenance window. The chart's default is always the latest LTS — currently 3.4.

!!! note "HAProxy version vs DataPlane API version"
    The series above is the **HAProxy binary** version. HAPTIC talks to each pod's
    DataPlane API, which has its own release cadence: the HAProxy 3.3 and 3.4 images
    both ship **DataPlane API v3.3**. HAPTIC bundles clients and validators for
    DataPlane API v3.0–v3.3 and picks one from the version the pod reports at
    `/v3/info`, so a 3.4 pod is served by the v3.3 client. Config syntax is still
    validated against the matching HAProxy binary via `haproxy -c`, which is why a
    per-series controller image exists.

## Feature version requirements

Most chart features work on every supported series. A few require a minimum HAProxy series:

| Feature | Minimum series | Behavior below the minimum |
|---------|----------------|----------------------------|
| SSL/TLS termination, [CRT-list management](../libraries/ssl.md#crt-list-certificate-management), [OCSP stapling](../libraries/ssl.md#ocsp-stapling) | 3.0 | Not applicable — 3.0 is the minimum supported series |
| [SPOA hub](spoa-hub.md) native transport (`mode spop`) | 3.1 | Auto-falls back to `mode tcp`; the hub still works |
| Reload-free TLS certificate rotation (`set ssl cert`) | 3.2 | Certificate rotation still works, but each renewal triggers a full reload instead of a runtime update |
| [Shared-memory stats persistence](../libraries/base.md#shared-memory-stats-haproxy-33) (`shm-stats-file`) | 3.3 | Silently omitted; stats counters reset on every reload |

Only `mode spop` (3.1) and shm-stats (3.3) gate chart behavior you can't otherwise get. 3.2 unlocks no chart feature you couldn't use below it — it only enables the reload-free lane for certificate rotation; on 3.0 and 3.1 the same rotation triggers a reload. That lane depends on DataPlane API v3.2+, which ships with the 3.2+ images (see the note above).

## Selecting a version

Set `haproxyVersion` to your desired series. The chart defaults to `3.4`:

```bash
# Use HAProxy 3.0 LTS
helm install haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --namespace haptic --create-namespace \
  --set haproxyVersion=3.0

# Use HAProxy 3.3
helm install haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --namespace haptic --create-namespace \
  --set haproxyVersion=3.3
```

Or in your values file:

```yaml
haproxyVersion: "3.0"
```

## Patch version pinning

By default, the HAProxy pod image is pinned to the latest patch version tested with the chart, looked up from the `haproxyPatchVersions` map in [`charts/haptic/values.yaml`](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/charts/haptic/values.yaml). For example, with `haproxyVersion: "3.2"`, the pod uses whichever 3.2.x patch the chart currently pins (for example `haproxytech/haproxy-debian:3.2.16`). The pin moves forward over time as Renovate updates the chart.

To pin a specific patch version yourself, set `haproxy.image.tag`:

```yaml
haproxyVersion: "3.2"
haproxy:
  image:
    tag: "3.2.10"  # Pin to a specific patch
```

## Keeping patches up to date

The chart ships with a `haproxyPatchVersions` map whose entries are kept current by the project's own Renovate setup. Each chart release picks up the latest patch within every series and ships it as the new default. If you don't override `haproxy.image.tag`, you inherit those patches automatically when you bump the chart.

If you pin `haproxy.image.tag` yourself in a GitOps repository and want Renovate to track patches **within the pinned series**, add a regex custom manager to your `renovate.json`. The trick is to extract the major.minor from the current value and bake it back into the versioning regex so Renovate stays inside the series:

```json
{
  "customManagers": [
    {
      "customType": "regex",
      "fileMatch": ["values\\.ya?ml$"],
      "matchStrings": [
        "# renovate:\\s*datasource=docker\\s+depName=haproxytech/haproxy-debian[^\\n]*\\n\\s*tag:\\s*\"(?<currentValue>(?<seriesMajor>\\d+)\\.(?<seriesMinor>\\d+)\\.\\d+)\""
      ],
      "datasourceTemplate": "docker",
      "depNameTemplate": "haproxy-debian {{{seriesMajor}}}.{{{seriesMinor}}}.x",
      "packageNameTemplate": "haproxytech/haproxy-debian",
      "versioningTemplate": "regex:^(?<major>{{{seriesMajor}}})\\.(?<minor>{{{seriesMinor}}})\\.(?<patch>\\d+)$"
    }
  ]
}
```

And annotate the override in your values file so the manager can find it:

```yaml
haproxyVersion: "3.2"
haproxy:
  image:
    # renovate: datasource=docker depName=haproxytech/haproxy-debian
    tag: "3.2.10"
```

The chart's own `renovate.json` ([source](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/renovate.json)) uses the same pattern against `haproxyPatchVersions` — it's the working reference if you need a more elaborate setup (for example handling multiple series in one file).

## HAProxy Enterprise

Enterprise deployments require:

1. Setting `haproxy.enterprise.enabled: true`
2. Configuring `haproxy.podSpec.imagePullSecrets` with your registry credentials
3. Building your own controller image and pointing `controller.image.repository` (and optionally `controller.image.tag`) at it — the HAPTIC project doesn't distribute enterprise controller images (see the note below)

```yaml
haproxyVersion: "3.2"
haproxy:
  enterprise:
    enabled: true
  podSpec:
    imagePullSecrets:
      - name: hapee-registry-secret
```

With `enterprise.enabled: true`, an empty `haproxy.image.repository` selects
`hapee-registry.haproxy.com/haproxy-enterprise`, and the tag defaults to the
tested revision from `haproxyEnterprisePatchVersions` (for example `3.2r1`).
The same `haproxyVersion` also derives the Enterprise binary path, so image and
binary series can't drift. The chart fails if the selected series has no tested
Enterprise revision. To use a custom registry or pin a specific revision:

```yaml
haproxy:
  image:
    repository: registry.example.com/haproxy-enterprise
    tag: "3.2r2"
```

Check [HAProxy Enterprise release notes](https://www.haproxy.com/documentation/haproxy-enterprise/release-notes/) for available revisions.

!!! note "Building the controller"
    Enterprise controller images aren't distributed by the HAPTIC project. You must build the controller yourself and push it to your own registry, then set `controller.image.repository` and optionally `controller.image.tag` accordingly.

## Upgrading to a new series

To move from one major.minor to another (for example 3.2 → 3.3):

1. Verify the new series is supported in the chart version you are using
2. Update `haproxyVersion` in your values
3. Clear any `haproxy.image.tag` override, or update it to a patch in the new series
4. Run `helm upgrade`:

    ```bash
    helm upgrade haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
      --namespace haptic \
      --reuse-values --set haproxyVersion=3.3
    ```

The controller and HAProxy pods restart with the new images.

HAProxy rolls without dropping the fleet. The chart runs `haproxy.replicaCount` pods (2 by default) behind a `RollingUpdate` strategy set to `maxUnavailable: 0` and `maxSurge: 1` (`haproxy.updateStrategy`). Kubernetes starts a new-version pod and waits for it to pass its readiness probe before terminating an old one, so the number of serving pods never drops below the replica count during the roll. Keep `haproxy.replicaCount` at 2 or more for a no-downtime series bump — a single replica has nowhere to shift traffic while it restarts.
