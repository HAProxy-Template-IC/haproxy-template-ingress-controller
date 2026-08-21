# pkg/generated

Auto-generated clients consumed by the controller.

## Overview

Everything under this directory is generated — never edit by hand. There are two unrelated halves:

| Subtree | Source | Generator |
|---------|--------|-----------|
| `clientset/` + `informers/` + `listers/` | `pkg/apis/haproxytemplate/v1alpha1/` (Go types) | `code-generator` (`make generate-clientset`) |
| `validators/` | The pinned Data Plane API OpenAPI specs under `cmd/gen-validators/spec/` | `make generate-playground-validators` |

`make generate` runs the clientset half. Run `make verify-generate` locally to
verify that CRD manifests and DeepCopy methods are up-to-date.

## The validators are playground-only

`validators/` builds only under the `playground` build tag. It is the schema
half of the check the browser playground answers `haproxy_valid` with, because
the browser has no `haproxy` binary. No production binary links it —
`scripts/check-client-native-free.sh` proves that — and HAProxy's own verdict is
a strict superset of it everywhere else (see `docs/adr/0022-haptic-agent.md`).

The specs it is generated from are pinned: HAPTIC does not speak the Data Plane
API any more, so there is nothing to track. Refresh one only if you deliberately
want the playground to check against a newer schema:

```bash
./scripts/extract-dataplane-spec.sh 3.2 cmd/gen-validators/spec/v32.json
make generate-playground-validators
```

## See Also

- [`pkg/apis`](../apis/) — the source Go types for the typed clientset
- `cmd/gen-validators` — the validator generator and its pinned specs
- `Makefile` — every `generate-*` target

## License

Apache-2.0 — see root `LICENSE`.
