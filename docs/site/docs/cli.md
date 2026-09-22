# Install the HAPTIC CLI

The `haptic` command inspects generated configuration and tests changes before
deployment. You don't need it to install HAPTIC with Helm or create routes.
Use the same HAPTIC version as your controller; newer commands may be absent
from an older release.

For the default Helm installation, check the controller image:

```bash
kubectl get deployment haptic-controller --namespace haptic \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="controller")].image}{"\n"}'
```

## Install a Linux release

Release binaries support Linux on AMD64, ARM64, and ARMv7. The following Bash
commands require `curl` and `sha256sum`. Enter the version from the
[releases page](https://gitlab.com/haproxy-haptic/haptic/-/releases), without its
leading `v`. The download is an executable, not an archive.

```bash
(
  set -eu
  case "$(uname -m)" in
    x86_64) haptic_arch=amd64 ;;
    aarch64|arm64) haptic_arch=arm64 ;;
    armv7l) haptic_arch=armv7 ;;
    *) echo "No release binary for this architecture" >&2; exit 1 ;;
  esac
  read -r -p "HAPTIC release version: " haptic_version
  haptic_asset="haptic-${haptic_version}-linux-${haptic_arch}"
  haptic_download="https://gitlab.com/haproxy-haptic/haptic/-/releases/v${haptic_version}/downloads"
  haptic_tmp=$(mktemp -d)
  trap 'rm -rf "$haptic_tmp"' EXIT
  cd "$haptic_tmp"
  curl --fail --location --remote-name "$haptic_download/$haptic_asset"
  curl --fail --location --remote-name "$haptic_download/checksums.txt"
  sha256sum --check --ignore-missing checksums.txt
  install -Dm755 "$haptic_asset" "$HOME/.local/bin/haptic"
)
```

Add the directory to your current shell's path and check the installed version:

```bash
export PATH="$HOME/.local/bin:$PATH"
haptic version
haptic --help
```

To keep that path in future shells, add the `export` line to your shell startup
file. Commands that validate HAProxy configuration also need a local `haproxy`
binary matching your deployment's HAProxy series. The container option below
includes it.

### Use a development build on Linux

For a controller built from `main`, extract the CLI from its exact image instead
of downloading a release binary. With Docker and access to the default Helm
installation:

```bash
(
  set -eu
  haptic_image=$(kubectl get deployment haptic-controller --namespace haptic \
    -o jsonpath='{.spec.template.spec.containers[?(@.name=="controller")].image}')
  haptic_container=$(docker create "$haptic_image")
  trap 'docker rm "$haptic_container" >/dev/null' EXIT
  mkdir -p "$HOME/.local/bin"
  docker cp "$haptic_container:/usr/local/bin/haptic" "$HOME/.local/bin/haptic"
  chmod 755 "$HOME/.local/bin/haptic"
)
export PATH="$HOME/.local/bin:$PATH"
haptic version
```

## Use a container on macOS, Windows, or Linux

There are no native macOS or Windows release binaries. Use Docker with Linux
containers; on Windows, run these Bash commands in Windows Subsystem for Linux (WSL) 2.

For offline template tests, prepare `config.yaml` and a `schemas` directory as
described in [Test your templates](validation-tests.md). From that directory,
select the controller image used by your installation and run validation:

```bash
haptic_image=$(kubectl get deployment haptic-controller --namespace haptic \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="controller")].image}')
docker run --rm \
  --mount "type=bind,source=$PWD,target=/work,readonly" \
  --workdir /work "$haptic_image" \
  validate --file config.yaml --schema-dir /work/schemas
```

Only the image lookup uses cluster access. Validation reads the mounted files
and runs with the image's matching HAProxy binary.

For cluster inspection without a local CLI, run the command in a controller pod:

```bash
kubectl exec --namespace haptic deployment/haptic-controller --container controller \
  -- haptic config view --namespace haptic
```

Continue with [template tests](validation-tests.md),
[pre-deployment checks](operations/validate-before-deploy.md), or
[fleet diagnostics](operations/diagnostics.md).
