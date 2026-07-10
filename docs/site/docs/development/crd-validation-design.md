# CRD & Validation Design Notes

This page captures the durable design decisions behind the `HAProxyTemplateConfig` CRD and the validation stack. For field-by-field reference see [CRD Reference](../crd-reference.md); for user-facing validation workflows see [Validation Tests](../validation-tests.md).

## Why a CRD Instead of a ConfigMap

The controller originally accepted its configuration via `ConfigMap`. That was replaced with a typed, namespaced Kubernetes API (`scope: Namespaced`, defined in `pkg/apis/haproxytemplate/v1alpha1`) because:

- **Schema validation at admission.** OpenAPI rules in the CRD reject malformed YAML before it reaches the controller — a `ConfigMap` accepts any strings.
- **Embedded test fixtures.** `spec.validationTests` lets users ship assertions alongside templates and run them via the validating webhook or the `haptic-controller validate` CLI without duplicating fixtures elsewhere.
- **Native tooling.** `kubectl get htplcfg`, `kubectl describe`, status subresource, RBAC on a real Kind — all come for free.
- **Typed client.** The generated clientset (`pkg/generated`) gives both the controller and test harnesses a typed view of the config.

## Defence in Depth

Three layers of validation run in sequence:

1. **OpenAPI schema** (Kubernetes API server). Rejects structural errors the moment `kubectl apply` hits the apiserver — invalid enum values, missing required fields, bad types. This covers the *structure* of `HAProxyTemplateConfig` itself; the *semantic* validity of its templates (do they compile and render to a config `haproxy -c` accepts?) is checked by the dedicated `haproxytemplateconfigs` webhook in layer 2.
2. **Validating admission webhook** (the controller itself, served at `/validate`). Two kinds of webhook entry are registered. The **watched-resource webhooks** apply to *watched resources* — by default the chart libraries opt **Ingresses, HTTPRoutes, and GRPCRoutes** in (`enableValidationWebhook: true` in `charts/haptic/libraries/ingress.yaml` and `charts/haptic/charts/gateway/_index.yaml`); Gateways, Services, EndpointSlices, and Secrets are deliberately left out so EndpointSlice churn and large Secret payloads don't put every cluster write on the webhook critical path. They render templates with the proposed object overlaid on the live store and reject the write if rendering or HAProxy validation fails; to validate additional kinds, set `enableValidationWebhook: true` on the matching watched-resource entry in `controller.config.watchedResources`. Separately, a dedicated **`haproxytemplateconfigs` webhook** (`pkg/controller/webhook/configvalidator.go`) validates the CRD itself: it compiles the *prospective* templates, builds an ephemeral render+validate pipeline, runs it against the controller's current stores, and rejects the apply with line-numbered `haproxy -c` output when the new templates don't render to a valid config — so operators catch template errors before the resource ever lands in etcd. This upstream gate is what lets the leader-side reconcile pipeline skip its own `haproxy -c`.
3. **Runtime validation** (reconciler). The three-phase HAProxy validator (`pkg/dataplane`: client-native syntax parse + OpenAPI schema check + `haproxy -c` semantic check) runs on every reconciliation; results are cached by `(configHash, auxHash, versionHash)` so drift-prevention cycles are cheap. If it fails, the previously-deployed config stays in place.

Failure at layer 3 never takes down traffic — the reconciler refuses to deploy invalid output while continuing to serve the last good config. The watched-resource webhooks ship `failurePolicy: Fail` (charts/haptic/templates/validatingwebhookconfiguration.yaml), so creates/updates of *opted-in resources* are rejected when the controller is unreachable; that's deliberate, since rendering an admission decision based on an unvalidated overlay is riskier than asking the user to retry. The `haproxytemplateconfigs` webhook instead ships `failurePolicy: Ignore` — it must tolerate the controller being absent (first install, or applying a fix while the controller is degraded), so a webhook outage falls back to a delayed dataplane-API rejection on the next reload rather than blocking the operator.

## Credentials Stay in a Secret

`spec.credentialsSecretRef` points at a `Secret` rather than embedding credentials inline:

- Keeps `HAProxyTemplateConfig` non-sensitive, so it can be stored in Git / Helm values / etc.
- Allows independent rotation: the credentials loader watches the Secret and re-publishes `CredentialsUpdatedEvent` without a full reinitialisation cycle.
- Follows the conventional Kubernetes split between "what to do" (typed API) and "secrets needed to do it" (opaque Secret).

Required keys: `dataplane_username`, `dataplane_password`.

## Webhook Architecture

The validating webhook runs inside the controller pod rather than a dedicated deployment. Rationale:

- **Single source of truth.** The webhook uses the same render/validate code path as the reconciler, so there's no way for the admission decision to drift from the runtime decision.
- **No extra moving parts.** One deployment, one Service, one Lease, one cert.
- **Shared store.** The webhook reuses the watched-resource store to build a realistic render context with proposed changes overlaid (see `pkg/stores/overlay.go`).

TLS certificates are provided by cert-manager (recommended) or supplied manually; the chart wires both options.

### Multi-Controller Isolation

Each Helm release deploys its own `ValidatingWebhookConfiguration` named `<release>-webhook`, and each entry's `clientConfig.service` points at the controller `Service` for that release. So cross-validation between two HAPTIC instances doesn't happen by accident — the apiserver only invokes the webhook(s) whose `rules` match the resource being admitted, and each release's rules cite a different Service. There is **no** `objectSelector` in the chart today; if you need to scope a webhook to a label-selector subset of objects, add one in `validatingwebhookconfiguration.yaml`.

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
| Webhook TLS, RBAC, chart deployment | [Deploying with Helm](../deploying-with-helm.md), [SSL Certificates](../ssl-certificates.md) |
| Go type definitions | `pkg/apis/haproxytemplate/v1alpha1/types_*.go` |
| Generated code | `pkg/generated/` (`make generate` to refresh) |
