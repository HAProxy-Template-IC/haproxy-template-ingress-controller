"""Exercise GitHub keyless signing against the temporary publication registry."""

import base64
import json
import os
from pathlib import Path
import subprocess


def run(*args):
    return subprocess.check_output(args, text=True)


def main():
    evidence = json.loads(Path(".pilot/snapshot-publication.json").read_text())
    identity = "https://github.com/" + os.environ["GITHUB_WORKFLOW_REF"]
    verification = ("--certificate-identity", identity,
                    "--certificate-oidc-issuer", "https://token.actions.githubusercontent.com")
    registry = ("--allow-http-registry",)
    output = Path(".pilot/signatures")
    output.mkdir()
    results = []
    for index in evidence["indexes"]:
        repository = index["reference"].rsplit(":", 1)[0]
        reference = repository + "@" + index["digest"]
        name = repository.rsplit("/", 1)[1]
        run("cosign", "sign", "--yes", *registry, reference)
        signature = run("cosign", "verify", *registry, *verification, reference)
        (output / f"{name}-signature.json").write_text(signature)
        signatures = json.loads(signature)
        if not signatures or any(item["critical"]["image"]["docker-manifest-digest"] != index["digest"]
                                 for item in signatures):
            raise ValueError(f"{reference}: verified signature has a different subject")
        sbom_path = output / f"{name}-amd64.spdx.json"
        image = next(item for item in evidence["images"]
                     if item["published"].startswith(repository + ":") and item["platform"] == "linux/amd64")
        run("syft", "scan", "docker:" + image["source"], "-o", "spdx-json=" + str(sbom_path))
        run("cosign", "attest", "--yes", *registry, "--type", "spdxjson", "--predicate", str(sbom_path), reference)
        attestation = run("cosign", "verify-attestation", *registry, *verification, "--type", "spdxjson", reference)
        (output / f"{name}-attestation.json").write_text(attestation)
        expected = json.loads(sbom_path.read_text())
        verified = [json.loads(base64.b64decode(json.loads(line)["payload"]))
                    for line in attestation.splitlines() if line.strip()]
        if not any(document["predicate"] == expected and
                   any(subject["digest"].get("sha256") == index["digest"].removeprefix("sha256:")
                       for subject in document["subject"]) for document in verified):
            raise ValueError(f"{reference}: verified SBOM differs from the generated predicate")
        results.append({"reference": reference, "sbom_platform": "linux/amd64"})
    chart = evidence["chart"]["reference"]
    run("cosign", "sign", "--yes", *registry, chart)
    (output / "chart-signature.json").write_text(run("cosign", "verify", *registry, *verification, chart))
    bundle = output / "checksums.sigstore.json"
    run("cosign", "sign-blob", "--yes", "--bundle", str(bundle), "dist/checksums.txt")
    run("cosign", "verify-blob", *verification, "--bundle", str(bundle), "dist/checksums.txt")
    rejected = subprocess.run(["cosign", "verify", *registry, "--certificate-identity", identity + "-wrong",
                               "--certificate-oidc-issuer", "https://token.actions.githubusercontent.com", chart],
                              capture_output=True, text=True, check=False)
    if rejected.returncode == 0:
        raise ValueError("Signature verification accepted a different workflow identity")
    (output / "wrong-identity.log").write_text(rejected.stderr)
    (output / "receipt.json").write_text(json.dumps({"identity": identity, "images": results, "chart": chart,
                                                    "checksums_verified": True, "wrong_identity_rejected": True},
                                                   indent=2) + "\n")


if __name__ == "__main__":
    main()
