# CRD & Validation Design Notes

This page captures the durable design decisions behind the `HAProxyTemplateConfig` CRD and the validation stack. For field-by-field reference see [CRD Reference](../crd-reference.md); for user-facing validation workflows see [Validation Tests](../validation-tests.md).

## Why a CRD Instead of a ConfigMap

The controller originally accepted its configuration via `ConfigMap`. That was replaced with a cluster-scoped API because:

- **Schema validation at admission.** OpenAPI rules in the CRD reject malformed YAML before it reaches the controller — a `ConfigMap` accepts any strings.
- **Embedded test fixtures.** `spec.validationTests` lets users ship assertions alongside templates and run them via the validating webhook or the `haptic-controller validate` CLI without duplicating fixtures elsewhere.
- **Native tooling.** `kubectl get htplcfg`, `kubectl describe`, status subresource, RBAC on a real Kind — all come for free.
- **Typed client.** The generated clientset (`pkg/generated`) gives both the controller and test harnesses a typed view of the config.

## Defence in Depth

Three layers of validation run in sequence:

1. **OpenAPI schema** (Kubernetes API server). Rejects structural errors the moment `kubectl apply` hits the apiserver — invalid enum values, missing required fields, bad types.
2. **Validating admission webhook** (the controller itself). Renders templates against the fixtures declared in `spec.validationTests`, runs assertions, and rejects the write if anything fails. Disabled per-resource-kind via `enableValidationWebhook` on each watched-resource entry to avoid exploding the matrix; the CRD itself is always webhook-guarded.
3. **Runtime validation** (reconciler). The three-phase HAProxy validator (`pkg/dataplane`: client-native syntax parse + OpenAPI schema check + `haproxy -c` semantic check) runs on every reconciliation; results are cached by `(configHash, auxHash, versionHash)` so drift-prevention cycles are cheap. If it fails the previously-deployed config stays in place.

Failure at layer 3 never takes down traffic — the reconciler refuses to deploy invalid output while continuing to serve the last good config. Layer 2 (`failurePolicy: Fail`) does block writes to `HAProxyTemplateConfig` if the webhook endpoint is unreachable; this is deliberate — accepting an unvalidated config is riskier than rejecting a legitimate edit until the operator is back, and `HAProxyTemplateConfig` edits are rare operator actions, not part of the request path.

## Credentials Stay in a Secret

`spec.credentialsSecretRef` points at a `Secret` rather than embedding credentials inline:

- Keeps `HAProxyTemplateConfig` non-sensitive, so it can be stored in Git / Helm values / etc.
- Allows independent rotation: the credentials loader watches the Secret and re-publishes `CredentialsUpdatedEvent` without a full reinitialisation cycle.
- Follows the conventional Kubernetes split between "what to do" (typed API) and "secrets needed to do it" (opaque Secret).

Required keys: `dataplane_username`, `dataplane_password`, `validation_username`, `validation_password`.

## Webhook Architecture

The validating webhook runs inside the controller pod rather than a dedicated deployment. Rationale:

- **Single source of truth.** The webhook uses the same render/validate code path as the reconciler, so there's no way for the admission decision to drift from the runtime decision.
- **No extra moving parts.** One deployment, one Service, one Lease, one cert.
- **Shared store.** The webhook reuses the watched-resource store to build a realistic render context with proposed changes overlaid (see `pkg/stores/overlay`).

TLS certificates are provided by cert-manager (recommended) or supplied manually; the chart wires both options.

### Multi-Controller Isolation

Clusters that run multiple HAPTIC instances in the same namespace must not cross-validate. The webhook's `objectSelector` matches `app.kubernetes.io/instance` against its own release, so each controller only sees configs labelled for it. Users don't have to set labels themselves — the chart does it automatically via the standard Helm labels.

## CRD Versioning Posture

The API is at `v1alpha1`. The project's posture during alpha:

- Breaking changes are allowed but batched: new sub-fields should be added as optional; renames/removals wait for a minor bump.
- No conversion webhooks yet. When we graduate to `v1beta1` we'll add one and ship a conversion strategy in the release notes.
- The generated clientset, informers, and listers live in `pkg/generated`; regenerate via `make generate` after changing types in `pkg/apis/haproxytemplate/v1alpha1/`.

## Relationship to Other Docs

| Concern | Canonical doc |
|---------|---------------|
| Every field with types and defaults | [CRD Reference](../crd-reference.md) |
| Writing and debugging validation tests | [Validation Tests](../validation-tests.md) |
| How configuration composes (chart layers, snippets) | [Configuration Model](./design/configuration.md) |
| Webhook TLS, RBAC, chart deployment | Helm chart docs (`charts/haptic/`) |
| Go type definitions | `pkg/apis/haproxytemplate/v1alpha1/types_*.go` |
| Generated code | `pkg/generated/` (`make generate` to refresh) |
