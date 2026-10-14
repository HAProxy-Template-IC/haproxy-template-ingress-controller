import copy
import hashlib
import io
import json
from pathlib import Path
import unittest
from unittest.mock import Mock
import urllib.error
import urllib.parse

import yaml

from scripts.ci.github_release_mirror import (
    COMPONENTS, Registry, copy_artifacts, destination_name, validate_inventory, verify_artifacts,
)


ROOT = Path(__file__).parents[2]
INDEX = "application/vnd.oci.image.index.v1+json"
MANIFEST = "application/vnd.oci.image.manifest.v1+json"
BLOB = "sha256:" + "1" * 64


def encoded(document):
    content = json.dumps(document, separators=(",", ":")).encode()
    digest = "sha256:" + hashlib.sha256(content).hexdigest()
    return digest, content, {"digest": digest, "size": len(content), "mediaType": document["mediaType"]}


class FixtureRegistry(Registry):
    def __init__(self):
        super().__init__("registry.gitlab.com", COMPONENTS["controller"])
        child, child_content, descriptor = encoded({"mediaType": MANIFEST,
                                                   "config": {"digest": BLOB, "size": 10}, "layers": []})
        parent, parent_content, parent_descriptor = encoded({"mediaType": INDEX, "manifests": [descriptor]})
        signature, signature_content, _ = encoded({"mediaType": MANIFEST, "subject": parent_descriptor,
                                                   "config": {"digest": BLOB, "size": 10}, "layers": []})
        self.root, self.child, self.signature = parent, child, signature
        self.content = {child: child_content, parent: parent_content, signature: signature_content}
        self.tags = {"0.5.0-haproxy3.4": parent, parent.replace(":", "-") + ".sig": signature}
        self.sizes = {BLOB: 10}
        self.attachments = {parent: [signature]}
        self.row = {"repository": self.name, "roots": {"0.5.0-haproxy3.4": parent},
                    "manifests": {digest: {"size": len(content), "mediaType": json.loads(content)["mediaType"]}
                                  for digest, content in self.content.items()},
                    "blobs": self.sizes.copy(), "referrers": copy.deepcopy(self.attachments),
                    "legacy_tags": {parent.replace(":", "-") + ".sig": signature}}

    def read(self, suffix, *, head=False, renew=True):
        resource, value = suffix.split("/", 1)
        if resource == "manifests":
            value = self.tags.get(value, value)
            return (self.content[value], {}) if value in self.content else None
        if resource == "blobs":
            return ((b"", {"Docker-Content-Digest": value, "Content-Length": str(self.sizes[value])})
                    if value in self.sizes else None)
        if resource == "referrers":
            return json.dumps({"manifests": [{"digest": item} for item in self.attachments.get(value, [])]}).encode(), {}
        raise AssertionError(suffix)


class ReleasedArtifactTests(unittest.TestCase):
    def setUp(self):
        self.registry = FixtureRegistry()
        self.row = copy.deepcopy(self.registry.row)

    def test_verifies_release_platform_blob_and_both_attachment_formats(self):
        receipt = verify_artifacts(self.row, self.registry)
        self.assertEqual((receipt["release_tags"], receipt["manifests"], receipt["blobs"],
                          receipt["referrer_links"], receipt["legacy_tags"]), (1, 3, 1, 1, 1))

    def test_detects_missing_platform_signature_blob_or_attachment_binding(self):
        for mutation in (
            lambda r: r.content.pop(r.child),
            lambda r: r.content.pop(r.signature),
            lambda r: r.sizes.pop(BLOB),
            lambda r: r.sizes.update({BLOB: 11}),
            lambda r: r.attachments.clear(),
            lambda r: r.tags.pop(r.root.replace(":", "-") + ".sig"),
        ):
            registry = FixtureRegistry()
            mutation(registry)
            with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                verify_artifacts(self.row, registry)

    def test_checks_manifest_bytes_even_when_reference_has_the_expected_digest(self):
        self.registry.content[self.registry.child] += b" "
        with self.assertRaisesRegex(ValueError, "bytes differ"):
            verify_artifacts(self.row, self.registry)

    def test_legacy_manifest_format_comes_from_content_type_without_rewriting_bytes(self):
        content = b'{"schemaVersion":2,"layers":[]}'
        digest = "sha256:" + hashlib.sha256(content).hexdigest()
        registry = Registry("registry.gitlab.com", COMPONENTS["controller"])
        registry.read = Mock(return_value=(content, {"Content-Type": MANIFEST}))
        self.assertEqual(registry.manifest(digest), {"digest": digest, "size": len(content), "mediaType": MANIFEST})

    def test_digest_reads_are_reused_but_tags_are_read_again(self):
        registry = Registry("registry.gitlab.com", COMPONENTS["controller"])
        registry.read = Mock(return_value=(self.registry.content[self.registry.root], {}))
        registry.manifest("0.5.0-haproxy3.4")
        registry.manifest(self.registry.root)
        registry.manifest(self.registry.root)
        self.assertEqual(registry.read.call_count, 1)
        registry.manifest("0.5.0-haproxy3.4")
        self.assertEqual(registry.read.call_count, 2)

    def test_rejects_inventory_omissions_even_if_all_listed_objects_exist(self):
        for field, key in (("manifests", self.registry.child), ("blobs", BLOB)):
            row = copy.deepcopy(self.row)
            del row[field][key]
            with self.subTest(field=field), self.assertRaisesRegex(ValueError, "Inventory omits"):
                verify_artifacts(row, self.registry)

    def test_refuses_changed_source_or_conflicting_destination_before_any_write(self):
        for change_source in (True, False):
            source, destination = FixtureRegistry(), FixtureRegistry()
            target = source if change_source else destination
            target.tags[next(iter(self.row["legacy_tags"]))] = target.child
            execute = Mock()
            with self.subTest(source=change_source), self.assertRaises(ValueError):
                copy_artifacts(self.row, source, destination, execute)
            execute.assert_not_called()

    def test_copy_pins_source_digests_and_includes_all_platforms_and_attachments(self):
        destination = FixtureRegistry()
        destination.host, destination.name = "ghcr.io", "owner/haptic"
        execute = Mock()
        copy_artifacts(self.row, self.registry, destination, execute)
        self.assertEqual(execute.call_count, 2)
        for call in execute.call_args_list:
            command = call.args[0]
            self.assertIn("--recursive", command)
            self.assertIn("@sha256:", command[-2])
            self.assertTrue(command[-1].startswith("ghcr.io/owner/haptic:"))
            self.assertNotIn("--platform", command)
            self.assertNotIn("--force", command)

    def test_recovers_unlinked_discovery_metadata_by_digest(self):
        destination = FixtureRegistry()
        destination.content.pop(destination.signature)
        execute = Mock()
        copy_artifacts(self.row, self.registry, destination, execute)
        self.assertEqual(execute.call_count, 3)
        self.assertEqual(execute.call_args.args[0][-1], f"{destination.host}/{destination.name}")

    def test_inventory_is_restricted_to_all_haptic_components_and_immutable_objects(self):
        rows = [{**copy.deepcopy(self.row), "repository": name} for name in COMPONENTS.values()]
        validate_inventory(rows, "Owner")
        self.assertEqual(destination_name(rows[1], "Owner"), "owner/haptic/charts/haptic")
        for invalid in (rows[:-1], rows + [rows[0]], [{**rows[0], "repository": "foreign/project"}] + rows[1:]):
            with self.assertRaises(ValueError):
                validate_inventory(invalid, "Owner")
        for tag in ("latest", "main-sha", "0.5.0;touch-file", "0.5.0\n"):
            invalid = copy.deepcopy(rows)
            invalid[0]["roots"] = {tag: self.registry.root}
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                validate_inventory(invalid, "Owner")
        with self.assertRaises(ValueError):
            validate_inventory(rows, "Owner/other")

    def test_checked_in_inventory_preserves_all_published_release_artifacts(self):
        rows = json.loads((ROOT / ".github/release-registry-mirror.json").read_text())
        validate_inventory(rows, "HAProxy-Template-IC")
        self.assertEqual({row["repository"]: len(row["roots"]) for row in rows},
                         {COMPONENTS["controller"]: 98, COMPONENTS["chart"]: 22,
                          COMPONENTS["spoa"]: 9, COMPONENTS["varnish"]: 4})


class RegistryTransportTests(unittest.TestCase):
    def setUp(self):
        self.registry = Registry("ghcr.io", "owner/haptic")
        self.registry.token = "expired"
        self.registry.opener = Mock()

    def test_permission_errors_are_never_treated_as_missing_artifacts(self):
        for status in (401, 403, 500):
            self.registry.opener.open.side_effect = urllib.error.HTTPError("url", status, "failed", {}, io.BytesIO())
            self.registry.authenticate = Mock()
            with self.subTest(status=status), self.assertRaises(urllib.error.HTTPError):
                self.registry.read("manifests/tag")

    def test_only_not_found_means_missing(self):
        self.registry.opener.open.side_effect = urllib.error.HTTPError("url", 404, "missing", {}, io.BytesIO())
        self.assertIsNone(self.registry.read("manifests/tag"))

    def test_refreshes_expired_tokens_once(self):
        response = Mock()
        response.__enter__ = Mock(return_value=response)
        response.__exit__ = Mock(return_value=False)
        response.headers = {}
        response.read.return_value = b"{}"
        self.registry.opener.open.side_effect = [urllib.error.HTTPError("url", 401, "expired", {}, io.BytesIO()), response]
        self.registry.authenticate = Mock(side_effect=lambda: setattr(self.registry, "token", "renewed"))
        self.assertEqual(self.registry.read("manifests/tag"), (b"{}", {}))
        self.registry.authenticate.assert_called_once()
        request = self.registry.opener.open.call_args.args[0]
        self.assertEqual(request.headers["Authorization"], "Bearer renewed")

    def test_credentials_cannot_be_sent_to_another_registry(self):
        for host in ("registry.gitlab.com", "example.org"):
            registry = Registry(host, "owner/haptic", "actor", "credential")
            registry.opener = Mock()
            with self.assertRaises(ValueError):
                registry.authenticate()
            registry.opener.open.assert_not_called()

    def test_destination_creation_scope_is_limited_to_the_requested_package(self):
        for write, actions in ((False, "pull"), (True, "pull,push")):
            registry = Registry("ghcr.io", "owner/haptic", "actor", "credential", write=write)
            registry.opener = Mock()
            response = Mock()
            response.__enter__ = Mock(return_value=response)
            response.__exit__ = Mock(return_value=False)
            response.read.return_value = b'{"token":"token"}'
            registry.opener.open.return_value = response
            registry.authenticate()
            request = registry.opener.open.call_args.args[0]
            query = urllib.parse.parse_qs(urllib.parse.urlsplit(request.full_url).query)
            self.assertEqual(query["scope"], ["repository:owner/haptic:" + actions])

    def test_redirected_blob_bytes_are_hashed_without_forwarding_registry_credentials(self):
        digest = "sha256:" + hashlib.sha256(b"abc").hexdigest()
        response = Mock()
        response.__enter__ = Mock(return_value=response)
        response.__exit__ = Mock(return_value=False)
        response.read.side_effect = [b"a", b"bc", b""]
        self.registry.read = Mock(side_effect=urllib.error.HTTPError(
            "url", 307, "redirect", {"Location": "https://storage.example/blob"}, io.BytesIO()))
        self.registry.opener.open.return_value = response
        self.assertEqual(self.registry.blob(digest, 3), 3)
        request = self.registry.opener.open.call_args.args[0]
        self.assertNotIn("Authorization", request.headers)

    def test_redirected_blobs_reject_insecure_urls_and_wrong_bytes(self):
        digest = "sha256:" + hashlib.sha256(b"abc").hexdigest()
        for location in ("http://storage.example/blob", "https://actor:secret@storage.example/blob", "https:///blob"):
            with self.subTest(location=location), self.assertRaises(ValueError):
                self.registry.redirected_blob(location, digest, 3)
        self.registry.opener.open.assert_not_called()
        for content in (b"ab", b"abd", b"abcd"):
            response = Mock()
            response.__enter__ = Mock(return_value=response)
            response.__exit__ = Mock(return_value=False)
            response.read.side_effect = [content, b""]
            self.registry.opener.open.return_value = response
            with self.subTest(content=content), self.assertRaises(ValueError):
                self.registry.redirected_blob("https://storage.example/blob", digest, 3)


class ReleasedMirrorWorkflowTests(unittest.TestCase):
    def test_migration_copy_uses_standard_runners_and_only_package_writes(self):
        workflow = yaml.safe_load((ROOT / ".github/workflows/release-registry-mirror.yml").read_text())
        self.assertNotIn("schedule", workflow["on"])
        self.assertEqual(workflow["permissions"], {"contents": "read", "packages": "write"})
        job = workflow["jobs"]["mirror"]
        self.assertEqual(job["runs-on"], "ubuntu-24.04")
        self.assertEqual(set(job["strategy"]["matrix"]["component"]), set(COMPONENTS))
        self.assertFalse(workflow["concurrency"]["cancel-in-progress"])


if __name__ == "__main__":
    unittest.main()
