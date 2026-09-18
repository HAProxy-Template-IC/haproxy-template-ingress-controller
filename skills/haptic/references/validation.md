# Validate HAPTIC customizations

Use matching HAPTIC, chart, HAProxy, and schema versions. Inspect `haptic --help`
and the selected subcommand's help if the installed CLI differs from these
examples. Run commands from the user's project with its actual paths.

## Helm values

With `haptic`, a chart directory matching the deployment, native HAProxy on
`PATH`, and offline schemas:

```bash
haptic preflight --values ./haptic-values.yaml --chart ./chart \
  --namespace haptic --release haptic --schema-dir ./schemas
```

Use the actual release name and namespace. Preserve every values file and its
order by repeating `--values`. Preflight uses the image-embedded chart when
`--chart` isn't supplied. Without `--schema-dir`, it reads schemas from the
configured cluster; this requires working credentials and API access.
Preflight renders Helm charts in process; it doesn't need a Helm executable.

Preflight runs the merged configuration's load checks and embedded tests. Vector
and Varnish validation use containers when enabled; without a container runtime,
those checks report that they were skipped. Report that limitation instead of
claiming they passed. Preflight's fixtures don't represent all live routes.

The chart's pre-rollout hook runs preflight by default. Keep it enabled. See the
version's `operations/validate-before-deploy/index.md` for binary extraction,
schema preparation, and sidecar validation requirements.

## Standalone configuration

With `haptic` and a matching native `haproxy` on `PATH`:

```bash
haptic validate --file ./config.yaml --schema-dir ./schemas
```

Pass all referenced `HAProxyTemplateLibrary` objects with the config (multi-document
YAML or repeated `--file`). Passing just a config's `libraryRefs` doesn't fetch
the libraries from Kubernetes. Library order comes from `libraryRefs`.

For the bundled custom-resource example, copy `assets/maintenance-config.yaml`
to `config.yaml` and `assets/schemas/` to `schemas/`, then run the command above.
It needs no cluster, Helm chart, or built-in Kubernetes schema bundle.

To export a deployed installation's merged input:

```bash
haptic config view --input --namespace haptic > config.yaml
haptic validate --file ./config.yaml --schema-dir ./schemas
```

The export reads the configured cluster. Treat it as configuration data; avoid
including credentials or private resource contents in shared prompts or logs.

## Assertions

Define `validationTests` alongside the templates. Fixture keys must match
`watchedResources` aliases. `_global` contributes shared fixtures and context;
its assertions don't run. Per-test `extraContext` overrides individual settings.

- Include `haproxy_valid` for successful renders and assert the requested output.
- Use `contains` and `not_contains` with regular expressions, escaping literal
  punctuation. Targets include `haproxy.cfg`, `map:<filename>`, `file:<filename>`,
  and `k8s:<template-name>`; check the reference before using another target.
- A deliberate `fail()` needs an assertion on `rendering_error`. Don't also
  require valid HAProxy output from that rejected render.
- Cover missing resources, unrelated resources, absent optional fields, and
  rejected values where those cases change the behavior.

Diagnose with `--test <name> --dump-rendered --verbose`, then rerun the complete
relevant suite. The browser playground uses a syntax/schema checker for
`haproxy_valid`; native `haptic validate` executes the real HAProxy binary.
Neither a successful render nor preflight proves end-to-end traffic behavior.

## Working in HAPTIC's source repository

Read its development instructions. Use `make build` for the local binary and
`./scripts/test-templates.sh` for bundled libraries; the script renders Helm before
validating and supplies the repository schemas. Don't validate raw library files
as standalone configs. `make lint audit` and `make test` are the repository gates.
Keep lint/audit sequential with tests because vendoring may rewrite dependencies.
