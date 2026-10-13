"""Retain verified owner identities without downloading retained configuration bodies."""

import json
from pathlib import Path
import re
import signal
import subprocess
import sys
import time
import urllib.parse
import urllib.request


RESOURCE = "/apis/haproxy-haptic.org/v1alpha1/haproxycfgs"
ACCEPT = "application/json;as=PartialObjectMetadata;g=meta.k8s.io;v=v1"
LIST_ACCEPT = "application/json;as=PartialObjectMetadataList;g=meta.k8s.io;v=v1"


def record_identity(item, output):
    if item.get("apiVersion") != "meta.k8s.io/v1" or item.get("kind") != "PartialObjectMetadata":
        raise ValueError("Expected metadata-only HAProxyCfg response")
    metadata = item["metadata"]
    identity = {key: metadata[key] for key in ("namespace", "name", "uid")}
    if not all(isinstance(value, str) and value for value in identity.values()):
        raise ValueError("HAProxyCfg identity is incomplete")
    output.write(json.dumps({"apiVersion": "haproxy-haptic.org/v1alpha1", "kind": "HAProxyCfg",
                             "metadata": identity}) + "\n")
    output.flush()


def observe(base, output, fetch=urllib.request.urlopen, ready=lambda: None):
    def request(query=""):
        accept = ACCEPT if query else LIST_ACCEPT
        return fetch(urllib.request.Request(base + RESOURCE + query, headers={"Accept": accept}), timeout=45)

    with request() as response:
        document = json.load(response)
    if document.get("apiVersion") != "meta.k8s.io/v1" or document.get("kind") != "PartialObjectMetadataList":
        raise ValueError("Expected metadata-only HAProxyCfg list")
    for item in document["items"]:
        record_identity(item, output)
    revision = document["metadata"]["resourceVersion"]
    ready()
    while True:
        query = urllib.parse.urlencode({"watch": "true", "allowWatchBookmarks": "true",
                                        "resourceVersion": revision, "timeoutSeconds": 30})
        with request("?" + query) as response:
            for line in response:
                event = json.loads(line)
                if event["type"] not in {"ADDED", "MODIFIED", "DELETED", "BOOKMARK"}:
                    raise ValueError("HAProxyCfg identity watch returned an invalid event")
                item = event["object"]
                revision = item["metadata"]["resourceVersion"]
                if event["type"] != "BOOKMARK":
                    record_identity(item, output)


def proxy_address(process, log):
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError("Metadata proxy exited before becoming ready")
        match = re.search(r"Starting to serve on 127\.0\.0\.1:(\d+)", log.read_text())
        if match:
            return "http://127.0.0.1:" + match[1]
        time.sleep(0.1)
    raise RuntimeError("Metadata proxy did not become ready")


def stop(_signal, _frame):
    raise SystemExit(0)


def main():
    root = Path(sys.argv[1])
    signal.signal(signal.SIGTERM, stop)
    history = root / "runtime-config-history.jsonl"
    log = root / "runtime-config-proxy.log"
    with history.open("a") as output, log.open("w") as proxy_log:
        history.chmod(0o600)
        process = subprocess.Popen(["kubectl", "proxy", "--address=127.0.0.1", "--port=0"],
                                   stdout=proxy_log, stderr=subprocess.STDOUT)
        try:
            observe(proxy_address(process, log), output,
                    ready=lambda: (root / "runtime-config-history.ready").touch())
        finally:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()


if __name__ == "__main__":
    main()
