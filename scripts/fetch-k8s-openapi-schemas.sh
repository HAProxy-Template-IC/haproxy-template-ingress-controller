#!/bin/bash
# Fetch K8s built-in resource schemas from a running cluster's OpenAPI v3
# endpoints and emit CRD-wrapped YAML files under tests/schemas/ so the
# offline `controller validate` path can resolve typed access for chart
# templates that reach `resources.namespaces`, `resources.services`, etc.
#
# Why this exists: DirFetcher's offline GVK resolver only picks up
# (apiVersion, plural) → GVK mappings from full CRD-shaped files (see
# pkg/k8s/schemafetcher/dir_fetcher.go::PluralsFor). Bare OpenAPI v3
# schemas with x-kubernetes-group-version-kind work for typed lookup
# but don't register a plural, so chart configs declaring
# `watchedResources.<X>: { apiVersion: v1, resources: <X> }` can't
# resolve their GVK without the CRD wrapper. This script bridges the
# gap for K8s built-ins (Namespace, Service, Secret, EndpointSlice,
# Ingress) which aren't CRDs but need the same offline-resolvable
# shape.
#
# Inputs: a kube context with the resources we care about installed.
# kind clusters (`kind-haptic-dev`, `kind-haptic-e2e`) are fine — the
# OpenAPI surface is identical across vanilla apiserver builds.
#
# Outputs: one file per resource under tests/schemas/, named
# `<group>_<version>_<resourcePlural>.yaml`. Existing files are
# overwritten — re-run on apiserver upgrades to track schema changes.

set -euo pipefail

CONTEXT="${KUBE_CONTEXT:-kind-haptic-e2e}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SCHEMA_DIR="$REPO_ROOT/tests/schemas"

# Resources to fetch: (file-prefix, openapi-endpoint, schema-name,
# group, plural, kind, scope). One entry per chart-watched K8s built-in.
RESOURCES=(
  "core_v1_namespaces:/openapi/v3/api/v1:io.k8s.api.core.v1.Namespace::namespaces:Namespace:Cluster"
  "core_v1_services:/openapi/v3/api/v1:io.k8s.api.core.v1.Service::services:Service:Namespaced"
  "core_v1_secrets:/openapi/v3/api/v1:io.k8s.api.core.v1.Secret::secrets:Secret:Namespaced"
  "core_v1_configmaps:/openapi/v3/api/v1:io.k8s.api.core.v1.ConfigMap::configmaps:ConfigMap:Namespaced"
  "networking_k8s_io_v1_ingresses:/openapi/v3/apis/networking.k8s.io/v1:io.k8s.api.networking.v1.Ingress:networking.k8s.io:ingresses:Ingress:Namespaced"
  "discovery_k8s_io_v1_endpointslices:/openapi/v3/apis/discovery.k8s.io/v1:io.k8s.api.discovery.v1.EndpointSlice:discovery.k8s.io:endpointslices:EndpointSlice:Namespaced"
)

mkdir -p "$SCHEMA_DIR"

# Fetch the OpenAPI v3 docs once per endpoint (cached in /tmp)
declare -A ENDPOINT_DATA
for r in "${RESOURCES[@]}"; do
  IFS=':' read -r prefix endpoint schema_name group plural kind scope <<<"$r"
  if [[ -z "${ENDPOINT_DATA[$endpoint]:-}" ]]; then
    tmp="/tmp/oapi-$(echo "$endpoint" | tr '/' '-').json"
    echo "fetching $endpoint → $tmp"
    kubectl --context "$CONTEXT" get --raw "$endpoint" > "$tmp"
    ENDPOINT_DATA[$endpoint]="$tmp"
  fi
done

# Use Python to resolve $refs inline and emit a CRD-wrapped YAML per
# resource. The resolver walks the openapi components dict and inlines
# every #/components/schemas/<name> reference recursively. Cycle
# detection avoids infinite recursion on self-referential schemas
# (ObjectMeta has none, but the helper is generic so we're safe).
python3 - "$SCHEMA_DIR" "${RESOURCES[@]}" <<'PY'
import json, sys, os, subprocess, tempfile

schema_dir = sys.argv[1]
entries = sys.argv[2:]

# Load all endpoint docs (deduped). Each endpoint may serve multiple
# resources; we resolve $refs against the endpoint's own components map
# so cross-endpoint refs are flattened lazily (Namespace and Service
# both live under /api/v1 so they share the same components).
endpoints = {}

def load_endpoint(endpoint):
    if endpoint in endpoints:
        return endpoints[endpoint]
    tmp = "/tmp/oapi-" + endpoint.replace("/", "-") + ".json"
    with open(tmp) as f:
        endpoints[endpoint] = json.load(f)
    return endpoints[endpoint]

def resolve(node, components, seen=None):
    """Inline every $ref under node by walking the components dict.
    seen is a per-path cycle guard so a self-referential schema
    doesn't blow the stack."""
    if seen is None:
        seen = set()
    if isinstance(node, dict):
        if "$ref" in node and node["$ref"].startswith("#/components/schemas/"):
            ref_name = node["$ref"][len("#/components/schemas/"):]
            if ref_name in seen:
                # Cycle: substitute an `object` placeholder. The chart
                # never reaches into circular types (none of the
                # built-ins we care about have any) but we keep the
                # contract safe.
                return {"type": "object"}
            target = components.get(ref_name)
            if target is None:
                return node  # leave broken ref visible
            return resolve(target, components, seen | {ref_name})
        # Some schemas use allOf with a single $ref (the K8s convention
        # for "this field IS this type"). Inline that shape too so the
        # output is flat.
        if list(node.keys()) == ["description", "default", "allOf"] or list(node.keys()) == ["description", "allOf"]:
            if len(node["allOf"]) == 1 and "$ref" in node["allOf"][0]:
                inner = resolve(node["allOf"][0], components, seen)
                # Merge description from outer if inner doesn't have one
                if isinstance(inner, dict) and "description" not in inner:
                    inner = dict(inner)
                    inner["description"] = node.get("description", "")
                return inner
        return {k: resolve(v, components, seen) for k, v in node.items()}
    if isinstance(node, list):
        return [resolve(v, components, seen) for v in node]
    return node

def emit_crd(prefix, schema_name, group, plural, kind, scope, resolved):
    """Wrap the resolved schema in apiextensions.k8s.io/v1 CRD
    envelope so DirFetcher's OfflineGVKResolver picks up the
    (apiVersion, plural) mapping (bare schemas don't register a
    plural — see pkg/k8s/schemafetcher/dir_fetcher.go:90-93)."""
    crd_name = plural + (("." + group) if group else "")
    api_group_for_meta = group if group else ""
    crd = {
        "apiVersion": "apiextensions.k8s.io/v1",
        "kind": "CustomResourceDefinition",
        "metadata": {"name": crd_name},
        "spec": {
            "group": api_group_for_meta,
            "scope": scope,
            "names": {
                "plural": plural,
                "singular": kind.lower(),
                "kind": kind,
                "listKind": kind + "List",
            },
            "versions": [{
                "name": "v1",
                "served": True,
                "storage": True,
                "schema": {"openAPIV3Schema": resolved},
            }],
        },
    }
    return crd

# Collapse multi-line strings (mostly schema descriptions) to a single
# line before serialization. We don't need exact newline preservation in
# schema descriptions, just legible text, and a single line keeps diffs
# across re-fetches readable. yq handles all the YAML quoting/escaping.
def collapse_multiline(node):
    if isinstance(node, dict):
        return {k: collapse_multiline(v) for k, v in node.items()}
    if isinstance(node, list):
        return [collapse_multiline(v) for v in node]
    if isinstance(node, str) and "\n" in node:
        return " ".join(line.strip() for line in node.split("\n") if line.strip())
    return node

# Run
for entry in entries:
    parts = entry.split(":")
    prefix, endpoint, schema_name, group, plural, kind, scope = parts
    doc = load_endpoint(endpoint)
    components = doc.get("components", {}).get("schemas", {})
    raw = components.get(schema_name)
    if raw is None:
        print(f"!! schema {schema_name} not found in {endpoint}; skipping", file=sys.stderr)
        continue
    resolved = resolve(raw, components)
    crd = collapse_multiline(emit_crd(prefix, schema_name, group, plural, kind, scope, resolved))
    out_path = os.path.join(schema_dir, prefix + ".yaml")
    # Serialize via yq: JSON is valid YAML, so feed the CRD as JSON and
    # let `yq -P` emit block-style YAML. The schema dir is re-parsed by
    # DirFetcher (not byte-compared), so yq's formatting is fine.
    with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as tf:
        json.dump(crd, tf)
        tmp_json = tf.name
    try:
        yaml_body = subprocess.run(
            ["yq", "-P", "-oy", ".", tmp_json],
            check=True, capture_output=True, text=True,
        ).stdout
    finally:
        os.unlink(tmp_json)
    with open(out_path, "w") as f:
        f.write("# Generated by scripts/fetch-k8s-openapi-schemas.sh — do not edit.\n")
        f.write("# Source: kubectl get --raw " + endpoint + " (schema " + schema_name + ")\n")
        f.write(yaml_body)
    print(f"wrote {out_path}")
PY
