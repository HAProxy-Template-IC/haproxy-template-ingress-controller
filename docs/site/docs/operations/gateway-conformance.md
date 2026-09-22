# Gateway API conformance evidence

Use the [Gateway API reference](../libraries/gateway.md) to check supported
features and configuration. Conformance reports provide test results for a
specific HAPTIC and Gateway API version, including skipped tests and limits.

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

## Find evidence for your release

Check the [Gateway API implementations listing](https://gateway-api.sigs.k8s.io/implementations/)
for an accepted report matching your HAPTIC and Gateway API versions. A report for
another version doesn't establish coverage for your installation.

When a HAPTIC release has a successful `release-gateway-conformance` job, you can
also open its artifacts from the release tag's pipeline on
[GitLab](https://gitlab.com/haproxy-haptic/haptic/-/pipelines):

- `report.yaml` lists the tested profiles, supported features, and skipped tests.
- `provenance.json` identifies the tested release and environment. Its `verified`
  and `release_evidence` fields must both be `true`.
- `SHA256SUMS` contains checksums for those files.

A manual `gateway-conformance-report` job tests a development candidate, not a
published release. A local artifact or passing regression test doesn't establish
upstream acceptance; use the implementations listing for that.
