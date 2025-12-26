# Releasing

## Overview

The HAProxy Template Ingress Controller uses a dual-release model where the controller and Helm chart have independent version numbers. This allows chart-only releases (e.g., documentation fixes) without requiring a new controller version.

Both use [Semantic Versioning](https://semver.org/) with support for pre-release suffixes.

## Version Numbering

**Format:** `MAJOR.MINOR.PATCH[-PRERELEASE]`

| Type | Example | Description |
|------|---------|-------------|
| Stable release | `0.1.0`, `1.0.0` | Production-ready version |
| Alpha | `0.1.0-alpha.1` | Early testing, APIs may change |
| Beta | `0.1.0-beta.1` | Feature complete, needs testing |
| Release candidate | `0.1.0-rc.1` | Final testing before release |

## CHANGELOG Conventions

A single `CHANGELOG.md` tracks changes for both controller and chart. Use prefixes to indicate scope:

| Prefix | When to Use |
|--------|-------------|
| `[Controller]` | Changes to controller code only |
| `[Chart]` | Changes to Helm chart only (values, templates) |
| *(no prefix)* | Changes affecting both controller and chart |

**Example:**

```markdown
## [0.1.0] - 2025-01-15

### Added
- [Chart] Default SSL certificate support via Helm values
- [Controller] Leader election for high availability
- New CRD field for custom annotations  <!-- affects both -->

### Changed
- [Chart] Default replica count changed from 1 to 2
```

## Prerequisites

Before releasing:

1. **Clean working directory** - All changes committed
2. **CHANGELOG.md updated** - Release notes documented
3. **All tests passing** - CI pipeline green on main branch
4. **Documentation updated** - Any new features documented

## Controller Release Process

The main branch is protected, so releases are made via merge requests. CI automatically creates tags when the VERSION file changes on main.

### Step 1: Update CHANGELOG.md

Prepare the changelog for release:

```markdown
# Change from:
## [Unreleased]
### Added
- New feature X

# To:
## [0.1.0-alpha.1] - 2025-01-15
### Added
- New feature X

## [Unreleased]
```

### Step 2: Run the Release Script

```bash
./scripts/release-controller.sh <version>
```

The script will:

- Validate version format (SemVer)
- Check CHANGELOG.md has the version entry
- Update the `VERSION` file
- Update `Chart.yaml` appVersion and image annotation
- Create a release commit

### Step 3: Create and Merge Release MR

```bash
# Create release branch
git checkout -b release/controller-v<version>

# Push branch
git push -u origin release/controller-v<version>

# Create merge request
glab mr create --title "release: haptic-controller v<version>" \
  --description "Release haptic-controller v<version>" \
  --target-branch main
```

Review and merge the MR through GitLab.

### Automatic Tag Creation

After the MR is merged, CI automatically:

1. Detects the VERSION file change on main
2. Creates and pushes the `v<version>` tag
3. Triggers the release pipeline (binaries, images, GitLab release)

No manual tagging is required.

??? note "Manual Tagging (Fallback)"
    If automatic tagging fails, you can create the tag manually:

    ```bash
    git checkout main
    git pull origin main
    git tag -a v<version> -m "Controller release <version>"
    git push origin v<version>
    ```

### What CI Does Automatically

When a `v*` tag is pushed, CI will:

1. **Build binaries** for linux/amd64, linux/arm64, linux/arm/v7
2. **Create GitLab release** with:
   - Signed binaries
   - SHA256 checksums
   - Release notes from CHANGELOG.md
   - Pre-release flag (for alpha/beta/rc versions)
3. **Build Docker images** for HAProxy 3.0, 3.1, 3.2
4. **Sign all artifacts** with Cosign (keyless OIDC)
5. **Generate SBOM** (Software Bill of Materials) for each image
6. **Attach SBOM attestation** to images via Cosign
7. **Trigger documentation build** with version tag

## Chart Release Process

!!! note "When to Release Chart Separately"
    Only release the chart separately when:

    - Chart-only changes (values, templates, docs)
    - Breaking Helm value changes
    - Chart bug fixes independent of controller

    Controller releases automatically update the chart's `appVersion`.

### Step 1: Update CHANGELOG.md

Add a `## [<version>]` section with chart changes prefixed by `[Chart]`:

```markdown
## [0.2.0] - 2025-01-20
### Changed
- [Chart] Updated default resource limits
```

### Step 2: Run the Release Script

```bash
./scripts/release-chart.sh <version>
```

The script will:

- Validate version format (SemVer)
- Check CHANGELOG.md has the version entry
- Update `Chart.yaml` version
- Create a release commit

### Step 3: Create and Merge Release MR

```bash
git checkout -b release/haptic-chart-v<version>
git push -u origin release/haptic-chart-v<version>
glab mr create --title "release: chart v<version>" \
  --description "Release chart v<version>" \
  --target-branch main
```

Review and merge the MR through GitLab.

### Automatic Tag Creation

After the MR is merged, CI automatically:

1. Detects the Chart.yaml version change on main
2. Creates and pushes the `haptic-chart-v<version>` tag
3. Triggers the release pipeline (OCI registry, GitLab release)

No manual tagging is required.

??? note "Manual Tagging (Fallback)"
    If automatic tagging fails, you can create the tag manually:

    ```bash
    git checkout main
    git pull origin main
    git tag -a haptic-chart-v<version> -m "Chart release v<version>"
    git push origin haptic-chart-v<version>
    ```

### What CI Does Automatically

When a `haptic-chart-v*` tag is pushed, CI will:

1. **Package Helm chart** as OCI artifact
2. **Push to GitLab registry** at `registry.gitlab.com/haproxy-haptic/haptic/charts`
3. **Sign with Cosign** (keyless)
4. **Create GitLab release** with release notes from CHANGELOG.md
5. **Trigger documentation build** with version tag

## Documentation Versioning

Each release creates a versioned documentation snapshot:

| Release Type | Docs Behavior |
|--------------|---------------|
| Stable (`0.1.0`) | Creates version, gets `latest` alias |
| Pre-release (`0.1.0-alpha.1`) | Creates version, no `latest` alias |
| Final after pre-release | Removes matching pre-release versions |

**Example lifecycle:**

1. `0.1.0-alpha.1` released -> Docs at `/v0.1.0-alpha.1/`
2. `0.1.0-alpha.2` released -> Docs at `/v0.1.0-alpha.2/`
3. `0.1.0` released -> Docs at `/v0.1.0/` with `latest` alias, alpha versions removed

## Pre-release vs Final Release

### Pre-releases

Pre-releases (alpha, beta, rc) have these differences:

- Docker images built but **don't get `latest` tag**
- Documentation created but **not marked as `latest`**
- GitLab release marked as **pre-release**
- Not recommended for production use

### Final Releases

Final releases (no suffix):

- Docker images get `latest` tags
- Documentation gets `latest` alias
- Pre-release documentation versions are removed
- Recommended for production use

## Troubleshooting

### Release Script Fails

| Error | Solution |
|-------|----------|
| "Working directory is not clean" | Commit or stash changes |
| "CHANGELOG.md has no entry" | Add `## [version]` section |
| "Invalid version format" | Use `X.Y.Z` or `X.Y.Z-suffix.N` |

### CI Pipeline Fails

1. **Check GitLab CI logs** for specific error
2. **Verify tests pass locally** with `make test`
3. **Check Docker builds** work locally

### Docker Image Missing

If images don't appear after release:

1. Check `release-controller-images` job completed
2. Verify registry authentication succeeded
3. Check for build errors in job logs

## Supply Chain Security

All release artifacts are signed and include security metadata:

### Artifact Signing

All artifacts are signed with [Cosign](https://github.com/sigstore/cosign) using keyless OIDC:

- **Binaries**: Checksums file signed with detached signature
- **Docker images**: Each image tag signed
- **Helm chart**: OCI artifact signed

**Verify image signature:**

```bash
cosign verify \
  --certificate-identity-regexp='https://gitlab.com/haproxy-haptic/.*' \
  --certificate-oidc-issuer='https://gitlab.com' \
  registry.gitlab.com/haproxy-haptic/haptic:v0.1.0
```

### SBOM (Software Bill of Materials)

Each Docker image includes an SBOM attestation in SPDX format:

**View SBOM:**

```bash
cosign verify-attestation \
  --type spdxjson \
  --certificate-identity-regexp='https://gitlab.com/haproxy-haptic/.*' \
  --certificate-oidc-issuer='https://gitlab.com' \
  registry.gitlab.com/haproxy-haptic/haptic:v0.1.0 \
  | jq -r '.payload' | base64 -d | jq '.predicate'
```

The SBOM lists all packages, libraries, and dependencies in the container image.

## Version Files Reference

| File | Content | Updated By |
|------|---------|------------|
| `VERSION` | Controller version | Release script |
| `Chart.yaml:version` | Chart version | Chart release script |
| `Chart.yaml:appVersion` | Controller version | Controller release script |
| `Chart.yaml` annotation | Image version | Controller release script |
