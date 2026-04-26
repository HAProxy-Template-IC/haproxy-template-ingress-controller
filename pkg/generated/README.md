# pkg/generated

Auto-generated clients consumed by the controller.

## Overview

Everything under this directory is generated — never edit by hand. There are two unrelated halves:

| Subtree | Source | Generator |
|---------|--------|-----------|
| `clientset/` + `informers/` + `listers/` | `pkg/apis/haproxytemplate/v1alpha1/` (Go types) | `code-generator` (`make generate-clientset`) |
| `dataplaneapi/v3{0,1,2,3}/` + `dataplaneapi/v3{0,1,2}ee/` | DataPlane API OpenAPI spec at each subtree's `spec.json` | `oapi-codegen` (`make generate-dataplaneapi-v3<x>` / `…ee`) |
| `validators/` | The same OpenAPI specs, used for zero-allocation request validation | `make generate-validators` |

All of the above run together via `make generate`. CI runs `make verify-generate` to make sure the committed code matches the generated output for the current source.

## Refreshing the DataPlane API Clients

Bumping the OpenAPI spec for a HAProxy version is a two-step process:

```bash
./scripts/extract-dataplane-spec.sh 3.2 pkg/generated/dataplaneapi/v32/spec.json
make generate-dataplaneapi-v32
```

The Enterprise variants follow the same pattern but the extract script needs an `--enterprise` (or `-e`) flag and `r1`-suffixed versions because the spec lives in a different registry:

```bash
./scripts/extract-dataplane-spec.sh -e 3.2r1 pkg/generated/dataplaneapi/v32ee/spec.json
make generate-dataplaneapi-v32ee
```

`./scripts/extract-dataplane-spec.sh --help`-style usage is documented in the script's header — see the comment block at the top.

## See Also

- [`pkg/apis`](../apis/) — the source Go types for the typed clientset
- `scripts/extract-dataplane-spec.sh` — pulls a fresh OpenAPI spec from a running DataPlane API
- `Makefile` — every `generate-*` target

## License

Apache-2.0 — see root `LICENSE`.
