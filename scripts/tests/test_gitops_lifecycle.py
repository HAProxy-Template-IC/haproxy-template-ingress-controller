"""GitOps snapshots require completed publication, including certificate cleanup."""

import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import unittest
from unittest.mock import Mock, patch


SCRIPT = Path(__file__).resolve().parents[1] / "test-gitops-lifecycle.py"
SPEC = importlib.util.spec_from_file_location("gitops_lifecycle", SCRIPT)
LIFECYCLE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(LIFECYCLE)


class PublicationTest(unittest.TestCase):
    def setUp(self):
        self.config = {
            "metadata": {"uid": "config-uid", "resourceVersion": "123",
                "annotations": {"haproxy-haptic.org/auxiliary-set-id": "current"}},
            "spec": {"checksum": "current-checksum"},
            "status": {
                "auxiliaryFiles": {"setID": "current", "sslCertificates": [{"name": "certificate"}]},
                "deployedToPods": [
                    {"podUID": uid, "checksum": "current-checksum", "appliedPlanID": "applied", "runningPlanID": "running"}
                    for uid in ["pod-a", "pod-b"]
                ],
            },
        }
        self.agents = {uid: {"applied_plan_id": "applied", "running_plan_id": "running"} for uid in ["pod-a", "pod-b"]}
        self.secrets = [{"metadata": {"name": "certificate", "ownerReferences": [{"uid": "config-uid"}],
            "annotations": {"haproxy-haptic.org/auxiliary-set-id": "current"}}}]

    def ready(self):
        return LIFECYCLE.publication_matches(self.config, self.agents, self.secrets)

    def test_completed_publication(self):
        self.secrets.append({"metadata": {"name": "unowned-credentials"}})
        self.assertTrue(self.ready())

    def test_serving_new_plan_before_publication(self):
        self.agents["pod-a"]["applied_plan_id"] = "new-plan"
        self.assertFalse(self.ready())

    def test_pending_reload_proof(self):
        self.agents["pod-b"]["running_plan_id"] = "new-running-plan"
        self.assertFalse(self.ready())

    def test_replaced_pod(self):
        self.agents["replacement"] = self.agents.pop("pod-b")
        self.assertFalse(self.ready())

    def test_partial_fleet(self):
        del self.agents["pod-b"]
        self.assertFalse(self.ready())

    def test_stale_auxiliary_references(self):
        self.config["status"]["auxiliaryFiles"]["setID"] = "previous"
        self.assertFalse(self.ready())

    def test_pending_certificate_creation(self):
        self.secrets.clear()
        self.assertFalse(self.ready())

    def test_pending_certificate_cleanup(self):
        previous = copy.deepcopy(self.secrets[0])
        previous["metadata"]["name"] = "previous-certificate"
        self.secrets.append(previous)
        self.assertFalse(self.ready())

    def test_stale_certificate_publication(self):
        self.secrets[0]["metadata"]["annotations"]["haproxy-haptic.org/auxiliary-set-id"] = "previous"
        self.assertFalse(self.ready())

    def content_publication(self, ca_file=False):
        set_id = "content-sha256:current"
        self.config["metadata"]["annotations"]["haproxy-haptic.org/auxiliary-set-id"] = set_id
        metadata = self.secrets[0]["metadata"]
        checksum = "sha256:" + hashlib.sha256(b"certificate").hexdigest()
        metadata["annotations"] = {"haproxy-haptic.org/auxiliary-path": "/certs/site.pem",
            "haproxy-haptic.org/checksum": checksum, "haproxy-haptic.org/auxiliary-claim": "retained"}
        metadata["labels"] = {"haproxy-haptic.org/type": "ssl-ca" if ca_file else "ssl-certificate"}
        identity = f"Secret\0/certs/site.pem\0{checksum}\0{str(ca_file).lower()}"
        metadata["name"] = "certificate-content-" + hashlib.sha256(identity.encode()).hexdigest()
        field = "sslCaFiles" if ca_file else "sslCertificates"
        self.config["status"]["auxiliaryFiles"] = {"setID": set_id, field: [{"name": metadata["name"]}]}
        return metadata

    def test_reused_content_publication(self):
        for ca_file in [False, True]:
            with self.subTest(ca_file=ca_file):
                self.content_publication(ca_file)
                self.assertTrue(self.ready())

    def test_content_publication_rejects_changed_identity(self):
        for annotation in ["auxiliary-path", "checksum"]:
            with self.subTest(annotation=annotation):
                metadata = self.content_publication()
                metadata["annotations"]["haproxy-haptic.org/" + annotation] = "different"
                self.assertFalse(self.ready())
                del metadata["annotations"]["haproxy-haptic.org/" + annotation]
                self.assertFalse(self.ready())

    def test_content_publication_rejects_wrong_role(self):
        metadata = self.content_publication()
        metadata["labels"]["haproxy-haptic.org/type"] = "ssl-ca"
        self.assertFalse(self.ready())
        references = self.config["status"]["auxiliaryFiles"]
        references["sslCaFiles"] = references.pop("sslCertificates")
        self.assertFalse(self.ready())

    def test_content_publication_rejects_pending_cleanup(self):
        self.content_publication()
        previous = copy.deepcopy(self.secrets[0])
        previous["metadata"]["name"] = "previous-certificate"
        self.secrets.append(previous)
        self.assertFalse(self.ready())

    def test_content_publication_rejects_missing_or_terminating_certificate(self):
        metadata = self.content_publication()
        metadata["deletionTimestamp"] = "2026-10-06T22:00:00Z"
        self.assertFalse(self.ready())
        self.secrets.clear()
        self.assertFalse(self.ready())

    def test_content_publication_with_publication_suffix(self):
        metadata = self.content_publication()
        metadata["name"] += "-publication"
        self.config["status"]["auxiliaryFiles"]["sslCertificates"][0]["name"] = metadata["name"]
        self.assertTrue(self.ready())

    def lifecycle(self):
        lifecycle = object.__new__(LIFECYCLE.Lifecycle)
        lifecycle.get = Mock(side_effect=lambda kind, *args: {
            "pods": {"items": [{"metadata": {"name": uid, "uid": uid, "labels": {
                "app.kubernetes.io/component": "loadbalancer"}}} for uid in self.agents]},
            "haproxycfg": self.config, "secrets": {"items": self.secrets},
        }[kind])
        lifecycle.kubectl = Mock(side_effect=lambda *args: json.dumps(self.agents[args[3]]))
        lifecycle.save = Mock()
        return lifecycle

    def test_retries_resource_observation(self):
        for operation in ["exec", "pods", "haproxycfg", "secrets"]:
            with self.subTest(operation=operation):
                lifecycle = self.lifecycle()
                call = lifecycle.kubectl if operation == "exec" else lifecycle.get
                original = call.side_effect
                failed = False

                def transient(*args):
                    nonlocal failed
                    if not failed and (operation == "exec" or args[0] == operation):
                        failed = True
                        raise RuntimeError("resource disappeared")
                    return original(*args)

                call.side_effect = transient
                with patch.object(LIFECYCLE.time, "sleep") as sleep:
                    lifecycle.wait_publication("upgraded")
                sleep.assert_called_once_with(2)
                self.assertTrue(lifecycle.save.call_args.args[1]["ready"])
                self.assertEqual(lifecycle.save.call_args_list[0].args[1],
                    {"ready": False, "observation_failed": True})

    def test_persistent_observation_failure_times_out(self):
        lifecycle = self.lifecycle()
        lifecycle.kubectl.side_effect = RuntimeError("agent unavailable")
        with patch.object(LIFECYCLE.time, "monotonic", side_effect=[0, 0, 181]), \
                patch.object(LIFECYCLE.time, "sleep"):
            with self.assertRaisesRegex(RuntimeError, "timed out waiting for upgraded publication"):
                lifecycle.wait_publication("upgraded")

    def test_invalid_agent_state_is_fatal(self):
        lifecycle = self.lifecycle()
        lifecycle.kubectl.return_value = b"invalid JSON"
        lifecycle.kubectl.side_effect = None
        with self.assertRaises(json.JSONDecodeError):
            lifecycle.wait_publication("upgraded")


if __name__ == "__main__":
    unittest.main()
