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

An absent or failed job provides no verified release report. A manual
`gateway-conformance-report` job tests a development candidate. Neither an
artifact nor a passing regression test is an accepted upstream submission.

Maintainers generating or submitting reports should use the
[conformance instructions](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/tests/conformance/README.md).
