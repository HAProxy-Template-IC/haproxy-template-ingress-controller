"""Verify snapshot images and chart publication against a loopback registry."""

import hashlib
import json
import os
from pathlib import Path
import subprocess
from urllib.request import Request, urlopen


REGISTRY = "127.0.0.1:5000"
ACCEPT = ", ".join(("application/vnd.oci.image.index.v1+json",
                    "application/vnd.docker.distribution.manifest.list.v2+json",
                    "application/vnd.oci.image.manifest.v1+json",
                    "application/vnd.docker.distribution.manifest.v2+json"))


def run(*args):
    return subprocess.check_output(args, text=True)


def manifest(repository, reference):
    request = Request(f"http://{REGISTRY}/v2/{repository}/manifests/{reference}",
                      headers={"Accept": ACCEPT})
    with urlopen(request, timeout=30) as response:
        body = response.read()
        digest = "sha256:" + hashlib.sha256(body).hexdigest()
        if response.headers["Docker-Content-Digest"] != digest:
            raise ValueError(f"{repository}:{reference}: registry content digest differs")
        return json.loads(body), digest


def main():
    images = [item for item in json.loads(Path("dist/artifacts.json").read_text())
              if item["type"] == "Docker Image"]
    expected = {f"haproxy{version}": {"linux/amd64", "linux/arm64", "linux/arm/v7"}
                for version in ("30", "31", "32", "33", "34")}
    expected["varnish"] = {"linux/amd64", "linux/arm64"}
    groups = {}
    evidence = {"images": [], "indexes": []}
    for item in images:
        source = item["name"]
        image = json.loads(run("docker", "image", "inspect", source))[0]
        platform = "/".join(filter(None, (image["Os"], image["Architecture"], image.get("Variant"))))
        if item["extra"]["Platforms"] != [platform]:
            raise ValueError(f"{source}: image platform differs from build metadata")
        group = item["extra"]["ID"]
        if group not in expected:
            raise ValueError(f"Unexpected snapshot image group: {group}")
        repository = f"snapshot/{group}"
        tag = platform.replace("/", "-")
        target = f"{REGISTRY}/{repository}:{tag}"
        print(run("docker", "tag", source, target), end="")
        print(run("docker", "push", target), end="")
        document, digest = manifest(repository, tag)
        if document["config"]["digest"] != image["Id"]:
            raise ValueError(f"{target}: published image config differs")
        print(run("docker", "pull", "--platform", platform, f"{REGISTRY}/{repository}@{digest}"), end="")
        pulled = json.loads(run("docker", "image", "inspect", f"{REGISTRY}/{repository}@{digest}"))[0]
        if pulled["Id"] != image["Id"]:
            raise ValueError(f"{target}: pulled image differs from the build")
        members = groups.setdefault(group, {})
        if platform in members:
            raise ValueError(f"Duplicate snapshot platform: {group} {platform}")
        members[platform] = {"reference": f"{REGISTRY}/{repository}@{digest}", "digest": digest}
        evidence["images"].append({"source": source, "published": target, "digest": digest,
                                   "platform": platform, "config_digest": image["Id"]})
    if {group: set(members) for group, members in groups.items()} != expected:
        raise ValueError("Snapshot image inventory does not cover all supported platforms")
    for group, members in groups.items():
        repository = f"snapshot/{group}"
        target = f"{REGISTRY}/{repository}:verified"
        run("docker", "manifest", "create", "--insecure", target,
            *(item["reference"] for item in members.values()))
        run("docker", "manifest", "push", "--insecure", target)
        document, digest = manifest(repository, "verified")
        published = {"/".join(filter(None, (item["platform"]["os"], item["platform"]["architecture"],
                                           item["platform"].get("variant")))): item["digest"]
                     for item in document["manifests"]}
        if len(document["manifests"]) != len(members) or published != {
                platform: item["digest"] for platform, item in members.items()}:
            raise ValueError(f"{target}: published manifest platform inventory differs")
        evidence["indexes"].append({"reference": target, "digest": digest, "platforms": published})
    version = "0.0.0-github.g" + os.environ["PILOT_SOURCE_SHA"][:8]
    destination = Path(".pilot/chart-publication")
    destination.mkdir()
    run("helm", "package", "charts/haptic", "--version", version, "--app-version", version,
        "--destination", str(destination))
    archive = destination / f"haptic-{version}.tgz"
    run("helm", "push", str(archive), f"oci://{REGISTRY}/charts", "--plain-http")
    retrieved = destination / "retrieved"
    retrieved.mkdir()
    run("helm", "pull", f"oci://{REGISTRY}/charts/haptic", "--version", version,
        "--destination", str(retrieved), "--plain-http")
    if archive.read_bytes() != (retrieved / archive.name).read_bytes():
        raise ValueError("Retrieved chart differs from the packaged snapshot")
    _, chart_digest = manifest("charts/haptic", version)
    evidence["chart"] = {"version": version, "sha256": hashlib.sha256(archive.read_bytes()).hexdigest(),
                         "reference": f"{REGISTRY}/charts/haptic@{chart_digest}"}
    Path(".pilot/snapshot-publication.json").write_text(json.dumps(evidence, indent=2) + "\n")


if __name__ == "__main__":
    main()
