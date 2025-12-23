# Releasing

This document describes the versioning strategy and release process for the HAProxy Template Ingress Controller.

## Overview

The project uses **decoupled versioning** - the controller and Helm chart have independent version numbers. This allows chart-only changes (documentation, template fixes, new values) without requiring a new controller release.

## Version Scheme

### Controller Versions

Controller versions follow [Semantic Versioning](https://semver.org/):

```
vX.Y.Z          # Stable release
vX.Y.Z-alpha.N  # Early development, unstable
vX.Y.Z-beta.N   # Feature complete, needs testing
vX.Y.Z-rc.N     # Release candidate, final testing
```

Git tags use the prefix `controller-`:

```
controller-v0.1.0
controller-v0.2.0-beta.1
controller-v1.0.0
```

### Chart Versions

Chart versions also follow Semantic Versioning:

```
X.Y.Z           # Stable release
X.Y.Z-beta.N    # Pre-release
```

Git tags use the prefix `chart-`:

```
chart-v0.1.0
chart-v0.2.0
```

### Chart.yaml

The `Chart.yaml` file contains two version fields:

```yaml
version: 0.2.0        # Chart version (incremented for ANY chart change)
appVersion: "0.1.0"   # Controller version this chart deploys
```

- `version`: Incremented for any chart change (templates, values, docs)
- `appVersion`: Set to the controller version the chart is designed for

## Release Artifacts

### Controller Release

When a `controller-v*` tag is pushed, CI automatically:

1. **Builds Go binaries** for multiple architectures:
   - `linux/amd64` - Intel/AMD servers
   - `linux/arm64` - AWS Graviton, Apple Silicon, modern ARM
   - `linux/arm/v7` - Raspberry Pi, older ARM devices

2. **Creates GitLab release** with:
   - Raw binaries (no tarballs)
   - SHA256 checksums
   - Cosign signatures for supply chain security

3. **Builds Docker images** for each supported HAProxy version:
   - `v0.1.0-haproxy3.0`
   - `v0.1.0-haproxy3.1`
   - `v0.1.0-haproxy3.2`
   - Each image is a multi-arch manifest

4. **Creates convenience tags** (stable releases only):
   - `v0.1.0` → `v0.1.0-haproxy3.2` (latest HAProxy)
   - `latest` → latest stable + latest HAProxy
   - `latest-haproxy3.1` → latest stable + specific HAProxy

### Chart Release

When a `chart-v*` tag is pushed, CI automatically:

1. **Packages the Helm chart**
2. **Pushes to OCI registry**
3. **Signs with Cosign**

## Release Process

### Prerequisites

1. Ensure all tests pass: `make check-all`
2. Ensure working directory is clean: `git status`

### Releasing a Controller Version

1. **Update CHANGELOG.md**:
   - Rename `[Unreleased]` to `[X.Y.Z] - YYYY-MM-DD`
   - Add new empty `[Unreleased]` section
   - Commit the change

2. **Run the release script**:

   ```bash
   ./scripts/release-controller.sh 0.1.0-beta.1
   ```

   The script:
   - Validates version format
   - Checks CHANGELOG.md has an entry
   - Updates VERSION file
   - Updates Chart.yaml appVersion
   - Creates commit and tag

3. **Push to trigger CI**:

   ```bash
   git push origin main controller-v0.1.0-beta.1
   ```

### Releasing a Chart Version

1. **Run the release script**:

   ```bash
   ./scripts/release-chart.sh 0.2.0
   ```

   The script:
   - Validates version format
   - Updates Chart.yaml version
   - Creates commit and tag

2. **Push to trigger CI**:

   ```bash
   git push origin main chart-v0.2.0
   ```

## Docker Tag Strategy

| Tag Pattern | Example | Description |
|-------------|---------|-------------|
| `{version}` | `v0.1.0` | Default tag = latest HAProxy |
| `{version}-haproxy{ver}` | `v0.1.0-haproxy3.1` | Specific HAProxy version |
| `latest` | `latest` | Latest stable + latest HAProxy |
| `latest-haproxy{ver}` | `latest-haproxy3.1` | Latest stable + specific HAProxy |

Default tags point to the newest supported HAProxy version (currently 3.2):

- `v0.1.0` = `v0.1.0-haproxy3.2`
- `latest` = `latest-haproxy3.2`

Pre-release tags (alpha, beta, rc) do not update `latest` tags.

## Helm Chart Installation

Install from OCI registry:

```bash
helm install haproxy-ic \
  oci://registry.gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/charts/haptic \
  --version 0.1.0
```

## Verifying Releases

### Verify Binary Signatures

```bash
# Download checksums and signatures
curl -LO https://gitlab.com/.../checksums.txt
curl -LO https://gitlab.com/.../checksums.txt.sig
curl -LO https://gitlab.com/.../checksums.txt.pem

# Verify signature
cosign verify-blob \
  --signature checksums.txt.sig \
  --certificate checksums.txt.pem \
  checksums.txt

# Verify binary checksums
sha256sum -c checksums.txt
```

### Verify Docker Images

```bash
cosign verify registry.gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller:v0.1.0
```

### Verify Helm Chart

```bash
cosign verify \
  oci://registry.gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/charts/haptic:0.1.0
```

## Version Compatibility Matrix

| Chart Version | Controller Version | Kubernetes | HAProxy |
|---------------|-------------------|------------|---------|
| 0.1.x | 0.1.x | 1.31-1.34 | 3.0-3.2 |

## Supported HAProxy Versions

| HAProxy Version | Base Image | Status |
|-----------------|-----------|--------|
| 3.0 | `haproxytech/haproxy-debian:3.0` | Supported |
| 3.1 | `haproxytech/haproxy-debian:3.1` | Supported |
| 3.2 | `haproxytech/haproxy-debian:3.2` | Supported (current) |

All images include multi-architecture support: `linux/amd64`, `linux/arm64`, `linux/arm/v7`.

Only Community Edition (CE) images are released. Enterprise Edition is not supported for legal reasons.
