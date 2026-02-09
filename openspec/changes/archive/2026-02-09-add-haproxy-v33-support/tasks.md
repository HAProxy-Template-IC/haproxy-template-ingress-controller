## 1. Version Management and Script Fixes

- [x] 1.1 Fix `extract-dataplane-spec.sh` binary path from `/usr/bin/dataplaneapi` to `/usr/local/bin/dataplaneapi`
- [x] 1.2 Add HAProxy 3.3 to `HAPROXY_VERSIONS` in `versions.env` and add a comment noting enterprise 3.3 is pending
- [x] 1.3 Add 3.3 community case to `start-dev-env.sh`

## 2. DataPlane API v33 Client Generation

- [x] 2.1 Extract v3.3 OpenAPI spec using the fixed `extract-dataplane-spec.sh`
- [x] 2.2 Create `hack/oapi-codegen-v33.yaml` config (copy v32, change package to `v33`)
- [x] 2.3 Add `generate-dataplaneapi-v33` target to `Makefile`
- [x] 2.4 Run code generation to produce `pkg/generated/dataplaneapi/v33/api.gen.go`

## 3. Dispatcher and Clientset Extension

- [x] 3.1 Add v33 client fields, creation, and accessors to `clientset.go`
- [x] 3.2 Add V33/V33EE fields and dispatch cases to `dispatcher.go` and `dispatcher_helpers.go`
- [x] 3.3 Add v33 entries to all `pkg/dataplane/client/` storage and operation files (~15 files)
- [x] 3.4 Add v33 entries to all `pkg/dataplane/client/comparator/sections/executors/` files (~17 files)
- [x] 3.5 Add v33 generated validator to `pkg/dataplane/validators/`

## 4. Template Library Balance Consolidation

- [x] 4.1 Add `balance roundrobin` to the defaults section in `base.yaml`
- [x] 4.2 Remove hardcoded `balance roundrobin` from `ingress.yaml` backend snippet
- [x] 4.3 Remove hardcoded `balance roundrobin` from `gateway.yaml` HTTPRoute and SSL passthrough backend snippets
- [x] 4.4 Remove hardcoded `balance roundrobin` from SSL passthrough backends in `haproxytech.yaml` and `haproxy-ingress.yaml`
- [x] 4.5 Run template validation tests to verify balance behavior is preserved

## 5. Documentation and Changelog

- [x] 5.1 Add CHANGELOG entry for HAProxy 3.3 support and balance consolidation

## 6. Verification

- [x] 6.1 Run `make lint` and `make test` to verify all code compiles and passes
- [x] 6.2 Run `./scripts/test-templates.sh` to verify template rendering
