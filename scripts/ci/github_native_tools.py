"""Select native ARM tools from the repository's shared build pins."""

import argparse
import json
from pathlib import Path
import re
import subprocess


def plan(pins, checksums, images):
    if not re.fullmatch(r"[a-zA-Z0-9./:_-]+@sha256:[a-f0-9]{64}", pins["GO_BUILD_IMAGE"]):
        raise ValueError("Native Go selection requires an immutable build image")
    version_vars = [item.removeprefix("GOLANG_VERSION=") for item in images["linux/arm64"]["config"]["Env"]
                    if item.startswith("GOLANG_VERSION=")]
    if len(version_vars) != 1 or not re.fullmatch(r"\d+\.\d+\.\d+", version_vars[0]):
        raise ValueError("Pinned Go image does not declare one exact compiler version")
    go_version = version_vars[0]
    if not go_version.startswith(pins["GO_VERSION"] + "."):
        raise ValueError("Pinned Go image and GO_VERSION disagree")
    tools = []
    for name, filename, url in (
        ("kind", "kind", "https://github.com/kubernetes-sigs/kind/releases/download/v{version}/kind-linux-arm64"),
        ("helm", "helm.tar.gz", "https://get.helm.sh/helm-v{version}-linux-arm64.tar.gz"),
        ("kubectl", "kubectl", "https://dl.k8s.io/release/v{version}/bin/linux/arm64/kubectl"),
        ("yq", "yq", "https://github.com/mikefarah/yq/releases/download/v{version}/yq_linux_arm64"),
    ):
        version = pins[name.upper() + "_VERSION"]
        if not re.fullmatch(r"\d+\.\d+\.\d+", version):
            raise ValueError("Invalid pinned native tool version: " + name)
        digest = checksums.get(name, {}).get(version, "")
        if not re.fullmatch(r"[a-f0-9]{64}", digest):
            raise ValueError(f"Add the ARM64 {name} {version} checksum to .github/native-tool-checksums.json")
        tools.append({"name": name, "filename": filename, "version": version,
                      "url": url.format(version=version), "sha256": digest})
    return {"go_version": go_version, "go_image": pins["GO_BUILD_IMAGE"], "tools": tools}


def verify(selected, *, output=subprocess.check_output):
    versions = {
        "go": output(["go", "env", "GOVERSION"], text=True).strip().removeprefix("go"),
        "kind": output(["kind", "version"], text=True).split()[1].removeprefix("v"),
        "helm": output(["helm", "version", "--template", "{{.Version}}"], text=True).strip().removeprefix("v"),
        "kubectl": json.loads(output(["kubectl", "version", "--client", "-o", "json"], text=True))[
            "clientVersion"]["gitVersion"].removeprefix("v"),
        "yq": output(["yq", "--version"], text=True).split()[-1].removeprefix("v"),
    }
    expected = {"go": selected["go_version"], **{tool["name"]: tool["version"] for tool in selected["tools"]}}
    if versions != expected:
        raise ValueError(f"Installed native tools differ from the build pins: {versions}, expected {expected}")
    return versions


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    select = commands.add_parser("plan")
    select.add_argument("pins", type=Path)
    select.add_argument("image_config", type=Path)
    check = commands.add_parser("verify")
    check.add_argument("plan", type=Path)
    args = parser.parse_args()
    if args.command == "verify":
        print(json.dumps(verify(json.loads(args.plan.read_text())), indent=2))
        return
    checksums = json.loads(Path(".github/native-tool-checksums.json").read_text())
    print(json.dumps(plan(json.loads(args.pins.read_text()), checksums,
                          json.loads(args.image_config.read_text())), indent=2))


if __name__ == "__main__":
    main()
