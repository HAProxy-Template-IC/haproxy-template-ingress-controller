# GitHub registry migration

The migration copies published artifacts without rebuilding them. Controller
images, the SPOA bundle, Varnish, and the Helm chart keep their original manifest
digests, platform manifests, layers, signatures, and SBOMs. Imported signatures
keep their original GitLab signing identities; later GitHub releases use their
GitHub workflow identities.

`.github/release-registry-mirror.json` pins the released tags and their complete
OCI object inventory. It records OCI referrers and any legacy `.sig`, `.att`, and
`.sbom` tags. The copier rejects changed source tags and conflicting destination tags
before writing any tags for that component. It copies by digest, includes every
platform, and verifies each manifest's bytes, every referenced blob's digest and
size, and signature discoverability at the destination. The verifier also rejects
an inventory that omits objects referenced by a copied manifest.

Immutable manifest reads are shared within each component, and independent
metadata checks use four workers. When a registry redirects a blob to storage,
the verifier strips registry credentials and hashes the returned bytes against
the declared digest and size.

`release-registry-mirror.yml` runs the four components independently on standard
GitHub runners. It is a migration operation, outside PR verification and scheduled
CI. Its token can read source code and write packages; it cannot modify repository
contents. No image aliases, chart defaults, or publication settings change when
the mirror runs.

The mirror retains these paths under `ghcr.io/haproxy-template-ic`:

| Artifact | Package path |
| --- | --- |
| Controller | `haptic` |
| Helm chart | `haptic/charts/haptic` |
| SPOA bundle | `haptic/spoa-hub` |
| Varnish | `haptic/varnish` |

The separate `haptic-ci` package holds the seven pinned CI toolchains and fixture
images. Each package must permit anonymous pulls before CI or chart references
move to it. Package visibility is an account setting; a successful authenticated
copy does not prove public access.

For an anonymous check of a copied component, run:

```sh
python3 -m scripts.ci.github_release_mirror \
  --owner HAProxy-Template-IC --component chart --anonymous \
  --output .pilot/released-artifacts-chart-public.json
```

The mirror uses ORAS 1.3.4, pinned to its signed release tag's commit. ORAS copies
all manifests in an image index and supports recursive attachment copying. The
additional inventory verification checks what actually arrived.

References: [ORAS copy](https://oras.land/docs/commands/oras_cp/),
[ORAS 1.3.4](https://github.com/oras-project/oras/releases/tag/v1.3.4),
[GitHub package visibility](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility).
