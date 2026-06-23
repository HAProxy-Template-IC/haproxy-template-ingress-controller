# Tasks

## 1. Wire-protocol primitives (TDD)

- [ ] 1.1 `pkg/controller/pluggablevalidator/protocol_test.go` — table-driven tests: encode + decode of valid frames, reject zero-length frame, reject frame larger than configured max, reject malformed JSON, reject mismatched `protocol_version`, reject empty `files` array.
- [ ] 1.2 `pkg/controller/pluggablevalidator/protocol.go` — `Request`, `Response`, `Diagnostic`, `Severity` types with JSON tags matching the hub-side wire format. `Encode` / `Decode` helpers using length-prefixed JSON. `MaxFrameSize` constant (default 1 MiB; configurable on the client side per-validator).

## 2. Result cache (TDD)

- [ ] 2.1 `pkg/controller/pluggablevalidator/cache_test.go` — content-hash key derivation is stable across goroutines; LRU evicts oldest under capacity pressure; cache hit returns the cached `Response` byte-for-byte; cache key incorporates plugin-name to prevent cross-plugin pollution.
- [ ] 2.2 `pkg/controller/pluggablevalidator/cache.go` — `ResultCache` with `Get(key)` / `Put(key, response)`, content-hash key derivation (sha256 of plugin-name + TOML bytes), capacity bound (default 256 entries).

## 3. Unix-socket client (TDD)

- [ ] 3.1 `pkg/controller/pluggablevalidator/client_test.go` with a fixture socket server (`testutil/fixturesocket.go`) that scripts canned responses. Cases: happy-path round-trip, server-side timeout (client respects per-call timeout), connection refused (returns synthetic `connection refused` diagnostic), malformed response frame, partial write retry semantics if applicable.
- [ ] 3.2 `pkg/controller/pluggablevalidator/client.go` — `Client.Validate(ctx, configBytes)` blocking call that opens the socket, writes one request frame, reads one response frame, closes. Per-call timeout via `context.Context` and the configured `timeoutMs`. No retries (plumbed at the component level).
- [ ] 3.3 `pkg/controller/pluggablevalidator/health.go` — `HealthCheck(socketPath) error` for `/healthz` callers: stat the path, verify it's a unix socket, attempt `O_WRONLY` open + close. Sub-millisecond happy path.

## 4. CRD field

- [ ] 4.1 `pkg/apis/haproxytemplate/v1alpha1/types_validators.go` — `ValidatorConfig` type with `name`, `socketPath`, `plugins []string`, `timeoutMs *int32`, plus kubebuilder validation tags. Add `Validators []ValidatorConfig` to `HAProxyTemplateConfigSpec`.
- [ ] 4.2 `make generate` — codegen refreshes the CRD YAML and the `zz_generated_deepcopy.go`.
- [ ] 4.3 Acceptance check: `kubectl apply` of an HAProxyTemplateConfig with a `validators` block does not fail OpenAPI validation (covered by the next change's e2e test; here we assert the rendered CRD passes `kubectl --validate=true` against an example fixture).

## 5. Event types

- [ ] 5.1 `pkg/controller/events/pluggable_validation.go` — `PluggableValidationRequest` and `PluggableValidationResponse` event structs mirroring `ConfigValidationRequest` / `ConfigValidationResponse`. Document why they're separate (different correlation IDs, different scatter-gather group).

## 6. Event adapter

- [ ] 6.1 `pkg/controller/pluggablevalidator/component_test.go` — subscribes to bus, on `PluggableValidationRequest` invokes the client (or returns cached `Response`), publishes `PluggableValidationResponse`. On client error, publishes a response with a single error-severity diagnostic and `result: "error"`.
- [ ] 6.2 `pkg/controller/pluggablevalidator/component.go` — wraps `component.Base` like `pkg/controller/validator/base.go`. Constructor takes the EventBus, a logger, the parsed `[]ValidatorConfig` slice, and the cache. Exposes `Healthy() (bool, []string)` summarising socket health for `/healthz` injection in the next MR.

## 7. Controller startup wiring

- [ ] 7.1 `pkg/controller/controller.go` — instantiate the component during `setupComponents()`. Component subscribes to the bus but no one publishes its event yet, so it's dormant. Verifies the component compiles into the binary and survives shutdown without leaking goroutines.

## 8. Protocol relocation

- [ ] 8.1 `docs/development/validator-protocol.md` — verbatim copy of the hub-side `validate-socket-protocol.md`, minus the "interim ownership" disclaimer (which belongs only on the redirect copy). Add HAPTIC-side framing: who calls this, the LRU cache layered on top, the protocol-version evolution policy from HAPTIC's POV.
- [ ] 8.2 Hub-side spec gets a separate tiny MR after this lands: replace the body with a one-liner pointing at the new HAPTIC URL. Tracked separately so the cross-repo diff stays minimal.

## 9. CHANGELOG

- [ ] 9.1 Add `[Unreleased] / ### Added` entry naming the new CRD field and the new package, plus a pointer at the protocol doc.
