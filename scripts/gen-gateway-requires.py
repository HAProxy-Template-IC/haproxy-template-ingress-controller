#!/usr/bin/env python3
"""Regenerate the gateway library's `requires` annotations.

Derives, for every templateSnippet in the merged chart config, the set of
gateway kinds it depends on, and writes `requires: [...]` lines into the
gateway fragment files. validationTests get requires from the gateway kinds
among their fixture keys.

Dependency edges per snippet:
  - direct typed/store references: `resources.<kind>`
  - imports: `{% import "other" ... %}` (compile-time; no default clause)
  - renders WITHOUT a `default` clause: `render "other"`
  - fileRegistry edges: a snippet consuming a LITERAL file name that another
    snippet registers (fileRegistry.Register("map", "name.map", ...)) inherits
    the producer's requirements — the reference breaks at `haproxy -c` when
    the producer is stripped, even though no compile-time edge exists.

Usage: render the merged config first, then run this script:

    helm template charts/haptic \
      --set controller.templateLibraries.gateway.experimentalChannel=true \
      | yq 'select(.kind == "HAProxyTemplateConfig" or .kind == "HAProxyTemplateLibrary")' > /tmp/merged.yaml
    python3 scripts/gen-gateway-requires.py /tmp/merged.yaml
"""

import glob
import os
import re
import sys

import yaml

# Anchor all chart paths to the repo root so the script works from any CWD
# (running it elsewhere used to silently match zero fragment files).
REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
GATEWAY_GLOBS = (
    os.path.join(REPO_ROOT, 'charts/haptic/charts/gateway/*.yaml'),
    os.path.join(REPO_ROOT, 'charts/haptic/charts/gateway/tests/*.yaml'),
)

GATEWAY_KINDS = {
    'gatewayclasses', 'gateways', 'httproutes', 'grpcroutes', 'tlsroutes',
    'tcproutes', 'referencegrants', 'listenersets', 'backendtlspolicies',
}

RES_RE = re.compile(r'resources\.(\w+)')
IMP_RE = re.compile(r'\{%-?\s*import\s+"([^"]+)"')
REND_RE = re.compile(r'render\s+"([^"]+)"(?!\s+default)')
REG_RE = re.compile(r'fileRegistry\.Register\(\s*"[^"]+"\s*,\s*"([^"]+)"')


def main(merged_path: str) -> None:
    with open(merged_path) as f:
        spec = yaml.safe_load(f)['spec']
    snippets = {k: v.get('template', '') for k, v in spec.get('templateSnippets', {}).items()}

    direct, edges = {}, {}
    registered_by = {}  # literal file name -> producer snippet
    for name, body in snippets.items():
        for fname in REG_RE.findall(body):
            registered_by[fname] = name
    for name, body in snippets.items():
        direct[name] = {m for m in RES_RE.findall(body) if m in GATEWAY_KINDS}
        deps = set(IMP_RE.findall(body)) | {r for r in REND_RE.findall(body) if r in snippets}
        for fname, producer in registered_by.items():
            if producer != name and fname in body:
                deps.add(producer)
        edges[name] = deps

    def close(name, seen):
        if name in seen:
            return set()
        seen.add(name)
        out = set(direct.get(name, set()))
        for d in edges.get(name, set()):
            out |= close(d, seen)
        return out

    trans = {n: close(n, set()) for n in snippets}

    files = sorted(p for g in GATEWAY_GLOBS for p in glob.glob(g))
    if not files:
        sys.exit(f'no gateway fragment files found under {REPO_ROOT} — aborting')
    for path in files:
        with open(path) as f:
            raw = f.read()
        doc = yaml.safe_load(raw) or {}
        frag_snips = set((doc.get('templateSnippets') or {}).keys())
        frag_tests = doc.get('validationTests') or {}
        test_reqs = {t: sorted({k for k in (v.get('fixtures') or {}) if k in GATEWAY_KINDS})
                     for t, v in frag_tests.items()}
        lines = [l for l in raw.split('\n')
                 if not re.match(r'^    requires: \[[^\]]*\]$', l)]
        out, section = [], None
        for line in lines:
            out.append(line)
            m = re.match(r'^(\w[\w-]*):', line)
            if m:
                section = m.group(1)
                continue
            m = re.match(r'^  ([\w-]+):\s*$', line)
            if not m:
                continue
            name = m.group(1)
            if section == 'templateSnippets' and name in frag_snips and trans.get(name):
                out.append('    requires: [%s]' % ', '.join(sorted(trans[name])))
            elif section == 'validationTests' and name in frag_tests and test_reqs.get(name):
                out.append('    requires: [%s]' % ', '.join(test_reqs[name]))
        with open(path, 'w') as f:
            f.write('\n'.join(out))
        print(f'{path}: regenerated')


if __name__ == '__main__':
    main(sys.argv[1] if len(sys.argv) > 1 else '/tmp/merged.yaml')
