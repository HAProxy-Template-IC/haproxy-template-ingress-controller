# Releasing

## Overview

The controller and the Helm chart are released together: one version number, one `v<version>` git tag, one changelog, one release pipeline. The `VERSION` file is the single source of truth; `charts/haptic/Chart.yaml` carries the same value in both `version` and `appVersion`, and CI refuses to tag a release when they disagree.

Versions follow [Semantic Versioning](https://semver.org/) with support for pre-release suffixes.

## Version Numbering

**Format:** `MAJOR.MINOR.PATCH[-PRERELEASE]`

| Type | Example | Description |
|------|---------|-------------|
| Stable release | `0.1.0`, `1.0.0` | Production-ready version |
| Alpha | `0.1.0-alpha.1` | Early testing, APIs may change |
| Beta | `0.1.0-beta.1` | Feature complete, needs testing |
| Release candidate | `0.1.0-rc.1` | Final testing before release |

## CHANGELOG Conventions

There is one changelog file, `CHANGELOG.md` at the repository root, covering the controller and the Helm chart. It follows the [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format with an `## [Unreleased]` header at the top.

- Controller changes go in a release's top-level `### Added`/`### Changed`/`### Fixed`/… sections.
- Helm chart changes (values, templates, chart defaults, values migrations) go under the release's `### Helm chart` subsection, with `#### Added`/`#### Changed`/… sub-headings.

`charts/haptic/CHANGELOG.md` is a pointer to the root changelog and doesn't take entries.

## Prerequisites

Before releasing:

1. **Clean working directory** - All changes committed
2. **All tests passing** - CI pipeline green on main branch
3. **Documentation updated** - Any new features documented

## Release Process

The main branch is protected, so releases are made via merge requests. CI automatically creates the tag when the `VERSION` file changes on main.

### Step 1: Curate the changelog

Rewrite the accumulated `## [Unreleased]` entries in `CHANGELOG.md` into user-facing release notes: merge related entries, and drop items that never shipped in a release. Update the hand-curated `artifacthub.io/changes` annotation in `charts/haptic/Chart.yaml` with summary bullets for the new release.

Don't rename `[Unreleased]` yourself — the release script does the mechanical promotion.

### Step 2: Cut a release branch

`main` is protected, so the release commit lands via merge request. Branch first; the script commits to whatever branch is currently checked out.

```bash
git checkout -b release/v<version>
```

### Step 3: Run the release script

```bash
make release RELEASE_VERSION=<version>
# or directly: ./scripts/release.sh <version>
```

The script:

- Validates the version format (`X.Y.Z` or `X.Y.Z-suffix.N`)
- Promotes `CHANGELOG.md` `[Unreleased]` to `[<version>] - <date>` and leaves a fresh empty `[Unreleased]` above it (skipped when a `[<version>]` section already exists)
- Writes `<version>` to the `VERSION` file
- Updates `Chart.yaml` `version`, `appVersion`, and the `artifacthub.io/images` annotation (controller and spoa-hub image tags)
- Updates the `helm install ... --version <version>` examples in the READMEs and docs, and the landing page's fallback version
- Stages and commits everything as `release: haptic v<version>`

The docs-site changelog page needs no release-time sync: it is generated from `CHANGELOG.md` on every mkdocs build (`docs/site/hooks/changelog.py`).

The script does **not** create a tag — that happens in CI after the MR merges.

### Step 4: Push and open the MR

```bash
git push -u origin release/v<version>

glab mr create --title "release: haptic v<version>" \
  --description "Release haptic v<version>" \
  --target-branch main
```

Review and merge through GitLab.

### Automatic Tag Creation

After the MR is merged, CI automatically:

1. Runs the full post-merge pipeline (unit, e2e matrix, conformance) — the tag job waits for all of it
2. Verifies `VERSION`, `Chart.yaml` `version`, and `appVersion` agree
3. Creates and pushes the `v<version>` tag
4. Triggers the release pipeline

No manual tagging is required.

??? note "Manual Tagging (Fallback)"
    If automatic tagging fails, you can create the tag manually:

    ```bash
    git checkout main
    git pull origin main
    git tag -a v<version> -m "Release <version>"
    git push origin v<version>
    ```

### What CI Does Automatically

When a `v*` tag is pushed, CI will:

1. **Build binaries** for linux/amd64, linux/arm64, linux/arm/v7
2. **Create the GitLab release** with:
    - Signed binaries
    - SHA256 checksums
    - Release notes from the version's `CHANGELOG.md` section (controller + chart)
    - Pre-release flag (for alpha/beta/rc versions)
3. **Build Docker images** for every supported HAProxy series (3.0-3.4)
4. **Build the spoa-hub image** (`spoa-hub:<version>`)
5. **Package the Helm chart** and push it as a signed OCI artifact to `registry.gitlab.com/haproxy-haptic/haptic/charts`
6. **Sign all artifacts** with Cosign (keyless OIDC)
7. **Generate SBOM** (Software Bill of Materials) for each image and attach it as a Cosign attestation
8. **Trigger the versioned documentation build** (the `/docs/` site)

## Documentation Versioning

Each release creates a versioned snapshot of the documentation site:

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
| "CHANGELOG.md has no [Unreleased] section" | Restore the `## [Unreleased]` header |
| "Invalid version format" | Use `X.Y.Z` or `X.Y.Z-suffix.N` |

### CI Pipeline Fails

1. **Check GitLab CI logs** for specific error
2. **Verify tests pass locally** with `make test`
3. **Check Docker builds** work locally

### Docker Image Missing

If images don't appear after release:

1. Check the `release-controller` job completed
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

Release tags produce one image per supported HAProxy series (`<version>-haproxy<series>`). The `v` prefix from the git tag is stripped, so the git tag `v0.1.0` yields image tags `0.1.0-haproxy3.0`, `0.1.0-haproxy3.1`, `0.1.0-haproxy3.2`, `0.1.0-haproxy3.3`, `0.1.0-haproxy3.4`:

```bash
cosign verify \
  --certificate-identity-regexp='https://gitlab.com/haproxy-haptic/.*' \
  --certificate-oidc-issuer='https://gitlab.com' \
  registry.gitlab.com/haproxy-haptic/haptic:0.1.0-haproxy3.4
```

### SBOM (Software Bill of Materials)

Each controller image includes an SBOM attestation in SPDX format (the spoa-hub image uses CycloneDX — see [SPOA Hub](../operations/spoa-hub.md)):

**View SBOM:**

```bash
cosign verify-attestation \
  --type spdxjson \
  --certificate-identity-regexp='https://gitlab.com/haproxy-haptic/.*' \
  --certificate-oidc-issuer='https://gitlab.com' \
  registry.gitlab.com/haproxy-haptic/haptic:0.1.0-haproxy3.4 \
  | jq -r '.payload' | base64 -d | jq '.predicate'
```

The SBOM lists all packages, libraries, and dependencies in the container image.

## Version Files Reference

| File | Content | Updated By |
|------|---------|------------|
| `VERSION` | Release version (single source of truth) | Release script |
| `Chart.yaml:version` | Same value as `VERSION` | Release script |
| `Chart.yaml:appVersion` | Same value as `VERSION` | Release script |
| `Chart.yaml` `artifacthub.io/images` | Controller and spoa-hub image tags | Release script |
| `Chart.yaml` `artifacthub.io/changes` | Release summary bullets | By hand (Step 1) |
