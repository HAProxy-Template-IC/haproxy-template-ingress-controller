"""Copy pinned CI images to GHCR without changing their manifests or platforms."""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import urllib.error
import urllib.parse
import urllib.request


SOURCE_PATTERN = re.compile(
    r"registry\.gitlab\.com/haproxy-haptic/haptic/(?:ci(?:/python-docs)?|varnish)@sha256:([a-f0-9]{64})")


def plan(sources, repository):
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.-]+", repository):
        raise ValueError("GitHub repository must be an owner/name pair")
    destination = "ghcr.io/" + repository.split("/")[0].lower() + "/haptic-ci"
    if not sources or len(sources) != len(set(sources)):
        raise ValueError("Mirror inventory is empty or duplicated")
    result = []
    for source in sources:
        match = SOURCE_PATTERN.fullmatch(source)
        if not match:
            raise ValueError("Mirror source must be a pinned HAPTIC CI image")
        digest = "sha256:" + match[1]
        result.append({"source": source, "destination": destination + ":sha256-" + match[1],
                       "reference": destination + "@" + digest, "digest": digest})
    return result


def copy_images(images, command):
    for image in images:
        command(["bash", "scripts/retry.sh", "crane", "copy", "--no-clobber", "--jobs", "4",
                 image["source"], image["destination"]])
        actual = command(["crane", "digest", image["destination"]]).strip()
        if actual != image["digest"]:
            raise ValueError("Mirrored image differs from the source manifest: " + image["destination"])


def public_image(reference):
    name, digest = reference.removeprefix("ghcr.io/").split("@")
    query = urllib.parse.urlencode({"service": "ghcr.io", "scope": f"repository:{name}:pull"})
    try:
        with urllib.request.urlopen("https://ghcr.io/token?" + query, timeout=30) as response:
            token = json.load(response)["token"]
        request = urllib.request.Request(f"https://ghcr.io/v2/{name}/manifests/{digest}", method="HEAD", headers={
            "Authorization": "Bearer " + token,
            "Accept": ", ".join(("application/vnd.oci.image.index.v1+json",
                                  "application/vnd.docker.distribution.manifest.list.v2+json",
                                  "application/vnd.oci.image.manifest.v1+json",
                                  "application/vnd.docker.distribution.manifest.v2+json")),
        })
        with urllib.request.urlopen(request, timeout=30) as response:
            if response.headers["Docker-Content-Digest"] != digest:
                raise ValueError("Anonymous image response has a different digest")
    except urllib.error.HTTPError as error:
        error.close()
        if error.code in {401, 403, 404}:
            return False
        raise
    return True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", default=os.environ.get("GITHUB_REPOSITORY"), required=False)
    parser.add_argument("--inventory", type=Path, default=Path(".github/ci-registry-mirror.json"))
    parser.add_argument("--output", type=Path, default=Path(".pilot/registry-mirror.json"))
    modes = parser.add_mutually_exclusive_group()
    modes.add_argument("--copy", action="store_true")
    modes.add_argument("--check-public", action="store_true")
    args = parser.parse_args()
    if not args.repository:
        parser.error("--repository or GITHUB_REPOSITORY is required")
    images = plan(json.loads(args.inventory.read_text())["sources"], args.repository)
    if args.copy:
        copy_images(images, lambda command: subprocess.check_output(command, text=True))
    if args.copy or args.check_public:
        for image in images:
            image["public"] = public_image(image["reference"])
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps({"repository": args.repository, "copied": args.copy, "images": images}, indent=2) + "\n")
    print(f"CI mirror: {len(images)} pinned images; copied={args.copy}")
    if args.copy or args.check_public:
        count = sum(image["public"] for image in images)
        print(f"Anonymous access: {count}/{len(images)} images")
        if count != len(images):
            print("Set the haptic-ci package visibility to public before using it in CI.")
            if args.check_public:
                raise SystemExit(1)


if __name__ == "__main__":
    main()
