# Linting and code quality

Three checkers run against every commit:

| Tool | Config | Invoked by |
|------|--------|------------|
| `golangci-lint` | `.golangci.yml` | `make lint` |
| `arch-go` | `arch-go.yml` | `make lint` (auto-installs if missing) |
| `govulncheck` | — | `make audit` |

`make check-all` runs all three plus the full test suite — the same set of checks CI runs on every MR. `make lint-fix` applies golangci-lint's auto-fixes where possible.

## `golangci-lint`

The config is in v2 format (`version: "2"`), so formatters and linters are separated:

**Formatters** (`gofmt`, `goimports`) auto-format source on `make lint-fix`.

**Linters** (≈30 enabled, grouped below). The authoritative list is `.golangci.yml`; this page just classifies them:

| Purpose | Linters |
|---------|---------|
| Correctness | `errcheck`, `govet`, `staticcheck` (includes gosimple), `ineffassign`, `unused`, `bodyclose`, `errchkjson`, `nilerr`, `nilnil` |
| Security | `gosec` |
| Style | `revive`, `gocritic`, `misspell`, `unconvert`, `unparam`, `nakedret`, `whitespace`, `godot`, `importas`, `goprintffuncname` |
| Complexity | `gocyclo`, `goconst`, `dupl` |
| Performance | `prealloc`, `copyloopvar` |
| Hygiene | `godox` (`BUG`/`FIXME`/`HACK`), `asciicheck`, `bidichk`, `dogsled`, `makezero`, `nolintlint` |
| Tests | `thelper` |

Plus one project-local analyzer built in `tools/linters/eventimmutability` that enforces the event immutability contract — it flags assignments that mutate the fields of an event struct in `pkg/controller/events` after creation. The analyzer is compiled and invoked as part of `make lint`.

### Project rules worth knowing

- **`importas`** enforces canonical aliases for Kubernetes and haproxytech packages (`corev1`, `metav1`, `apierrors`, `corev1client`, `haproxy`). Deviations fail CI.
- **`revive`** caps function length at 50 lines and cognitive complexity at 20. `exported` and `package-comments` rules are off for internal packages.
- **`gocyclo`** rejects cyclomatic complexity > 20.
- **`gosec`** allowlists G114 (HTTP timeout — set at infra level), G204 (the only `exec` binary name is the hardcoded `"haproxy"` resolved via `LookPath`), and G404 (non-crypto random-number generation — not used for secrets). G304, G118, and G108 are allowlisted on specific file paths/messages, not globally.
- Generated code (`zz_generated.*.go`, `codegen/**/*.gen.go`, `pkg/generated/**`) and test files run with relaxed rules; see the per-path overrides at the bottom of `.golangci.yml`.

Never add a global ignore rule to silence findings, and don't suppress them with `nolint` directives on individual lines — fix the code, or add a scoped per-path exclusion in `.golangci.yml` if the rule is genuinely wrong for that file.

## arch-go

The DAG rules in `arch-go.yml` prevent the coordination layer from leaking into pure libraries. The high-level shape:

- **`pkg/controller/**`** may import anything under `pkg/` plus `codegen/`.
- **`pkg/core/**`** may not import `controller`, `dataplane`, `k8s`, `templating`, `httpstore`, `introspection`, `webhook`.
- **`pkg/events/**`** must not import any other `pkg/**` package.
- **`pkg/stores/**`** is isolated from `pkg/k8s/**`; the two declare structurally identical `Store` interfaces and `pkg/stores.TypesStoreAdapter` bridges them.
- Domain libraries (`pkg/k8s`, `pkg/dataplane`, `pkg/templating`) may not cross-import each other.

The exact allow/deny lists evolve with new packages, so consult `arch-go.yml` rather than memorising the rules.

## govulncheck

`make audit` runs govulncheck against the dependency graph. If it reports a vulnerability:

```bash
go get -u <module>@<fixed-version>
go mod tidy
make audit
```

If the finding is in the standard library, bump the Go toolchain in `.tool-versions` and verify CI images pick up the new version.

## Pre-commit hooks

`.pre-commit-config.yaml` wires `make lint` and `make audit` to run on every commit via the [pre-commit](https://pre-commit.com/) framework:

```bash
pip install pre-commit   # or: brew install pre-commit
pre-commit install       # installs .git/hooks/pre-commit
```

Once installed, they run automatically on `git commit`. To run them against the whole tree outside of a commit:

```bash
pre-commit run --all-files
```

!!! warning
    Don't bypass the hooks with `git commit --no-verify`. CI runs the same checks, so a bypassed commit just turns into a failed pipeline. If a hook fails spuriously, fix the root cause — see [CLAUDE.md](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/CLAUDE.md) for the project-wide policy.

## Tooling

Linter versions are pinned via Go's `tool` directive in `go.mod`; `make install-tools` rebuilds the local cache. The Go version is pinned in `.tool-versions` (asdf) — see `tests/README.md` for the `env -u GOROOT` note if you invoke Go commands directly.

## References

- `.golangci.yml` — authoritative linter config
- `arch-go.yml` — authoritative dependency rules
- [golangci-lint v2 docs](https://golangci-lint.run/)
- [arch-go](https://github.com/arch-go/arch-go)
- [govulncheck](https://go.dev/blog/vuln)
