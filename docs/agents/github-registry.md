# GitHub container registry

HAPTIC publishes to public packages under `ghcr.io/haproxy-haptic`:

| Artifact | Package path |
| --- | --- |
| Controller | `haptic` |
| Helm chart | `haptic/charts/haptic` |
| SPOA bundle | `haptic/spoa-hub` |
| Varnish | `haptic/varnish` |
| CI toolchains and fixture images | `haptic-ci` |

Releases up to 0.5.0 were copied from GitLab's registry by digest, with their
platform manifests, layers, signatures, and SBOMs (GitHub run 38498045175). They
keep their original GitLab signing identities; later releases carry the GitHub
release workflow's identity. The SPOA hub base image still comes from
`registry.gitlab.com/haproxy-haptic/haproxy-spoa-hub`, a separate project.

## CI toolchains

`.github/ci-toolchains.json` pins one CI image per HAProxy series plus the docs
image, each with the build-input hash it was built from. A job whose inputs
match a pin pulls it by digest. When the inputs differ, the job pulls
`haptic-ci:<hash>-hp<series>` (or `haptic-ci:docs-<hash>`) and checks its
build-input label; if that tag doesn't exist yet, it builds the image in the job.

Only `.github/workflows/ci-images.yml` pushes those tags. It runs on `main` when
an image input changes and publishes every image the job had to build, so the
next pull request reuses it. Refresh the pins in `.github/ci-toolchains.json`
when you want the bootstrap image itself to move.
