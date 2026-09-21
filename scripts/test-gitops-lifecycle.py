#!/usr/bin/env python3
"""Exercise chart lifecycle hooks through real Argo CD and Flux controllers."""

import argparse
import base64
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request

import yaml


ROOT = Path(__file__).resolve().parents[1]
ARGO_VERSION = "v3.5.3"
FLUX_VERSION = "v2.9.5"
CERT_MANAGER_VERSION = "v1.21.2"
KIND_NODE_IMAGE = "kindest/node:v1.33.0@sha256:02f73d6ae3f11ad5d543f16736a2cb2a63a300ad60e81dac22099b0b04784a4e"


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True).encode()).hexdigest()


def publication_matches(config, agents, secrets):
    metadata, status = config["metadata"], config.get("status", {})
    checksum = config.get("spec", {}).get("checksum")
    references = status.get("auxiliaryFiles", {})
    set_id = metadata.get("annotations", {}).get("haproxy-haptic.org/auxiliary-set-id")
    if not checksum or not set_id or references.get("setID") != set_id or len(agents) != 2:
        return False
    deployments = {item.get("podUID"): item for item in status.get("deployedToPods", [])}
    for uid, state in agents.items():
        deployed = deployments.get(uid, {})
        if (deployed.get("checksum") != checksum or deployed.get("lastError") or
                deployed.get("consecutiveErrors", 0) or not state.get("applied_plan_id") or
                not state.get("running_plan_id") or
                deployed.get("appliedPlanID") != state["applied_plan_id"] or
                deployed.get("runningPlanID") != state["running_plan_id"]):
            return False
    expected = {item["name"] for field in ["sslCertificates", "sslCaFiles"]
                for item in references.get(field, [])}
    owned = {secret["metadata"]["name"]: secret["metadata"] for secret in secrets
             if any(owner.get("uid") == metadata["uid"]
                    for owner in secret["metadata"].get("ownerReferences", []))}
    return expected == set(owned) and all(
        item.get("annotations", {}).get("haproxy-haptic.org/auxiliary-set-id") == set_id
        for item in owned.values())


class Lifecycle:
    def __init__(self, args):
        self.args = args
        self.cluster = args.cluster
        self.artifacts = Path(args.artifacts).resolve()
        self.kubeconfig = self.artifacts / "kubeconfig"
        self.env = dict(os.environ, KUBECONFIG=str(self.kubeconfig),
                        KIND_EXPERIMENTAL_DOCKER_NETWORK=self.cluster,
                        KIND_NODE_IMAGE=os.environ.get("KIND_NODE_IMAGE", KIND_NODE_IMAGE))
        self.created = False
        self.haproxy_version = yaml.safe_load((ROOT / "charts/haptic/values.yaml").read_text())["haproxyVersion"]
        self.repository = "http://chart-repository.gitops-test.svc.cluster.local:8080"
        self.versions = {}
        self.images = {}
        self.webhook_ca = ""

    def run(self, args, *, data=None, timeout=600):
        result = subprocess.run(args, input=data, capture_output=True, env=self.env,
                                cwd=ROOT, timeout=timeout, check=False)
        if result.returncode:
            raise RuntimeError(f"{' '.join(map(str, args))}: {result.stderr.decode()}\n{result.stdout.decode()}")
        return result.stdout

    def kubectl(self, *args, data=None):
        return self.run(["kubectl", "--kubeconfig", str(self.kubeconfig), *args], data=data)

    def get(self, kind, name=None, namespace="haptic"):
        args = ["-n", namespace, "get", kind]
        if name:
            args.append(name)
        return json.loads(self.kubectl(*args, "-o", "json"))

    def apply(self, obj):
        self.kubectl("apply", "--server-side", "--field-manager=haptic-gitops-test", "-f", "-",
                     data=json.dumps(obj).encode())

    def save(self, name, obj):
        (self.artifacts / name).write_text(json.dumps(obj, indent=2) + "\n")

    def poll(self, description, check, timeout=600):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if check():
                return
            time.sleep(2)
        raise RuntimeError(f"timed out waiting for {description}")

    def install_manifest(self, name, url, namespace=None, deployments=None):
        with urllib.request.urlopen(url, timeout=60) as response:
            content = response.read()
        self.save(name + "-source.json", {"url": url, "sha256": hashlib.sha256(content).hexdigest()})
        if deployments is not None:
            objects = [obj for obj in yaml.safe_load_all(content) if obj and
                       (obj.get("kind") != "Deployment" or obj["metadata"]["name"] in deployments)]
            content = yaml.safe_dump_all(objects).encode()
        args = (["-n", namespace] if namespace else []) + ["apply", "--server-side", "-f", "-"]
        self.kubectl(*args, data=content)

    def rollout(self, namespace, resource):
        self.kubectl("-n", namespace, "rollout", "status", resource, "--timeout=7m")

    def setup(self):
        if not re.fullmatch(r"haptic-gitops-[a-z0-9-]+", self.cluster):
            raise ValueError("cluster name must start with haptic-gitops-")
        if self.cluster in self.run(["kind", "get", "clusters"]).decode().splitlines():
            raise ValueError(f"cluster {self.cluster} already exists; use a fresh name")
        if self.cluster in self.run(["docker", "network", "ls", "--format", "{{.Name}}"]).decode().splitlines():
            raise ValueError(f"network {self.cluster} already exists; use a fresh name")
        self.artifacts.mkdir(parents=True, mode=0o700, exist_ok=False)
        print(f"Creating fresh cluster {self.cluster}", flush=True)
        self.created = True
        self.run(["bash", "-euc", 'source scripts/lib/cluster.sh; kind_create_cluster "$1"', "bash", self.cluster])
        for namespace in ["haptic", "cert-manager", "gitops-test", "argocd"]:
            self.apply({"apiVersion": "v1", "kind": "Namespace", "metadata": {"name": namespace}})
        if self.args.certificates == "cert-manager":
            self.install_manifest("cert-manager",
                                  f"https://github.com/cert-manager/cert-manager/releases/download/{CERT_MANAGER_VERSION}/cert-manager.yaml")
            for deployment in ["cert-manager", "cert-manager-webhook", "cert-manager-cainjector"]:
                self.rollout("cert-manager", "deployment/" + deployment)
        else:
            self.external_certificates()
        self.prepare_charts()
        self.apply({"apiVersion": "v1", "kind": "Secret", "metadata": {
            "name": "gitops-credentials", "namespace": "haptic"}, "stringData": {
                "dataplane_username": "admin", "dataplane_password": "gitops-lifecycle-fixture-only"}})
        if self.args.provider == "argo":
            self.install_argo()
        else:
            self.install_flux()

    def external_certificates(self):
        with tempfile.TemporaryDirectory(prefix="haptic-gitops-certificates-") as directory:
            work = Path(directory)
            ca, key = work / "ca.crt", work / "ca.key"
            self.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "365",
                      "-subj", "/CN=HAPTIC GitOps test CA", "-addext", "basicConstraints=critical,CA:TRUE",
                      "-keyout", str(key), "-out", str(ca)])
            self.webhook_ca = base64.b64encode(ca.read_bytes()).decode()
            for name, dns_names in [("gitops-webhook", ["haptic-webhook.haptic.svc"]),
                                    ("upgrade-default-tls", ["tls.upgrade.test"])]:
                cert, private, csr, extensions = [work / (name + suffix) for suffix in [".crt", ".key", ".csr", ".ext"]]
                self.run(["openssl", "req", "-new", "-newkey", "rsa:2048", "-nodes", "-subj", "/CN=" + dns_names[0],
                          "-keyout", str(private), "-out", str(csr)])
                extensions.write_text("basicConstraints=critical,CA:FALSE\nextendedKeyUsage=serverAuth\nsubjectAltName=" +
                                      ",".join("DNS:" + name for name in dns_names) + "\n")
                self.run(["openssl", "x509", "-req", "-in", str(csr), "-CA", str(ca), "-CAkey", str(key),
                          "-CAcreateserial", "-days", "90", "-extfile", str(extensions), "-out", str(cert)])
                self.apply({"apiVersion": "v1", "kind": "Secret", "metadata": {"name": name, "namespace": "haptic"},
                    "type": "kubernetes.io/tls", "data": {"tls.crt": base64.b64encode(cert.read_bytes()).decode(),
                    "tls.key": base64.b64encode(private.read_bytes()).decode(), "ca.crt": self.webhook_ca}})

    def prepare_charts(self):
        image_id = self.run(["docker", "image", "inspect", self.args.image, "--format", "{{.Id}}"]).decode().strip()
        version_output = self.run(["docker", "run", "--rm", "--entrypoint", "haptic", self.args.image, "version"]).decode()
        source_hash = self.run(["bash", "scripts/source-hash.sh"]).decode().strip()
        if f"Source Hash: {source_hash}" not in version_output:
            raise ValueError(f"image source does not match the worktree: {version_output}")
        provenance = {"commit": self.run(["git", "rev-parse", "HEAD"]).decode().strip(),
                      "dirty": bool(self.run(["git", "status", "--porcelain"])),
                      "sourceHash": source_hash, "baseImageID": image_id,
                      "versionOutput": version_output, "provider": self.args.provider, "certificates": self.args.certificates,
                      "providerVersion": ARGO_VERSION if self.args.provider == "argo" else FLUX_VERSION,
                      "kubernetes": json.loads(self.kubectl("version", "-o", "json"))["serverVersion"],
                      "charts": {}}
        repository = self.artifacts / "repository"
        repository.mkdir()
        for number, phase in enumerate(["installed", "upgraded", "rejected", "recovered"], 1):
            chart = self.artifacts / (phase + "-chart")
            shutil.copytree(ROOT / "charts/haptic", chart)
            version = f"0.0.0-gitops.{number}"
            metadata = chart / "Chart.yaml"
            metadata.write_text(re.sub(r"^version:.*$", "version: " + version, metadata.read_text(), flags=re.M))
            if phase == "rejected":
                library = chart / "charts/base/library.yaml"
                source = library.read_text()
                match = re.search(r"^haproxyConfig:\n(\s+)template: \|\n", source, re.M)
                if not match:
                    raise ValueError("base library has no haproxyConfig.template to corrupt")
                library.write_text(source[:match.end()] + match.group(1) + "  {%- var x = %}\n" + source[match.end():])
            image_tag = self.cluster + "-" + phase
            image = "haptic:" + image_tag + "-haproxy" + str(self.haproxy_version)
            dockerfile = "ARG BASE_IMAGE\nFROM ${BASE_IMAGE}\nUSER root\nRUN rm -rf /usr/share/haptic/chart\nCOPY . /usr/share/haptic/chart/\nUSER haproxy\n"
            self.run(["docker", "build", "--build-arg", "BASE_IMAGE=" + self.args.image,
                      "-t", image, "-f", "-", str(chart)], data=dockerfile.encode())
            self.run(["kind", "load", "docker-image", image, "--name", self.cluster])
            self.run(["helm", "package", str(chart), "--destination", str(repository)])
            archive = repository / ("haptic-" + version + ".tgz")
            provenance["charts"][phase] = {"version": version,
                "sha256": hashlib.sha256(archive.read_bytes()).hexdigest(),
                "imageID": self.run(["docker", "image", "inspect", image, "--format", "{{.Id}}"]).decode().strip()}
            self.versions[phase], self.images[phase] = version, image_tag
        self.save("provenance.json", provenance)
        self.run(["helm", "repo", "index", str(repository), "--url", self.repository])
        self.apply({"apiVersion": "v1", "kind": "Pod", "metadata": {"name": "chart-repository",
            "namespace": "gitops-test", "labels": {"app": "chart-repository"}}, "spec": {"containers": [{
                "name": "http", "image": "busybox:1.37.0", "command": ["sh", "-c",
                    "mkdir -p /www; exec httpd -f -p 8080 -h /www"], "ports": [{"containerPort": 8080}]}]}})
        self.apply({"apiVersion": "v1", "kind": "Service", "metadata": {"name": "chart-repository",
            "namespace": "gitops-test"}, "spec": {"selector": {"app": "chart-repository"},
            "ports": [{"port": 8080, "targetPort": 8080}]}})
        self.kubectl("-n", "gitops-test", "wait", "--for=condition=Ready", "pod/chart-repository", "--timeout=180s")
        archive = io.BytesIO()
        with tarfile.open(fileobj=archive, mode="w") as output:
            for path in repository.iterdir():
                output.add(path, arcname=path.name)
        self.kubectl("-n", "gitops-test", "exec", "-i", "chart-repository", "--", "tar", "xf", "-", "-C", "/www",
                     data=archive.getvalue())

    def install_argo(self):
        self.install_manifest("argocd", f"https://raw.githubusercontent.com/argoproj/argo-cd/{ARGO_VERSION}/manifests/core-install.yaml", "argocd")
        self.rollout("argocd", "deployment/argocd-repo-server")
        self.rollout("argocd", "statefulset/argocd-application-controller")
        self.apply({"apiVersion": "argoproj.io/v1alpha1", "kind": "AppProject", "metadata": {
            "name": "haptic-test", "namespace": "argocd"}, "spec": {"sourceRepos": [self.repository],
            "destinations": [{"namespace": "haptic", "server": "https://kubernetes.default.svc"}],
            "clusterResourceWhitelist": [{"group": "*", "kind": "*"}]}})

    def install_flux(self):
        self.install_manifest("flux", f"https://github.com/fluxcd/flux2/releases/download/{FLUX_VERSION}/install.yaml",
                              deployments=["source-controller", "helm-controller"])
        for deployment in ["source-controller", "helm-controller"]:
            self.rollout("flux-system", "deployment/" + deployment)
        self.apply({"apiVersion": "source.toolkit.fluxcd.io/v1", "kind": "HelmRepository", "metadata": {
            "name": "haptic", "namespace": "flux-system"}, "spec": {"interval": "1m", "url": self.repository}})

    def values(self, phase):
        values = yaml.safe_load((ROOT / "scripts/testdata/chart-upgrade/values.yaml").read_text())
        values["credentials"] = {"existingSecret": "gitops-credentials"}
        values["controller"].update({"image": {"repository": "haptic", "tag": self.images[phase]},
            "webhook": {"certManager": {"enabled": True}}, "resources": {"limits": {"cpu": 4}}})
        if self.args.certificates == "external":
            values["controller"]["webhook"] = {"secretName": "gitops-webhook", "caBundle": self.webhook_ca,
                                               "certManager": {"enabled": False}}
            values["defaultSSLCertificate"]["certManager"] = {"enabled": False}
        return values

    def desired(self, phase):
        version = self.versions[phase]
        if self.args.provider == "argo":
            return {"apiVersion": "argoproj.io/v1alpha1", "kind": "Application", "metadata": {
                "name": "haptic", "namespace": "argocd"}, "spec": {"project": "haptic-test",
                "destination": {"namespace": "haptic", "server": "https://kubernetes.default.svc"},
                "source": {"repoURL": self.repository, "chart": "haptic", "targetRevision": version,
                           "helm": {"releaseName": "haptic", "valuesObject": self.values(phase)}},
                "syncPolicy": {"automated": {"enabled": True, "prune": True, "selfHeal": True},
                               "retry": {"limit": 1},
                               "syncOptions": ["ServerSideApply=true", "DisableClientSideApplyMigration=true"]}}}
        return {"apiVersion": "helm.toolkit.fluxcd.io/v2", "kind": "HelmRelease", "metadata": {
            "name": "haptic", "namespace": "flux-system"}, "spec": {"interval": "1m", "timeout": "10m",
            "releaseName": "haptic", "targetNamespace": "haptic", "chart": {"spec": {"chart": "haptic",
            "version": version, "sourceRef": {"kind": "HelmRepository", "name": "haptic"}}},
            "install": {"strategy": {"name": "RetryOnFailure", "retryInterval": "1m"}},
            "upgrade": {"strategy": {"name": "RetryOnFailure", "retryInterval": "1m"}},
            "values": self.values(phase)}}

    def state(self):
        return self.get("application", "haptic", "argocd") if self.args.provider == "argo" else self.get("helmrelease", "haptic", "flux-system")

    def sync(self, phase, *, repeat=False):
        print(f"{self.args.provider}: {phase}{' repeat sync' if repeat else ''}", flush=True)
        self.apply(self.desired(phase))
        state = self.state()
        old_operation = state.get("status", {}).get("operationState", {}).get("startedAt")
        request = str(time.time_ns())
        if repeat and self.args.provider == "argo":
            self.kubectl("-n", "argocd", "patch", "application", "haptic", "--type=merge", "-p", json.dumps({
                "operation": {"initiatedBy": {"username": "haptic-lifecycle-test"}, "sync": {"prune": True,
                    "syncOptions": ["ServerSideApply=true", "DisableClientSideApplyMigration=true"]}}}))
        elif self.args.provider == "flux":
            self.kubectl("-n", "flux-system", "annotate", "helmrelease", "haptic",
                         "reconcile.fluxcd.io/requestedAt=" + request, "--overwrite")

        def finished():
            current = self.state()
            self.save(phase + ("-repeat" if repeat else "") + "-gitops.json", current)
            status = current.get("status", {})
            if self.args.provider == "argo":
                operation = status.get("operationState", {})
                result = operation.get("syncResult", {})
                if result.get("revision") != self.versions[phase] or (repeat and operation.get("startedAt") == old_operation):
                    return False
                outcome = operation.get("phase")
                if phase == "rejected":
                    return outcome in ["Failed", "Error"]
                if outcome in ["Failed", "Error"]:
                    raise RuntimeError(operation.get("message", "Argo operation failed"))
                return outcome == "Succeeded" and status.get("sync", {}).get("status") == "Synced" and status.get("health", {}).get("status") == "Healthy"
            if status.get("lastAttemptedRevision") != self.versions[phase]:
                return False
            conditions = [condition for condition in status.get("conditions", [])
                          if condition.get("observedGeneration") == current["metadata"]["generation"]]
            if phase == "rejected":
                return any(c["type"] == "Released" and c["status"] == "False" and c.get("reason") == "UpgradeFailed" for c in conditions)
            return status.get("lastHandledReconcileAt") == request and any(c["type"] == "Ready" and c["status"] == "True" for c in conditions)

        self.poll(phase + " GitOps result", finished)

    def snapshot(self, phase):
        objects = []
        for kind in ["haproxytemplateconfig", "haproxytemplatelibrary"]:
            objects.extend((item["kind"], item["metadata"]["name"], item["spec"]) for item in self.get(kind)["items"])
        pods = [pod for pod in self.get("pods")["items"] if pod["metadata"].get("labels", {}).get(
            "app.kubernetes.io/component") in ["controller", "loadbalancer"] and not pod["metadata"].get("deletionTimestamp")]
        if len(pods) != 4:
            raise RuntimeError(f"expected two controller and two HAProxy pods, found {len(pods)}")
        for pod in pods:
            for container in pod.get("status", {}).get("containerStatuses", []) + pod.get("status", {}).get("initContainerStatuses", []):
                if container.get("restartCount"):
                    raise RuntimeError(f"{pod['metadata']['name']}/{container['name']} restarted")
        secret_hashes = {secret["metadata"]["name"]: digest(secret.get("data", {})) for secret in self.get("secrets")["items"]
                         if not secret["metadata"]["name"].startswith("sh.helm.release.") and not secret["metadata"]["name"].endswith("-pre-rollout-values")}
        snapshot = {"config": digest(sorted(objects)), "pods": sorted(pod["metadata"]["uid"] for pod in pods), "secrets": secret_hashes}
        self.save(phase + "-snapshot.json", snapshot)
        return snapshot

    def verify_traffic(self, phase):
        self.rollout("haptic", "deployment/haptic-controller")
        script = '''
set -euo pipefail
source scripts/lib/cluster.sh
source scripts/lib/upgrade-traffic.sh
NS=haptic
k() { kubectl --context "$CTX" -n "$NS" "$@"; }
fail() { echo "$*" >&2; exit 1; }
info() { echo "$*"; }
[ "$(wait_config_validated 180)" = True ]
wait_upgrade_traffic "$PHASE"
'''
        work = self.artifacts / "traffic"
        work.mkdir(exist_ok=True)
        previous = self.env
        self.env = dict(previous, CTX="kind-" + self.cluster, WORK=str(work),
                        ARTIFACTS=str(self.artifacts), PHASE=phase)
        try:
            output = self.run(["bash", "-c", script])
            (self.artifacts / (phase + "-traffic.log")).write_bytes(output)
        finally:
            self.env = previous
        self.wait_publication(phase)

    def wait_publication(self, phase):
        # Serving traffic can precede throttled CRD publication and auxiliary cleanup.
        def finished():
            agents = {}
            try:
                for pod in self.get("pods")["items"]:
                    metadata = pod["metadata"]
                    if (metadata.get("labels", {}).get("app.kubernetes.io/component") != "loadbalancer" or
                            metadata.get("deletionTimestamp")):
                        continue
                    state = json.loads(self.kubectl("-n", "haptic", "exec", metadata["name"], "-c", "agent", "--",
                        "haptic", "agent", "state", "--output", "json", "--verify"))
                    agents[metadata["uid"]] = {key: state.get(key) for key in ["applied_plan_id", "running_plan_id"]}
                config = self.get("haproxycfg", "haptic-config-haproxycfg")
                secrets = self.get("secrets")["items"]
            except RuntimeError:
                self.save(phase + "-publication.json", {"ready": False, "observation_failed": True})
                return False
            ready = publication_matches(config, agents, secrets)
            self.save(phase + "-publication.json", {"ready": ready, "agents": agents,
                "config_resource_version": config["metadata"]["resourceVersion"],
                "checksum": config.get("spec", {}).get("checksum"),
                "auxiliary": config.get("status", {}).get("auxiliaryFiles", {})})
            return ready

        self.poll(phase + " publication and certificate cleanup", finished, timeout=180)

    def exercise(self):
        self.sync("installed")
        self.kubectl("-n", "haptic", "apply", "-f", "scripts/testdata/chart-upgrade/routes.yaml")
        self.rollout("haptic", "deployment/upgrade-backend")
        self.verify_traffic("installed")
        initial = self.snapshot("installed")
        self.sync("installed", repeat=True)
        self.verify_traffic("repeat")
        if self.snapshot("repeat") != initial:
            raise RuntimeError("unchanged sync replaced pods, rotated Secrets, or changed configuration")
        self.sync("upgraded")
        self.verify_traffic("upgraded")
        upgraded = self.snapshot("upgraded")
        if initial["secrets"] != upgraded["secrets"]:
            raise RuntimeError("chart upgrade rotated existing credentials or certificates")
        if initial["pods"] == upgraded["pods"]:
            raise RuntimeError("chart upgrade did not replace any workload pods")
        self.sync("rejected")
        jobs = [job for job in self.get("jobs")["items"] if job["metadata"]["name"].endswith("-pre-rollout")]
        if len(jobs) != 1 or not jobs[0].get("status", {}).get("failed", 0):
            raise RuntimeError("rejection did not come from the pre-rollout validation hook")
        (self.artifacts / "rejected-preflight.log").write_bytes(self.kubectl(
            "-n", "haptic", "logs", "job/" + jobs[0]["metadata"]["name"]))
        if self.snapshot("rejected") != upgraded:
            raise RuntimeError("rejected candidate changed the serving configuration, pods, or Secrets")
        self.verify_traffic("rejected")
        self.sync("recovered")
        self.verify_traffic("recovered")
        recovered = self.snapshot("recovered")
        if recovered["secrets"] != initial["secrets"]:
            raise RuntimeError("recovery rotated credentials or certificates")
        self.save("result.json", {"passed": True, "provider": self.args.provider,
                                 "phases": ["install", "repeat", "upgrade", "rejection", "recovery"]})
        print(f"PASS: {self.args.provider} install, stable sync, upgrade, rejection, and recovery", flush=True)

    def cleanup(self):
        if not self.created:
            return
        for resource in ["pods", "jobs"]:
            try:
                self.save("final-" + resource + ".json", self.get(resource))
            except RuntimeError as error:
                (self.artifacts / ("final-" + resource + "-error.txt")).write_text(str(error))
        if self.args.keep:
            print(f"Kept owned cluster {self.cluster}; kubeconfig {self.kubeconfig}", flush=True)
        else:
            self.run(["kind", "delete", "cluster", "--name", self.cluster])
            networks = self.run(["docker", "network", "ls", "--format", "{{.Name}}"]).decode().splitlines()
            if self.cluster in networks:
                self.run(["docker", "network", "rm", self.cluster])
            self.kubeconfig.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--provider", required=True, choices=["argo", "flux"])
    parser.add_argument("--image", default="haptic:test")
    parser.add_argument("--certificates", choices=["external", "cert-manager"], default="external")
    parser.add_argument("--cluster", required=True)
    parser.add_argument("--artifacts", required=True)
    parser.add_argument("--keep", action="store_true")
    args = parser.parse_args()
    os.umask(0o077)
    lifecycle = Lifecycle(args)
    try:
        lifecycle.setup()
        lifecycle.exercise()
    finally:
        failed = sys.exc_info()[0] is not None
        try:
            lifecycle.cleanup()
        except Exception as error:
            if not failed:
                raise
            print(f"Cleanup failed: {error}", file=sys.stderr)


if __name__ == "__main__":
    main()
