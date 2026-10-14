"""Store website version archives in GitHub Releases and a small Git catalog."""

import argparse
import base64
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import urllib.error
import urllib.parse

from .github import GitHub
from .github_pages import record
from .github_site_archive import download
from .github_site_versions import assemble, version_key


BRANCH = "website-state"
TAG = "website-content"
CATALOG = "catalog.json"


def optional(api, path):
    try:
        return api.get(path)
    except urllib.error.HTTPError as error:
        error.close()
        if error.code != 404:
            raise
        return None


def read(api, repository, baseline):
    root = f"repos/{repository}"
    reference = optional(api, root + "/git/ref/heads/" + BRANCH)
    if reference is None:
        return None, {"version": 1, "baseline": baseline, "releases": {}, "dev": None}
    sha = reference["object"]["sha"]
    tree = api.get(root + "/git/trees/" + sha)
    if tree.get("truncated") or len(tree["tree"]) != 1:
        raise ValueError("Website state branch must contain only its catalog")
    item = tree["tree"][0]
    if (item["path"], item["type"], item["mode"]) != (CATALOG, "blob", "100644"):
        raise ValueError("Website catalog is not a regular file")
    blob = api.get(root + "/git/blobs/" + item["sha"])
    if blob["encoding"] != "base64" or blob["size"] > 2_000_000:
        raise ValueError("Website catalog encoding or size is invalid")
    state = json.loads(base64.b64decode(blob["content"]))
    validate(state, baseline, repository)
    return sha, state


def validate(state, baseline, repository):
    if state.get("version") != 1 or state.get("baseline") != baseline or not isinstance(state.get("releases"), dict):
        raise ValueError("Website state does not match the pinned migration baseline")
    entries = list(state["releases"].items())
    if state.get("dev") is not None:
        entries.append(("dev", state["dev"]))
    for version, entry in entries:
        if version != "dev":
            version_key(version)
        if (entry["provenance"]["version"] != version or
                entry["provenance"]["source"]["repository"] != repository or
                not re.fullmatch(r"[a-f0-9]{64}", entry["sha256"]) or
                not 0 < entry["size"] < 2_000_000_000 or
                entry["url"] != f"https://github.com/{repository}/releases/download/{TAG}/website-{version}-{entry['sha256']}.tar.gz"):
            raise ValueError("Website publication catalog contains an invalid archive")


def advance(api, repository, state, entry):
    result = {**state, "releases": dict(state["releases"])}
    version = entry["provenance"]["version"]
    if version == "dev":
        previous = state.get("dev")
        if previous:
            old = previous["provenance"]["source"]["sha"]
            new = entry["provenance"]["source"]["sha"]
            compared = api.get(f"repos/{repository}/compare/{old}...{new}")
            if compared["status"] not in {"identical", "ahead"}:
                raise ValueError("Website development publication would roll back or diverge from its source")
        result["dev"] = entry
    else:
        previous = state["releases"].get(version)
        if previous is not None and previous != entry:
            raise ValueError("Released website archive cannot be replaced")
        result["releases"][version] = entry
    validate(result, state["baseline"], repository)
    return result


def write(api, repository, expected, state):
    root = f"repos/{repository}"
    current, _ = read(api, repository, state["baseline"])
    if current != expected:
        raise ValueError("Website state changed while publication was being prepared")
    encoded = json.dumps(state, indent=2, sort_keys=True) + "\n"
    tree = api.post(root + "/git/trees", {"tree": [
        {"path": CATALOG, "mode": "100644", "type": "blob", "content": encoded}]})
    commit = api.post(root + "/git/commits", {
        "message": "Record verified website publication", "tree": tree["sha"],
        "parents": [expected] if expected else []})
    if expected:
        api.request("PATCH", root + "/git/refs/heads/" + BRANCH, {"sha": commit["sha"], "force": False})
    else:
        api.post(root + "/git/refs", {"ref": "refs/heads/" + BRANCH, "sha": commit["sha"]})
    refreshed, actual = read(api, repository, state["baseline"])
    if refreshed != commit["sha"] or actual != state:
        raise ValueError("GitHub did not retain the expected website catalog")
    return refreshed


def upload(api, repository, bundle, provenance, scratch):
    expected = record(bundle)
    if expected["size"] >= 2_000_000_000:
        raise ValueError("Website archive exceeds the GitHub Release asset limit")
    root = f"repos/{repository}"
    release = optional(api, root + "/releases/tags/" + TAG)
    if release is None:
        release = api.post(root + "/releases", {"tag_name": TAG,
                          "target_commitish": provenance["source"]["sha"],
                          "name": "Website publication archives", "prerelease": True, "make_latest": "false",
                          "body": "Verified website content. The website-state branch records the published version catalog."})
    if release["draft"] or not release["prerelease"] or release["tag_name"] != TAG:
        raise ValueError("Website archive release has unexpected publication settings")
    name = f"website-{provenance['version']}-{expected['sha256']}.tar.gz"
    assets = list(api.all(root + f"/releases/{release['id']}/assets", None))
    matching = [asset for asset in assets if asset["name"] == name]
    if not matching:
        upload_path = scratch / name
        shutil.copyfile(bundle, upload_path)
        subprocess.run(["gh", "release", "upload", TAG, str(upload_path),
                        "--repo", repository], check=True)
        assets = list(api.all(root + f"/releases/{release['id']}/assets", None))
        matching = [asset for asset in assets if asset["name"] == name]
    if len(matching) != 1:
        raise ValueError("Website archive upload is missing or ambiguous")
    asset = matching[0]
    url = f"https://github.com/{repository}/releases/download/{TAG}/{name}"
    if (asset["size"] != expected["size"] or asset["digest"] != "sha256:" + expected["sha256"] or
            asset["state"] != "uploaded" or asset["browser_download_url"] != url):
        raise ValueError("Uploaded website archive differs from its local source")
    entry = {**expected, "url": url, "provenance": provenance}
    manifest = download(entry, scratch / "anonymous-download")
    if manifest["provenance"] != provenance:
        raise ValueError("Public website archive has unexpected provenance")
    return entry


def restore_current(api, repository, baseline, destination):
    head, state = read(api, repository, baseline)
    with tempfile.TemporaryDirectory(prefix="website-restore-") as temporary:
        scratch = Path(temporary)
        source = scratch / "baseline"
        download(baseline, source)
        entries = list(state["releases"].values()) + ([state["dev"]] if state["dev"] else [])
        publications = []
        for index, entry in enumerate(entries):
            output = scratch / str(index)
            manifest = download(entry, output)
            if manifest["provenance"] != entry["provenance"]:
                raise ValueError("Stored website archive differs from its catalog provenance")
            publications.append((output, manifest["provenance"]))
        result = assemble(source, publications, destination)
    return {**result, "state_head": head, "baseline_sha256": baseline["sha256"]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    baseline = json.loads(Path(".github/pages-baseline.json").read_text())
    repository = "/".join(urllib.parse.urlparse(baseline["url"]).path.strip("/").split("/")[:2])
    api = GitHub(os.environ.get("GH_TOKEN"))
    print(json.dumps(restore_current(api, repository, baseline, args.output), indent=2))


if __name__ == "__main__":
    main()
