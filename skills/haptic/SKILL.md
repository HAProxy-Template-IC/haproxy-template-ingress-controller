---
name: haptic
description: Explain and customize HAPTIC, the HAProxy Template Ingress Controller for Kubernetes. Use for HAPTIC Helm values, Scriggo templates, custom annotations, watched resources, template libraries, and configuration validation. Applies to HAPTIC operator projects as well as its source repository; not to unrelated HAProxy or Go templates.
license: Apache-2.0
metadata:
  author: haproxy-haptic
---

# HAPTIC

Help the user express routing behavior in configuration and Scriggo templates,
then verify the rendered result. The skill works in an operator's configuration
repository; a HAPTIC source checkout is optional.

## Establish the configuration

Read the relevant values, templates, resource samples, and schema files already
available. Identify the HAPTIC/chart version, HAProxy version, enabled libraries,
and whether the user supplies Helm values or standalone custom resources. Ask
for missing inputs only when they affect the solution. Preserve existing values.

Use documentation for that version. Start with its `llms.txt` index and fetch
only relevant Markdown pages. [Development documentation](https://haproxy-haptic.org/docs/dev/llms.txt)
describes `main`, which can differ from a released installation. Find released
versions in [the documentation version index](https://haproxy-haptic.org/docs/versions.json).
In a matching source checkout, read `docs/site/docs/` and the actual library
templates. If tools or docs are unavailable, identify what remains unverified.
Documentation paths in the references, such as `templating/index.md`, are
relative to the selected documentation version's root URL.

## Controller model

HAPTIC watches configured Kubernetes resources, renders Scriggo templates,
validates their output, and deploys configuration through the HAPTIC agent in
each HAProxy pod. Routing behavior belongs in templates. New watched resource
types, including an operator's CRDs, don't need resource-specific Go code.

- `HAProxyTemplateConfig.spec` selects watches, references libraries, and defines
  templates and tests. `HAProxyTemplateLibrary` supplies reusable configuration.
- Libraries merge in `spec.libraryRefs` order, then the config wins. Maps merge
  by key; lists replace. Preserve other libraries and user overrides.
- The Helm chart puts operator overrides under `controller.config`. Helm renders
  Kubernetes manifests first; Scriggo renders their template strings later.
- Outputs include `haproxyConfig`, `maps`, `files`, `sslCertificates`, and
  `k8sResources`. Templates can also produce status patches and Events.
- Embedded tests check configuration loads; admission checks proposed changes to
  watched resources. Preflight checks the complete chart configuration before
  rollout. These checks cover different inputs; keep all applicable gates.

## Choose the smallest customization

Use an existing value or annotation if it expresses the requested behavior.
Otherwise add a snippet at a documented extension point. Replace a named snippet
only when its whole behavior must change; replacing `haproxyConfig` assumes
ownership of the complete configuration.

Read the relevant reference before editing:

| Task | Reference |
| --- | --- |
| Write Scriggo or extend the chart | [Customization](references/customization.md) |
| Watch a custom resource or select/index resources | [Resource access](references/resources.md) |
| Add tests, validate values, or diagnose failed rendering | [Validation](references/validation.md) |

Runnable starting points:

- [Header extension values](assets/header-values.yaml): a chart snippet with
  positive, empty-value, and rejected-input tests.
- [Custom resource configuration](assets/maintenance-config.yaml) and
  [its CRD schema](assets/schemas/maintenancepolicy.yaml): typed access to an
  operator-defined resource, with present, absent, and disabled fixtures.

Resolve bundled paths relative to this skill directory. Copy examples to the
user's project before adapting them; don't assume the skill is in the current
working directory.

## Verify the behavior

Add fixtures that distinguish the requested behavior from the previous behavior,
including absence and invalid input when relevant. Run the appropriate commands
from [Validation](references/validation.md). Check rendered directives and
auxiliary outputs, not just template compilation. Never remove tests, relax
assertions, or disable validation to get a configuration accepted.

Report the changed configuration, test result, and deployment implications.
Do not claim a playground render proves native HAProxy validity, traffic behavior,
or a reload-free deployment. Applying resources or upgrading a release must stay
within the user's requested scope and the host agent's permissions.
