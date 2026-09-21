# Gateway API conformance evidence

Use the report for your HAPTIC release to check Gateway API coverage. A passing
regression shard doesn't establish conformance: each report requires one complete
run against the published controller image.

## Coverage

The report contains three profiles. Extended results may be partial; inspect the
named skipped tests and unsupported features before relying on an extension.

| Surface | Evidence and limits |
| --- | --- |
| HTTPRoute | `GATEWAY-HTTP` core and extended results |
| GRPCRoute | `GATEWAY-GRPC` core and extended results |
| TLSRoute | `GATEWAY-TLS` core and extended results |
| TCPRoute | HAPTIC's separate end-to-end tests; no profile in this report |
| Independent backend certificate SAN matching | `BackendTLSPolicySANValidation` remains skipped because HAProxy verifies the SNI hostname; unsupported policy identities are rejected |
| Mesh and `UDPRoute` | Unsupported; excluded from the declared feature set |

The backend identity limit is tracked in
[HAProxy issue 3452](https://github.com/haproxy/haproxy/issues/3452).
A generated report isn't an accepted upstream submission. Upstream review and the
[Gateway API implementations listing](https://gateway-api.sigs.k8s.io/implementations/)
are separate evidence.

## Release artifacts

The `release-gateway-conformance` job runs after the release controller and chart
are published. Its artifacts have no scheduled expiration:

| File | Contents |
| --- | --- |
| `report.yaml` | Unmodified upstream report, including partial coverage |
| `provenance.json` | Source commit and hash, chart digest, controller image and binary digests, running controller identities, Kubernetes and Gateway API versions, and test exit code |
| `SHA256SUMS` | Checksums of the report and provenance |

Successful evidence requires a clean checkout at the release tag, a published
image digest, matching image version and source hash, matching running binaries,
all three passing core profiles, and no failed extended tests. Missing or
incomplete reports fail the job. Failed jobs retain available artifacts with
`verified: false`; they don't establish conformance.

The manual `gateway-conformance-report` job produces candidate evidence for a
pipeline snapshot. Its version identifies that pipeline's image. It doesn't
claim to test the released version in `VERSION`.

Download artifacts from the release job using the GitLab CLI, `unzip`, and
`sha256sum`:

```bash
read -r -p 'Release conformance job ID: ' job_id
artifact_dir="$(mktemp -d)"
glab api "projects/haproxy-haptic%2Fhaptic/jobs/${job_id}/artifacts" >"$artifact_dir/evidence.zip"
unzip -q "$artifact_dir/evidence.zip" -d "$artifact_dir"
cd "$artifact_dir/build/gateway-conformance"
sha256sum --check SHA256SUMS
python3 - <<'PY'
import json
from pathlib import Path
p = json.loads(Path('provenance.json').read_text())
assert p['verified'] and p['release_evidence'] and p['test_exit_code'] == 0, p['errors']
print(p['controller']['version'], p['git_commit'])
print(json.dumps(p['profiles'], indent=2))
PY
```

For generation and upstream submission, see the repository's
[conformance instructions](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/tests/conformance/README.md).
