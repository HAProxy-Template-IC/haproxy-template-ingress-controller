# Tasks

## 1. Helper macro (TDD)

- [ ] 1.1 Write failing helm-unittest cases for `ResolveServicePort` covering: number-only, number-only-unnamed, name-only, both-given-number-wins, name-no-match (panic), service-missing (panic). Add to `charts/haptic/tests/` as `resolve_service_port_test.yaml` if no obvious existing file fits.
- [ ] 1.2 Implement `ResolveServicePort` macro in `charts/haptic/libraries/base.yaml`. Verify the unit tests from 1.1 pass.
- [ ] 1.3 Confirm panic messages name the offending namespace/service/portRef and (when relevant) list the actual port names the service does expose, so debugging from logs is one read away.

## 2. Switch ingress library call site

- [ ] 2.1 Add an integration test fixture under `tests/` (or wherever `./scripts/test-templates.sh` reads from) for an Ingress that references its backend service by **port name** (`port.name`), with a Service exposing that name on a non-80 numeric port.
- [ ] 2.2 Run `./scripts/test-templates.sh` against the new fixture and capture the failure (current behavior: emits `:80`).
- [ ] 2.3 Switch `charts/haptic/libraries/ingress.yaml` `util-generate-backends-ingress` call site to use `ResolveServicePort`. Re-run the fixture test; assert it passes.
- [ ] 2.4 Re-run the full template-test suite to confirm no existing fixture (all using numeric ports) regressed.

## 3. Switch gateway library call sites (×2)

- [ ] 3.1 Add fixtures for both call paths in `gateway.yaml`: (a) plain HTTPRoute → service backend by port name; (b) the weighted/multi-backend path with at least one backend referenced by port name.
- [ ] 3.2 Run the suite, capture failures.
- [ ] 3.3 Update both call sites in `gateway.yaml` to use `ResolveServicePort`. Verify both fixtures now pass.

## 4. Switch nginx-ingress / haproxy-ingress / haproxytech library call sites

- [ ] 4.1 Add per-library fixtures (one each) referencing a named service port on a non-80 number.
- [ ] 4.2 For each library, update the `BackendServers` call site to use `ResolveServicePort`. Verify the new fixture passes.
- [ ] 4.3 Confirm all previously-green fixtures across these libraries remain green.

## 5. Comment correctness

- [ ] 5.1 Add an assertion to at least one fixture per library that the `# Backend for: Ingress <ns>/<name> → Service <svc>:<port>` comment shows the **resolved numeric port**, not the previous misleading 80. (Catches regressions in the cosmetic-but-debugging-useful comment line.)

## 6. Panic behavior end-to-end

- [ ] 6.1 Add a fixture where the Ingress references `port.name: nonexistent` against a service that does not expose that name. Assert the render fails with a clear message naming `nonexistent` and listing the service's actual port names.
- [ ] 6.2 If the controller-side tests have a place where render failures are observed (e.g. an integration test verifying `deployFailed` status), add a case there too; otherwise note this as a manual-verification step in the MR description.

## 7. Documentation

- [ ] 7.1 Add a "Fixed" entry under `[Unreleased]` in `charts/haptic/CHANGELOG.md` summarizing the fix and naming the resolution helper. Do NOT mark `BREAKING`: no released version shipped the named-port path as working, so this is a pure bugfix.
- [ ] 7.2 If the existing template-libraries developer doc references `BackendServers` directly, add a note pointing future callers at `ResolveServicePort` first.

## 8. Pre-MR verification

- [ ] 8.1 `make lint-chart` passes.
- [ ] 8.2 `./scripts/test-templates.sh` passes the full suite (new fixtures + existing).
- [ ] 8.3 `make check-all` passes (Go tests, lint, audit — unaffected by templates but pre-commit hook will run them).
- [ ] 8.4 Render the chart against the cluster's live unifi Ingress (or a faithful copy) and confirm the generated `server` line is `<pod-ip>:8443` and the `# Backend for: ...` comment shows `:8443`.

## 9. Merge request

- [ ] 9.1 Push branch and open MR via `glab mr create`. Title: `fix(templates): resolve named Service ports correctly across all ingress libraries`. Body links this change directory under `openspec/changes/` and summarises the user-visible symptom (503 with `<NOSRV>` for any Ingress using `port.name`).
- [ ] 9.2 Confirm CI pipeline passes (chart-test job in particular). Fix any failures by addressing root causes, not by relaxing assertions.

## 10. Cleanup

- [ ] 10.1 After merge, the openspec change directory either stays (per project's archival convention seen in `openspec/changes/archive/`) or moves to archive — follow whatever existing convention dictates.
