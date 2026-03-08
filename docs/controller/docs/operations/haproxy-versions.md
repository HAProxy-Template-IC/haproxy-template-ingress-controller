# HAProxy Versions

## Overview

HAPTIC supports multiple HAProxy major.minor series simultaneously. The `haproxyVersion` value selects the series and controls two things:

- The **controller image** tag suffix (e.g. `-haproxy3.2`) — must match a version built by CI
- The **HAProxy pod image** tag — defaults to the latest tested patch for that series

The controller image uses major.minor only (`-haproxy3.2`, not `-haproxy3.2.13`) because CI builds one image per supported series. Patch versions within a series are API-compatible with the controller.

## Supported Versions

| Series | Status | Community image | Enterprise image |
|--------|--------|-----------------|------------------|
| 3.0 | Supported (LTS) | `haproxytech/haproxy-debian:3.0.x` | `...:3.0r1` |
| 3.1 | Supported | `haproxytech/haproxy-debian:3.1.x` | `...:3.1r1` |
| 3.2 | Supported (default) | `haproxytech/haproxy-debian:3.2.x` | `...:3.2r1` |
| 3.3 | Supported | `haproxytech/haproxy-debian:3.3.x` | — |

## Selecting a Version

Set `haproxyVersion` to your desired series. The chart defaults to `3.2`:

```bash
# Use HAProxy 3.0 LTS
helm install haptic ... --set haproxyVersion=3.0

# Use HAProxy 3.3
helm install haptic ... --set haproxyVersion=3.3
```

Or in your values file:

```yaml
haproxyVersion: "3.0"
```

## Patch Version Pinning

By default, the HAProxy pod image is pinned to the latest patch version tested with the chart (stored in `haproxyPatchVersions`). For example, with `haproxyVersion: "3.2"`, the pod uses `haproxytech/haproxy-debian:3.2.13`.

To pin a specific patch version, set `haproxy.image.tag`:

```yaml
haproxyVersion: "3.2"
haproxy:
  image:
    tag: "3.2.10"  # Pin to a specific patch
```

## Keeping Patches Up to Date

The chart ships with `haproxyPatchVersions` defaults that are updated with each chart release. If you manage your values in a GitOps repository, you can automate patch updates with Renovate.

Add a `renovate.json` (or update your existing one):

```json
{
  "packageRules": [
    {
      "matchDatasources": ["docker"],
      "matchPackageNames": ["haproxytech/haproxy-debian"],
      "matchFileNames": ["**/values.yaml"],
      "versioning": "semver"
    }
  ]
}
```

Renovate will detect the `# renovate:` annotation on `haproxy.image.tag` in your values file and propose patch updates within the selected series.

## HAProxy Enterprise

Enterprise deployments require:

1. Setting `haproxy.enterprise.enabled: true`
2. Pointing `haproxy.image.repository` to the enterprise registry
3. Configuring `imagePullSecrets` with your registry credentials

```yaml
haproxyVersion: "3.2"
haproxy:
  image:
    repository: hapee-registry.haproxy.com/haproxy-enterprise
  enterprise:
    enabled: true
    version: "3.2"
imagePullSecrets:
  - name: hapee-registry-secret
```

With `enterprise.enabled: true`, the pod image tag defaults to the enterprise revision from `haproxyEnterprisePatchVersions` (e.g. `3.2r1`). To pin a specific revision:

```yaml
haproxy:
  image:
    tag: "3.2r2"
```

Check [HAProxy Enterprise release notes](https://www.haproxy.com/documentation/haproxy-enterprise/release-notes/) for available revisions.

!!! note "Building the controller"
    Enterprise controller images are not distributed by the HAPTIC project. You must build the controller yourself and push it to your own registry, then set `image.repository` and optionally `image.tag` accordingly.

## Upgrading to a New Series

To move from one major.minor to another (e.g. 3.2 → 3.3):

1. Verify the new series is supported in the chart version you are using
2. Update `haproxyVersion` in your values
3. Clear any `haproxy.image.tag` override, or update it to a patch in the new series
4. Run `helm upgrade`

The controller and HAProxy pods will restart with the new images.
