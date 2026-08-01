# Governance guardrails

Enforce org-wide policy across the resources HAPTIC watches — require an annotation, inject a safe default, or validate a value — without hand-editing a single Ingress. Governance is off by default; you turn it on with a map of named rules under `controller.config.templatingSettings.extraContext.governance`. Rules are keyed by a name you choose, so your rules merge with — rather than replace — any a template library ships, and you switch a single one off with `enabled: false`.

This guide shows how to roll a guardrail out safely. For every rule field and its exact meaning, see [Policy guardrails (governance)](../reference.md#policy-guardrails-governance) in the Chart Values Reference.

## How a rule behaves

Each rule targets one watched resource by name and, per matching resource, does one of two things:

- **Inject** — when the value at `path` is absent and the rule sets a `default`, HAPTIC writes the default into that render, so downstream config is generated as if the resource had set it. The live resource is never modified.
- **Validate** — when the value is present (or `required`), HAPTIC checks it against the rule (`required`, `min`/`max`, `allowed`, `pattern`, `anyOf`, `satisfiedBy`).

A rule's `enforcement` controls what a *violation* does:

- `audit` — records a `GovernanceViolation` Warning Event on the resource and keeps serving. Nothing is blocked.
- `reject` — denies a **new or edited** violating resource at the admission webhook. An already-present violator isn't blocked; it records the same Warning Event and keeps serving.

Enforcement is scoped to the offending resource's own admission, so one pre-existing violator never blocks an unrelated `kubectl apply`, and a violation on a live reconcile never aborts the render.

!!! tip "Always start in audit"
    The daemon runs its bundled validation tests on every config load, and enforcement never blocks existing traffic — but a `reject` rule *will* deny future edits to any resource that violates it. Roll out in `audit` first, read the events, fix or exempt the resources they name, and only then switch to `reject`.

## Require a WAF policy on every Ingress

This is the common case: every Ingress must select a Web Application Firewall (WAF) policy. Roll it out in three moves — audit, fix, enforce.

### 1. Turn the rule on in audit mode

Add the rule to your values and apply it:

```yaml
# values.yaml
controller:
  config:
    templatingSettings:
      extraContext:
        governance:
          enabled: true
          rules:
            ingress-waf-policy:
              enabled: true
              resource: ingresses
              path: metadata.annotations['haproxy-haptic.org/waf-policy']
              required: true
              enforcement: audit
```

```bash
helm upgrade my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --namespace haptic -f values.yaml
```

Substitute your own release name and namespace. If you deploy with GitOps, commit the values change and let your sync apply it.

### 2. See which resources violate

List the Warning Events the audit rule recorded:

```bash
kubectl get events --all-namespaces --field-selector reason=GovernanceViolation
```

Each event names one Ingress that has no `haproxy-haptic.org/waf-policy` annotation, and the message states the rule it broke.

### 3. Fix the flagged resources, then enforce

For each flagged Ingress, either add the annotation:

```bash
kubectl annotate ingress <name> --namespace <ns> \
  haproxy-haptic.org/waf-policy=<your-policy>
```

or exempt its namespace from the guardrail (see [Exempt namespaces](#exempt-namespaces) below).

When `kubectl get events` no longer reports new violations, switch the rule to `reject` and apply:

```yaml
# values.yaml — same rule, now enforcing
controller:
  config:
    templatingSettings:
      extraContext:
        governance:
          enabled: true
          rules:
            ingress-waf-policy:
              enabled: true
              resource: ingresses
              path: metadata.annotations['haproxy-haptic.org/waf-policy']
              required: true
              enforcement: reject
```

```bash
helm upgrade my-controller oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --namespace haptic -f values.yaml
```

### 4. Confirm enforcement

Send a non-compliant Ingress to the admission webhook without creating it:

```bash
kubectl apply --dry-run=server -f - <<'EOF'
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: governance-check
  namespace: default
spec:
  ingressClassName: haproxy
  rules:
    - host: governance-check.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service: { name: example, port: { number: 80 } }
EOF
```

The webhook denies the request with the rule's message. Add a `haproxy-haptic.org/waf-policy` annotation and the same dry-run is admitted.

## Inject a safe default instead of requiring

Rather than reject Ingresses that lack a policy, give them one automatically. This rule injects a detect-mode WAF policy on every Ingress that doesn't already select one; Ingresses that already have a policy keep theirs.

```yaml
# values.yaml
controller:
  config:
    templatingSettings:
      extraContext:
        waf:
          policies:
            inline:
              baseline-detect:
                description: OWASP CRS in detect mode — monitor only, never blocks
                enforcement: detect
        governance:
          enabled: true
          rules:
            ingress-waf-policy:
              enabled: true
              resource: ingresses
              path: metadata.annotations['haproxy-haptic.org/waf-policy']
              default: baseline-detect
```

Detect mode inspects and logs but blocks nothing, so it's safe to inject on live traffic. Use the WAF metrics and audit log (see [Security](security.md)) to decide which routes are ready for a deny-mode policy.

## Require every Ingress to terminate TLS

`satisfiedBy: tls` passes when the Ingress has a `spec.tls` block **or** the chart-wide default HTTPS is on (`extraContext.ingressDefaultHTTPS`, the default). It fails only for an Ingress that would be served plaintext.

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        governance:
          enabled: true
          rules:
            ingress-tls:
              enabled: true
              resource: ingresses
              satisfiedBy: tls
              enforcement: audit
```

## Clamp a value to a ceiling

`min`/`max` bound a numeric annotation. With `onViolation: clamp`, an out-of-range value is rewritten to the nearest bound for that render instead of being rejected — so a team can't set a per-source rate limit above the ceiling you allow, and nothing breaks if they try.

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        governance:
          enabled: true
          rules:
            rate-limit-ceiling:
              enabled: true
              resource: ingresses
              path: metadata.annotations['haproxy-haptic.org/rate-limit-rps']
              max: 5000
              onViolation: clamp
```

## Exempt namespaces

`exemptNamespaces` skips a namespace entirely — no rule is checked, injected, or enforced there. Use it for infrastructure or system namespaces that shouldn't answer to tenant policy.

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        governance:
          enabled: true
          exemptNamespaces: [kube-system, monitoring]
          rules:
            ingress-waf-policy:
              enabled: true
              resource: ingresses
              path: metadata.annotations['haproxy-haptic.org/waf-policy']
              required: true
              enforcement: reject
```

## Govern any watched resource

Rules are generic — set `resource` to any name in your `watchedResources`, not just `ingresses`. The same rule shape governs `httproutes`, a custom CRD, or anything else HAPTIC watches:

```yaml
rules:
  httproute-waf-policy:
    enabled: true
    resource: httproutes
    path: metadata.annotations['haproxy-haptic.org/waf-policy']
    required: true
    enforcement: audit
```

## Switch off a single rule

Rules are keyed, so setting one entry's `enabled` to `false` leaves the rest
alone. This is how you disable a rule a template library ships without
restating — or accidentally dropping — every other rule:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        governance:
          rules:
            ingress-tls:
              enabled: false
```

`enabled` is required on every rule. An entry that omits it fails the render
naming the rule, rather than sitting inert and enforcing nothing.

## See also

- [Policy guardrails (governance)](../reference.md#policy-guardrails-governance) — every rule field, in the Chart Values Reference
- [Security](security.md) — annotation input as a trust boundary, WAF policies, and the audit trail
- [Watching Resources](../watching-resources.md) — the `watchedResources` a rule's `resource` refers to
