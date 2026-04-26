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

There are two separate changelog files, one per release artifact:

| File | Covers |
|------|--------|
| `CHANGELOG.md` | Controller-facing changes (CLI, metrics, CRD behaviour, controller bug fixes) |
| `charts/haptic/CHANGELOG.md` | Helm chart changes (values, templates, chart defaults) |

Changes that touch both belong in both files. Each file follows the [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format with an `## [Unreleased]` header at the top. The release scripts do **not** rewrite the changelog for you — you rename `[Unreleased]` to `[<version>] - <date>` and add a fresh empty `[Unreleased]` above it manually, and the script aborts if it doesn't find an entry for the version you're releasing.

## Prerequisites

Before releasing:

1. **Clean working directory** - All changes committed
2. **Relevant changelog updated** - `CHANGELOG.md` for controller releases, `charts/haptic/CHANGELOG.md` for chart-only releases
3. **All tests passing** - CI pipeline green on main branch
4. **Documentation updated** - Any new features documented

## Controller Release Process

The main branch is protected, so releases are made via merge requests. CI automatically creates tags when the VERSION file changes on main.

### Step 1: Update CHANGELOG.md

Promote the existing `[Unreleased]` block to a versioned entry, then leave a fresh empty `[Unreleased]` above it. The order matters — `[Unreleased]` must stay at the top:

```markdown
## [Unreleased]

## [0.1.0-alpha.1] - 2026-04-25

### Added

- New feature X
```

### Step 2: Cut a Release Branch

`main` is protected, so the release commit lands via merge request. Branch first; the script commits to whatever branch is currently checked out.

```bash
git checkout -b release/controller-v<version>
```

### Step 3: Run the Release Script

```bash
make release-controller VERSION=<version>
# or directly: ./scripts/release-controller.sh <version>
```

The script:

- Validates the version format (`X.Y.Z` or `X.Y.Z-suffix.N`)
- Aborts unless `CHANGELOG.md` already contains a `## [<version>]` entry from Step 1
- Writes `<version>` to the `VERSION` file
- Updates `Chart.yaml` `appVersion` and the `artifacthub.io/images` annotation (the latter is rewritten to `haptic:<version>-haproxy<DEFAULT_HAPROXY>` from `versions.env`)
- Stages and commits those three files as `release: haptic-controller v<version>`

The script does **not** create a tag — that happens in CI after the MR merges.

### Step 4: Push and Open the MR

```bash
git push -u origin release/controller-v<version>

glab mr create --title "release: haptic-controller v<version>" \
  --description "Release haptic-controller v<version>" \
  --target-branch main
```

Review and merge through GitLab.

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
3. **Build Docker images** for HAProxy 3.0, 3.1, 3.2, 3.3
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

### Step 1: Update the chart CHANGELOG

Same shape as the controller changelog — keep `[Unreleased]` at the top and add the versioned entry below it in `charts/haptic/CHANGELOG.md`:

```markdown
## [Unreleased]

## [0.2.0] - 2026-04-25

### Changed

- Updated default resource limits
```

### Step 2: Cut a Release Branch

```bash
git checkout -b release/haptic-chart-v<version>
```

### Step 3: Run the Release Script

```bash
make release-chart CHART_VERSION=<version>
# or directly: ./scripts/release-chart.sh <version>
```

The script:

- Validates the version format
- Aborts unless `charts/haptic/CHANGELOG.md` already contains a `## [<version>]` entry from Step 1
- Updates `Chart.yaml` `version`
- Updates the `helm install ... --version <version>` examples in the root and chart `README.md`
- Commits those changes as `release: chart v<version>`

### Step 4: Push and Open the MR

```bash
git push -u origin release/haptic-chart-v<version>

glab mr create --title "release: chart v<version>" \
  --description "Release chart v<version>" \
  --target-branch main
```

Review and merge through GitLab.

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

Release tags produce one image per supported HAProxy series (`<version>-haproxy<series>`). The `v` prefix from the git tag is stripped, so the git tag `v0.1.0` yields image tags `0.1.0-haproxy3.0`, `0.1.0-haproxy3.1`, `0.1.0-haproxy3.2`, `0.1.0-haproxy3.3`:

```bash
cosign verify \
  --certificate-identity-regexp='https://gitlab.com/haproxy-haptic/.*' \
  --certificate-oidc-issuer='https://gitlab.com' \
  registry.gitlab.com/haproxy-haptic/haptic:0.1.0-haproxy3.2
```

### SBOM (Software Bill of Materials)

Each Docker image includes an SBOM attestation in SPDX format:

**View SBOM:**

```bash
cosign verify-attestation \
  --type spdxjson \
  --certificate-identity-regexp='https://gitlab.com/haproxy-haptic/.*' \
  --certificate-oidc-issuer='https://gitlab.com' \
  registry.gitlab.com/haproxy-haptic/haptic:0.1.0-haproxy3.2 \
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
