"""Preserve released OCI artifacts, their platforms, and their signature attachments."""

import argparse
import base64
from concurrent.futures import ThreadPoolExecutor
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import urllib.error
import urllib.parse
import urllib.request

from scripts.ci.github import NoRedirect


COMPONENTS = {"controller": "haproxy-haptic/haptic", "chart": "haproxy-haptic/haptic/charts/haptic",
              "spoa": "haproxy-haptic/haptic/spoa-hub", "varnish": "haproxy-haptic/haptic/varnish"}
DIGEST = re.compile(r"sha256:[a-f0-9]{64}")
RELEASE_TAG = re.compile(r"\d+\.\d+\.\d+(?:-alpha\.\d+)?(?:-haproxy\d+\.\d+)?")
LEGACY_TAG = re.compile(r"sha256-[a-f0-9]{64}\.(?:sig|att|sbom)")
ACCEPT = ", ".join(("application/vnd.oci.image.index.v1+json", "application/vnd.oci.image.manifest.v1+json",
                    "application/vnd.docker.distribution.manifest.list.v2+json",
                    "application/vnd.docker.distribution.manifest.v2+json",
                    "application/vnd.oci.artifact.manifest.v1+json"))


class Registry:
    def __init__(self, host, name, actor="", credential="", *, write=False):
        self.host, self.name = host, name
        self.actor, self.credential = actor, credential
        self.actions = "pull,push" if write else "pull"
        self.token = None
        self.documents = {}
        self.descriptors = {}
        self.opener = urllib.request.build_opener(NoRedirect())

    def authenticate(self):
        if self.host == "ghcr.io":
            address, service = "https://ghcr.io/token", "ghcr.io"
        elif self.host == "registry.gitlab.com" and not self.credential and self.actions == "pull":
            address, service = "https://gitlab.com/jwt/auth", "container_registry"
        else:
            raise ValueError("Unsupported registry authentication destination")
        query = urllib.parse.urlencode({"service": service, "scope": f"repository:{self.name}:{self.actions}"})
        headers = {}
        if self.credential:
            headers["Authorization"] = "Basic " + base64.b64encode(
                f"{self.actor}:{self.credential}".encode()).decode()
        request = urllib.request.Request(address + "?" + query, headers=headers)
        with self.opener.open(request, timeout=30) as response:
            self.token = json.load(response)["token"]
        if not self.token:
            raise ValueError("Registry returned no pull token")

    def read(self, suffix, *, head=False, renew=True):
        if self.token is None:
            self.authenticate()
        request = urllib.request.Request(f"https://{self.host}/v2/{self.name}/{suffix}",
                                         method="HEAD" if head else "GET",
                                         headers={"Authorization": "Bearer " + self.token, "Accept": ACCEPT})
        try:
            response = self.opener.open(request, timeout=45)
        except urllib.error.HTTPError as error:
            error.close()
            if error.code == 401 and renew:
                self.authenticate()
                return self.read(suffix, head=head, renew=False)
            if error.code == 404:
                return None
            raise
        with response:
            if response.headers.get("Link"):
                raise ValueError("Registry inventory is paginated; cannot certify an incomplete response")
            content = response.read(16 * 1024 * 1024 + 1)
            if len(content) > 16 * 1024 * 1024:
                raise ValueError("Registry metadata exceeds its size limit")
            return content, response.headers

    def manifest(self, reference):
        if reference in self.descriptors:
            return self.descriptors[reference].copy()
        result = self.read("manifests/" + reference)
        if result is None:
            return None
        content, headers = result
        digest = "sha256:" + hashlib.sha256(content).hexdigest()
        if reference.startswith("sha256:") and digest != reference:
            raise ValueError("Manifest bytes differ from their requested digest")
        self.documents[digest] = json.loads(content)
        media_type = self.documents[digest].get("mediaType") or headers.get("Content-Type", "").split(";")[0]
        self.descriptors[digest] = {"digest": digest, "size": len(content), "mediaType": media_type}
        return self.descriptors[digest].copy()

    def references(self, digest):
        document = self.documents[digest]
        manifests = list(document.get("manifests", []))
        if "subject" in document:
            manifests.append(document["subject"])
        blobs = document.get("layers", []) + document.get("blobs", [])
        if "config" in document:
            blobs.append(document["config"])
        return manifests, blobs

    def blob(self, digest, expected_size):
        try:
            result = self.read("blobs/" + digest, head=True)
        except urllib.error.HTTPError as error:
            if error.code not in (302, 307):
                raise
            location = error.headers["Location"]
            error.close()
            return self.redirected_blob(location, digest, expected_size)
        if result is None:
            return None
        _, headers = result
        if headers.get("Docker-Content-Digest") != digest:
            raise ValueError("Registry blob response differs from its requested digest")
        return int(headers["Content-Length"])

    def redirected_blob(self, location, digest, expected_size):
        parsed = urllib.parse.urlsplit(location)
        if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password:
            raise ValueError("Blob storage redirect requires an HTTPS URL without credentials")
        request = urllib.request.Request(location)
        checksum, size = hashlib.sha256(), 0
        with self.opener.open(request, timeout=45) as response:
            while chunk := response.read(1024 * 1024):
                size += len(chunk)
                if size > expected_size:
                    raise ValueError("Redirected blob exceeds its declared size")
                checksum.update(chunk)
        if size != expected_size or "sha256:" + checksum.hexdigest() != digest:
            raise ValueError("Redirected blob bytes differ from their declared digest or size")
        return size

    def referrers(self, digest):
        result = set()
        for suffix in ("referrers/" + digest, "manifests/" + digest.replace(":", "-")):
            response = self.read(suffix)
            if response is not None:
                result.update(item["digest"] for item in json.loads(response[0]).get("manifests", []))
        return result


def validate_inventory(rows, owner):
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9-]*", owner):
        raise ValueError("Invalid GitHub organization")
    if len(rows) != len(COMPONENTS) or {row["repository"] for row in rows} != set(COMPONENTS.values()):
        raise ValueError("Release inventory must cover every HAPTIC registry component exactly once")
    for row in rows:
        if not row["roots"] or not row["manifests"]:
            raise ValueError("Release inventory is empty")
        for field, pattern in (("roots", RELEASE_TAG), ("legacy_tags", LEGACY_TAG)):
            for tag, digest in row[field].items():
                if not pattern.fullmatch(tag) or digest not in row["manifests"]:
                    raise ValueError("Release inventory contains an invalid tag or missing manifest")
        for digest, descriptor in row["manifests"].items():
            if not DIGEST.fullmatch(digest) or descriptor["size"] <= 0 or descriptor["mediaType"] not in ACCEPT.split(", "):
                raise ValueError("Release inventory contains an invalid manifest descriptor")
        for digest, size in row["blobs"].items():
            if not DIGEST.fullmatch(digest) or not isinstance(size, int) or size < 0:
                raise ValueError("Release inventory contains an invalid blob descriptor")
        for digest, children in row["referrers"].items():
            if digest not in row["manifests"] or not set(children) <= row["manifests"].keys():
                raise ValueError("Release inventory omits a signature attachment")


def destination_name(row, owner):
    return owner.lower() + "/" + row["repository"].removeprefix("haproxy-haptic/")


def copy_artifacts(row, source, destination, execute):
    tags = {**row["roots"], **row["legacy_tags"]}

    def preflight(item):
        tag, digest = item
        original = source.manifest(tag)
        if original is None or original["digest"] != digest:
            raise ValueError("Published source tag changed: " + tag)
        existing = destination.manifest(tag)
        if existing is not None and existing["digest"] != digest:
            raise ValueError("Destination tag already contains different bytes: " + tag)

    with ThreadPoolExecutor(max_workers=4) as pool:
        list(pool.map(preflight, tags.items()))
    for tag, digest in tags.items():
        execute(["oras", "cp", "--recursive", "--concurrency", "4",
                 f"{source.host}/{source.name}@{digest}", f"{destination.host}/{destination.name}:{tag}"])
    with ThreadPoolExecutor(max_workers=4) as pool:
        present = list(pool.map(destination.manifest, row["manifests"]))
    for digest, descriptor in zip(row["manifests"], present):
        if descriptor is None:
            execute(["oras", "cp", "--recursive", "--concurrency", "4",
                     f"{source.host}/{source.name}@{digest}", f"{destination.host}/{destination.name}"])


def verify_artifacts(row, destination):
    for tag, digest in {**row["roots"], **row["legacy_tags"]}.items():
        actual = destination.manifest(tag)
        if actual is None or actual["digest"] != digest:
            raise ValueError("Mirrored release tag differs: " + tag)
    def verify_manifest(item):
        digest, descriptor = item
        if destination.manifest(digest) != {**descriptor, "digest": digest}:
            raise ValueError("Mirrored platform or attachment manifest differs: " + digest)
        manifests, blobs = destination.references(digest)
        for child in manifests:
            expected = {key: child[key] for key in ("size", "mediaType")}
            if row["manifests"].get(child["digest"]) != expected:
                raise ValueError("Inventory omits a referenced platform or signature subject")
        for blob in blobs:
            if row["blobs"].get(blob["digest"]) != blob["size"]:
                raise ValueError("Inventory omits a referenced layer, config, or signature")
    def verify_blob(item):
        digest, size = item
        if destination.blob(digest, size) != size:
            raise ValueError("Mirrored layer, config, or signature blob differs: " + digest)
    def verify_referrers(item):
        digest, children = item
        if children and not set(children) <= destination.referrers(digest):
            raise ValueError("Mirrored signature attachment is undiscoverable: " + digest)
    with ThreadPoolExecutor(max_workers=4) as pool:
        list(pool.map(verify_manifest, row["manifests"].items()))
        list(pool.map(verify_blob, row["blobs"].items()))
        list(pool.map(verify_referrers, row["referrers"].items()))
    return {"registry": destination.host, "repository": destination.name, "release_tags": len(row["roots"]),
            "manifests": len(row["manifests"]), "blobs": len(row["blobs"]),
            "blob_bytes": sum(row["blobs"].values()), "legacy_tags": len(row["legacy_tags"]),
            "referrer_links": sum(map(len, row["referrers"].values()))}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--inventory", type=Path, default=Path(".github/release-registry-mirror.json"))
    parser.add_argument("--owner", required=True)
    parser.add_argument("--component", choices=COMPONENTS, required=True)
    parser.add_argument("--output", type=Path, required=True)
    modes = parser.add_mutually_exclusive_group()
    modes.add_argument("--copy", action="store_true")
    modes.add_argument("--verify-source", action="store_true")
    parser.add_argument("--anonymous", action="store_true")
    args = parser.parse_args()
    if args.copy and args.anonymous:
        parser.error("Anonymous verification cannot copy artifacts")
    rows = json.loads(args.inventory.read_text())
    validate_inventory(rows, args.owner)
    row = next(row for row in rows if row["repository"] == COMPONENTS[args.component])
    source = Registry("registry.gitlab.com", row["repository"])
    destination = Registry("ghcr.io", destination_name(row, args.owner),
                           "" if args.anonymous else os.environ.get("GITHUB_ACTOR", ""),
                           "" if args.anonymous else os.environ.get("GH_TOKEN", ""), write=args.copy)
    if args.verify_source:
        destination = source
    if args.copy:
        copy_artifacts(row, source, destination, lambda command: subprocess.run(command, check=True))
    verdict = verify_artifacts(row, destination)
    verdict.update({"inventory_sha256": hashlib.sha256(args.inventory.read_bytes()).hexdigest(),
                    "copied": args.copy, "anonymous": args.anonymous or args.verify_source})
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(verdict, indent=2) + "\n")
    print(json.dumps(verdict))


if __name__ == "__main__":
    main()
