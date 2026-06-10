.PHONY: help version lint lint-fix lint-chart lint-chart-ci audit check-all \
        test test-integration test-acceptance test-acceptance-parallel test-e2e test-gateway-conformance test-ingress-conformance build-integration-test \
        test-coverage test-integration-coverage test-coverage-combined bench \
        build docker-build docker-build-multiarch docker-build-multiarch-push docker-load-kind docker-push docker-clean \
        spoa-prep spoa-hub-image spoa-bundle-render spoa-bundle-check \
        tidy vendor verify verify-generate generate clean fmt vet install-tools dev \
        release-controller release-chart goreleaser-snapshot \
        pgo-profile pgo-merge \
        extract-schemas

.DEFAULT_GOAL := help

# Variables
# env -u GOROOT: strips stale GOROOT set by IDEs (e.g. IntelliJ) so the asdf-managed
# Go toolchain is used consistently across all make targets including lint and audit.
GO := env -u GOROOT go
# renovate: datasource=github-releases depName=golangci/golangci-lint
GOLANGCI_LINT_VERSION := v2.12.2
GOLANGCI_LINT := $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GOVULNCHECK := $(GO) run golang.org/x/vuln/cmd/govulncheck
ARCH_GO := $(shell which arch-go 2>/dev/null || echo "$(GO) run github.com/arch-go/arch-go/v2")
OAPI_CODEGEN := $(GO) run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen
CONTROLLER_GEN := $(GO) run sigs.k8s.io/controller-tools/cmd/controller-gen

# Docker variables
IMAGE_NAME ?= haptic# Container image name (override: IMAGE_NAME=my-image)
IMAGE_TAG ?= dev# Image tag (override: IMAGE_TAG=v1.0.0)
REGISTRY ?=# Container registry (e.g., registry.gitlab.com/myorg)
FULL_IMAGE := $(if $(REGISTRY),$(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG),$(IMAGE_NAME):$(IMAGE_TAG))
# HAProxy version baked into the controller image at build time.
# Sourced from versions.env's DEFAULT_HAPROXY (single source of truth) so
# local builds stay in lockstep with the chart's haproxyVersion default.
# Override per-build with HAPROXY_VERSION=3.x.
HAPROXY_VERSION ?= $(shell sh -c '. ./versions.env && echo $$DEFAULT_HAPROXY')
KIND_CLUSTER ?= haptic-dev  # Kind cluster name for local testing
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GIT_TAG := $(shell git describe --tags --exact-match 2>/dev/null || echo "dev")
VERSION := $(shell cat VERSION 2>/dev/null || echo "dev")
CHART_VERSION := $(shell grep '^version:' charts/haptic/Chart.yaml 2>/dev/null | awk '{print $$2}' || echo "dev")

# Coverage packages (excludes generated code)
COVERAGE_PACKAGES := ./cmd/...,./pkg/compression/...,./pkg/controller/...,./pkg/core/...,./pkg/dataplane/...,./pkg/events/...,./pkg/httpstore/...,./pkg/introspection/...,./pkg/k8s/...,./pkg/lifecycle/...,./pkg/metrics/...,./pkg/stores/...,./pkg/templating/...,./pkg/webhook/...

# Default target
help: ## Show this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-30s\033[0m %s\n", $$1, $$2}'

version: ## Display version information
	@echo "Version Information:"
	@echo "  Controller: $(VERSION)"
	@echo "  Chart:      $(CHART_VERSION)"
	@echo "  Git Commit: $(GIT_COMMIT)"
	@echo "  Git Tag:    $(GIT_TAG)"
	@echo "  Image:      $(FULL_IMAGE)"

## Linting targets

lint: vendor ## Run all linters (YAML, JSON, Markdown, Go)
	@echo "Linting YAML files..."
	yamllint -c .yamllint.yml .
	@echo "Linting JSON files..."
	@for f in renovate.json; do \
		echo "  Checking $$f..."; \
		jq empty "$$f" || exit 1; \
	done
	@echo "Linting Markdown files..."
	markdownlint-cli2 "**/*.md" "#node_modules" "#.claude" "#vendor" "#.cache"
	@echo "Running golangci-lint..."
ifdef CI
	$(GOLANGCI_LINT) run --output.code-climate.path=gl-code-quality-report.json \
		./cmd/... ./examples/... ./pkg/... ./tests/... ./tools/...
else
	$(GOLANGCI_LINT) run \
		./cmd/... ./examples/... ./pkg/... ./tests/... ./tools/...
endif
	@echo "Running arch-go..."
	$(ARCH_GO)
	@echo "Running event immutability checker..."
	@mkdir -p bin
	@cd tools/linters/eventimmutability && $(GO) build -o ../../../bin/eventimmutability ./cmd/eventimmutability
	@env -u GOROOT ./bin/eventimmutability ./...
	@echo "Verifying generated code..."
	@$(MAKE) verify-generate

lint-fix: ## Run golangci-lint with auto-fix
	@echo "Running golangci-lint with auto-fix..."
	$(GOLANGCI_LINT) run --fix ./cmd/... ./examples/... ./pkg/... ./tests/... ./tools/...

## Chart linting

# renovate: datasource=docker depName=quay.io/helmpack/chart-testing versioning=docker
CT_VERSION := v3.14.0
# renovate: datasource=docker depName=helmunittest/helm-unittest versioning=docker
HELM_UNITTEST_VERSION := 4.2.0-1.1.1
# renovate: datasource=docker depName=ghcr.io/yannh/kubeconform versioning=docker
KUBECONFORM_VERSION := v0.8.0-alpine
# renovate: datasource=docker depName=kindest/node
KUBE_VERSION := 1.36.1

lint-chart: ## Run chart linting (ct lint, helm-unittest, kubeconform) via Docker
	@echo "Running chart-testing lint..."
	docker run --rm -v $(PWD):/data -w /data quay.io/helmpack/chart-testing:$(CT_VERSION) \
		ct lint --config charts/haptic/.ct/ct.yaml --all
	@echo ""
	@echo "Running helm-unittest..."
	docker run --rm -v $(PWD)/charts/haptic:/apps \
		helmunittest/helm-unittest:$(HELM_UNITTEST_VERSION) .
	@echo ""
	@echo "Running kubeconform..."
	helm template charts/haptic \
		--api-versions=gateway.networking.k8s.io/v1/GatewayClass \
		--api-versions=gateway.networking.k8s.io/v1alpha2/TCPRoute \
		| docker run --rm -i ghcr.io/yannh/kubeconform:$(KUBECONFORM_VERSION) \
			-kubernetes-version $(KUBE_VERSION) \
			-schema-location default \
			-schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
			-skip haproxy-haptic.org/v1alpha1/HAProxyTemplateConfig,haproxy-haptic.org/v1alpha1/HAProxyConfig,haproxy-haptic.org/v1alpha1/HAProxyMapFile \
			-summary
	@echo ""
	@echo "Checking release-Secret size..."
	@$(MAKE) --no-print-directory chart-size-check
	@echo ""
	@echo "All chart linting passed!"

# CI target (runs all chart linting - tools must be installed)
lint-chart-ci: ## Run all chart linting for CI (requires ct, helm-unittest, kubeconform)
	@echo "Running chart-testing lint..."
	ct lint --config charts/haptic/.ct/ct.yaml --all
	@echo ""
	@echo "Running helm-unittest..."
	helm unittest charts/haptic --output-type JUnit --output-file chart-test-results.xml
	@echo ""
	@echo "Running kubeconform..."
	helm template charts/haptic \
		--api-versions=gateway.networking.k8s.io/v1/GatewayClass \
		--api-versions=gateway.networking.k8s.io/v1alpha2/TCPRoute \
		| kubeconform \
			-kubernetes-version $(KUBE_VERSION) \
			-schema-location default \
			-schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
			-skip haproxy-haptic.org/v1alpha1/HAProxyTemplateConfig,haproxy-haptic.org/v1alpha1/HAProxyConfig,haproxy-haptic.org/v1alpha1/HAProxyMapFile \
			-summary
	@echo ""
	@echo "Checking release-Secret size..."
	@$(MAKE) --no-print-directory chart-size-check
	@echo ""
	@echo "All chart linting passed!"

chart-size-check: ## Estimate the Helm release-Secret size; fail if it nears the 1 MiB Secret limit
	@# Renders the WORST-CASE profile (every bundled library enabled, Gateway
	@# CRDs present) and estimates the base64(gzip(json(release))) payload Helm
	@# stores in its release Secret. The hard 1,048,576-byte Secret limit caused
	@# a silent e2e/install failure once the chart approached it (chart MR !1105);
	@# this gate catches a regression in `make`/CI BEFORE an install fails. The
	@# estimator reconstructs Helm's release object offline (subchart source is
	@# NOT stored), accurate to ~2% against real installs — see the script header.
	@# nginxIngress + spoaHub are the two default-disabled libraries; enabling
	@# both makes the worst case explicit (spoaHub already merges by default via
	@# its OR-helper, so the flag is belt-and-suspenders against a future change).
	@python3 scripts/check-chart-release-size.py charts/haptic \
		--set controller.templateLibraries.nginxIngress.enabled=true \
		--set controller.templateLibraries.spoaHub.enabled=true

## Security & vulnerability scanning

audit: vendor ## Run security vulnerability scanning
	@echo "Running govulncheck..."
	$(GOVULNCHECK) ./...

## Combined checks

check-all: lint audit test ## Run all checks (linting, security, tests)
	@echo "✓ All checks passed!"

## Testing

test: ## Run tests
	@echo "Running tests..."
	$(GO) tool gotestsum --junitfile report.xml --format testname -- -race -cover ./...

test-integration: ## Run integration tests (requires kind cluster)
	@echo "Running integration tests..."
	@echo "Environment variables:"
	@echo "  KIND_NODE_IMAGE    - Kind node image (default: kindest/node:v1.32.0)"
	@echo "  KEEP_CLUSTER       - Keep cluster after tests (default: true)"
	@echo "  TEST_RUN_PATTERN   - Run specific tests matching pattern"
ifdef TEST_RUN_PATTERN
	@echo "Running tests matching pattern: $(TEST_RUN_PATTERN)"
	$(GO) tool gotestsum --junitfile report-integration.xml --format testname -- -tags=integration -v -race -timeout 15m -run "$(TEST_RUN_PATTERN)" ./tests/integration
else
	$(GO) tool gotestsum --junitfile report-integration.xml --format testname -- -tags=integration -v -race -timeout 15m ./tests/integration/...
endif

test-acceptance: docker-build-test ## Run acceptance tests (builds image, creates kind cluster)
	@echo "Running acceptance tests..."
	@echo "Note: This will create a kind cluster and may take several minutes"
	@echo "Environment variables:"
	@echo "  KIND_NODE_IMAGE - Kind node image (default: kindest/node:v1.32.0)"
	$(GO) test -tags=acceptance -v -timeout 15m ./tests/acceptance/...

test-acceptance-parallel: docker-build-test ## Run acceptance tests in parallel (faster, shared cluster)
	@echo "Running acceptance tests in parallel..."
	@echo "Note: Tests share a single Kind cluster with namespace isolation"
	@echo "Environment variables:"
	@echo "  KIND_NODE_IMAGE - Kind node image (default: kindest/node:v1.32.0)"
	@echo "  PARALLEL        - Max concurrent tests (default: 4)"
	$(GO) test -tags=acceptance -v -timeout 30m -parallel $${PARALLEL:-4} -run TestAllAcceptanceParallel ./tests/acceptance/...

CONFORMANCE_IMAGE ?= haptic-conformance-test:latest
CONFORMANCE_KIND_NETWORK ?= kind
CONFORMANCE_KIND_CLUSTER ?= haptic-e2e
CONFORMANCE_TIMEOUT ?= 30m

# Ingress conformance variables. The upstream
# kubernetes-sigs/ingress-controller-conformance project is dormant
# (last commit 2023-08-28, no releases, single maintainer); we pin to
# a specific SHA and never auto-follow master. Bumping the SHA is a
# deliberate, code-reviewed change.
INGRESS_CONFORMANCE_IMAGE ?= haptic-ingress-conformance-test:latest
INGRESS_CONFORMANCE_REPO ?= https://github.com/kubernetes-sigs/ingress-controller-conformance.git
INGRESS_CONFORMANCE_SHA ?= d920ed36a0076e169a9a329a850844ab3a695ae8

test-gateway-conformance: ## Run upstream Gateway API conformance suite as a sibling container on the kind network
	@echo "Running Gateway API conformance suite against the $(CONFORMANCE_KIND_CLUSTER) cluster..."
	@echo "Note: this expects 'make test-e2e' to have provisioned the kind cluster"
	@echo "      and left it running (KEEP_CLUSTER=true is the default)."
	@echo "Environment variables:"
	@echo "  TEST_RUN_PATTERN - Run a subset of conformance tests matching the pattern"
	@echo "                     (forwarded as -test.run); empty = full suite."
	@echo "  CONFORMANCE_IMAGE - Image tag for the test binary (default: $(CONFORMANCE_IMAGE))"
	@echo "  CONFORMANCE_KIND_NETWORK - Docker network to attach the test container to"
	@echo "                             (default: $(CONFORMANCE_KIND_NETWORK) — kind's default)"
	@echo "  CONFORMANCE_KIND_CLUSTER - kind cluster name (default: $(CONFORMANCE_KIND_CLUSTER))"
	@echo "  CONFORMANCE_DEBUG - non-empty for upstream RoundTripper debug logging"
	@# Architecture: the conformance test binary is built statically here on
	@# the host (CGO_ENABLED=0), packaged into a tiny distroless image, and
	@# run as a sibling container on the kind docker network. From that
	@# vantage point Gateway.Status MetalLB IPs are directly routable, so
	@# the stock upstream RoundTripper handles every dial — no NodePort
	@# tunnel, no DinD remap, no CustomDialContext. Same code path locally
	@# and under GitLab's docker:dind, the only thing that changes is which
	@# docker daemon owns the network (host daemon vs DinD's nested daemon).
	@echo "Building conformance test binary (gateway_conformance tag)..."
	@# Static binary: distroless-static has no libc / dynamic loader.
	CGO_ENABLED=0 $(GO) test -mod=mod -tags=gateway_conformance -c -o /tmp/haptic-conformance.test ./tests/conformance/
	@echo "Resolving kind apiserver kubeconfig (--internal, for container-network DNS)..."
	@# The kubeconfig is BAKED INTO the image rather than bind-mounted because
	@# bind mounts don't cross the DinD boundary — `docker run -v src:dst`
	@# resolves `src` on the DinD daemon's filesystem, not the GitLab job
	@# container's, and the daemon doesn't have it. Baking works in both
	@# environments and per-cluster image churn is acceptable (we rebuild
	@# per `make test-e2e` anyway).
	@echo "Packaging into $(CONFORMANCE_IMAGE)..."
	@# Build with a minimal context so the daemon isn't asked to upload the
	@# whole repo (which is a few hundred MB and pointless — Dockerfile.
	@# conformance-test only COPYs the test binary + kubeconfig).
	@rm -rf /tmp/haptic-conformance-build
	@mkdir -p /tmp/haptic-conformance-build
	cp /tmp/haptic-conformance.test /tmp/haptic-conformance-build/
	cp Dockerfile.conformance-test /tmp/haptic-conformance-build/Dockerfile
	kind get kubeconfig --internal --name=$(CONFORMANCE_KIND_CLUSTER) > /tmp/haptic-conformance-build/kubeconfig
	docker build -t $(CONFORMANCE_IMAGE) /tmp/haptic-conformance-build
	@rm -rf /tmp/haptic-conformance-build
	@echo "Running conformance suite..."
	docker run \
		--rm \
		--network $(CONFORMANCE_KIND_NETWORK) \
		$(if $(CONFORMANCE_DEBUG),-e CONFORMANCE_DEBUG=$(CONFORMANCE_DEBUG)) \
		$(CONFORMANCE_IMAGE) \
		-test.v -test.timeout=$(CONFORMANCE_TIMEOUT) \
		$(if $(TEST_RUN_PATTERN),-test.run "$(TEST_RUN_PATTERN)")

build-ingress-conformance-image: ## Build the ingress-conformance test image (clone upstream, apply patches, compile, docker build)
	@echo "Building ingress-conformance test image $(INGRESS_CONFORMANCE_IMAGE)..."
	@echo "Note: upstream kubernetes-sigs/ingress-controller-conformance is dormant"
	@echo "      (last commit 2023-08-28, no releases). Pinned to SHA:"
	@echo "      $(INGRESS_CONFORMANCE_SHA). We do NOT auto-follow master."
	@echo "Environment variables:"
	@echo "  INGRESS_CONFORMANCE_IMAGE - Image tag for the test image"
	@echo "                              (default: $(INGRESS_CONFORMANCE_IMAGE))"
	@echo "  INGRESS_CONFORMANCE_REPO  - Upstream git repo URL"
	@echo "                              (default: $(INGRESS_CONFORMANCE_REPO))"
	@echo "  INGRESS_CONFORMANCE_SHA   - Upstream commit SHA to build"
	@echo "                              (default: $(INGRESS_CONFORMANCE_SHA))"
	@echo "  CONFORMANCE_KIND_CLUSTER  - kind cluster name (default: $(CONFORMANCE_KIND_CLUSTER))"
	@echo "                              Only used when CONFORMANCE_BAKE_KUBECONFIG=1."
	@echo "  CONFORMANCE_BAKE_KUBECONFIG - When =1, bake the kind cluster's kubeconfig"
	@echo "                              into the image (legacy local-dev shape). Default"
	@echo "                              =0: image carries no kubeconfig and the runner"
	@echo "                              must bind-mount one at /etc/kubeconfig. CI sets"
	@echo "                              =0 because the cluster doesn't exist at"
	@echo "                              image-build time."
	@# Architecture: identical to test-gateway-conformance. Two binaries
	@# get baked into a distroless image: our Go test wrapper (built with
	@# the ingress_conformance tag, exec's the upstream and parses its
	@# Cucumber JSON into go-test subtests) and the upstream binary
	@# itself (built from a git clone at the pinned SHA, the same way
	@# upstream's own Makefile builds it). The container runs as a
	@# sibling on the kind docker network so it can reach the apiserver
	@# by its docker-DNS hostname.
	@#
	@# Split out from test-ingress-conformance so CI can build once per
	@# pipeline and share the image across the parallel:N shards instead
	@# of every shard re-cloning, re-patching, and re-compiling upstream
	@# (~150s wasted per extra shard). Local `make test-ingress-conformance`
	@# still triggers a build via the dependency edge below.
	@echo "Building wrapper binary (ingress_conformance tag)..."
	CGO_ENABLED=0 $(GO) test -mod=mod -tags=ingress_conformance -c -o /tmp/haptic-ingress-conformance.test ./tests/conformance/
	@echo "Cloning upstream at $(INGRESS_CONFORMANCE_SHA)..."
	@rm -rf /tmp/haptic-ingress-conformance-upstream
	git clone --quiet $(INGRESS_CONFORMANCE_REPO) /tmp/haptic-ingress-conformance-upstream
	git -C /tmp/haptic-ingress-conformance-upstream -c advice.detachedHead=false checkout --quiet $(INGRESS_CONFORMANCE_SHA)
	@echo "Applying vendored patches (see tests/conformance/patches/README)..."
	@# Upstream PR #101 (convergence retry) — vendored because upstream is
	@# dormant and never merged it. Cilium carries the same patch in its
	@# fork. Without this, single-shot HTTP requests lose to the inherent
	@# K8s-Endpoints→runtime-config-push race that every Ingress controller
	@# has a non-zero gap on.
	@for patch in tests/conformance/patches/*.patch; do \
	  echo "  applying $$patch"; \
	  git -C /tmp/haptic-ingress-conformance-upstream apply --verbose "$(CURDIR)/$$patch" || \
	    { echo "patch $$patch failed to apply — re-resolve against $(INGRESS_CONFORMANCE_SHA)"; exit 1; }; \
	done
	@echo "Building upstream ingress-controller-conformance binary..."
	@# Mirror upstream's own Makefile flags (-trimpath, ldflags). Running
	@# `go test -c` from inside the cloned tree uses the upstream's
	@# go.mod, not haptic's — that's exactly what we want for the pin
	@# to be honored end-to-end.
	cd /tmp/haptic-ingress-conformance-upstream && CGO_ENABLED=0 $(GO) test -c -trimpath -ldflags="-buildid= -w" -o /tmp/ingress-controller-conformance .
	@echo "Packaging into $(INGRESS_CONFORMANCE_IMAGE)..."
	@rm -rf /tmp/haptic-ingress-conformance-build
	@mkdir -p /tmp/haptic-ingress-conformance-build
	cp /tmp/haptic-ingress-conformance.test /tmp/haptic-ingress-conformance-build/
	cp /tmp/ingress-controller-conformance /tmp/haptic-ingress-conformance-build/
	cp -r /tmp/haptic-ingress-conformance-upstream/features /tmp/haptic-ingress-conformance-build/features
	cp Dockerfile.ingress-conformance-test /tmp/haptic-ingress-conformance-build/Dockerfile
	@# Kubeconfig handling. Local `make test-ingress-conformance` runs
	@# right after `make test-e2e`, so the kind cluster exists and we
	@# can bake its kubeconfig — same shape as
	@# Dockerfile.conformance-test. CI builds this image in a separate,
	@# earlier job (before the cluster exists), so it sets
	@# CONFORMANCE_BAKE_KUBECONFIG=0 and the test job mounts a
	@# kubeconfig into the container at run time. The Dockerfile's
	@# `COPY kubeconfig /etc/kubeconfig` would otherwise fail the build
	@# when the file isn't present; we materialise an empty stub so the
	@# COPY succeeds and let runtime overrides supersede it.
	@if [ "$(CONFORMANCE_BAKE_KUBECONFIG)" = "1" ]; then \
	  echo "Baking kubeconfig for kind cluster $(CONFORMANCE_KIND_CLUSTER)..."; \
	  kind get kubeconfig --internal --name=$(CONFORMANCE_KIND_CLUSTER) > /tmp/haptic-ingress-conformance-build/kubeconfig; \
	else \
	  echo "Skipping kubeconfig bake (CONFORMANCE_BAKE_KUBECONFIG!=1); runner must mount one."; \
	  : > /tmp/haptic-ingress-conformance-build/kubeconfig; \
	fi
	docker build -t $(INGRESS_CONFORMANCE_IMAGE) /tmp/haptic-ingress-conformance-build
	@rm -rf /tmp/haptic-ingress-conformance-build /tmp/haptic-ingress-conformance-upstream

test-ingress-conformance: ## Run upstream Kubernetes Ingress conformance suite as a sibling container on the kind network
	@echo "Running Ingress conformance suite against the $(CONFORMANCE_KIND_CLUSTER) cluster..."
	@echo "Note: this expects 'make test-e2e' to have provisioned the kind cluster"
	@echo "      and left it running (KEEP_CLUSTER=true is the default)."
	@echo "Environment variables:"
	@echo "  TEST_RUN_PATTERN - Run a subset of conformance scenarios matching the pattern"
	@echo "                     (forwarded as -test.run); empty = full suite."
	@echo "  SHARD_ID / SHARD_COUNT - Run only the assigned slice of the suite. Set by"
	@echo "                     GitLab CI's parallel:N keyword (CI_NODE_INDEX/CI_NODE_TOTAL)."
	@echo "                     Local dev: unset = full suite (the default)."
	@echo "  CONFORMANCE_IMAGE_PREBUILT - When =1, skip the build chain. The image must"
	@echo "                     already be tagged $(INGRESS_CONFORMANCE_IMAGE) locally"
	@echo "                     (CI pulls + retags from the registry before this target)."
	@echo "  CONFORMANCE_KIND_NETWORK  - Docker network for the test container"
	@echo "                              (default: $(CONFORMANCE_KIND_NETWORK))"
	@echo "  CONFORMANCE_KIND_CLUSTER  - kind cluster name"
	@echo "                              (default: $(CONFORMANCE_KIND_CLUSTER))"
	@if [ "$(CONFORMANCE_IMAGE_PREBUILT)" != "1" ]; then \
	  echo "Building $(INGRESS_CONFORMANCE_IMAGE) locally (set CONFORMANCE_IMAGE_PREBUILT=1 to skip)..."; \
	  $(MAKE) build-ingress-conformance-image CONFORMANCE_BAKE_KUBECONFIG=1; \
	else \
	  echo "Layering kind kubeconfig onto prebuilt $(INGRESS_CONFORMANCE_IMAGE)..."; \
	  : "# Bind-mounts don't cross the DinD boundary (the host path"; \
	  : "# resolves on the GitLab job's filesystem, not the DinD daemon's"; \
	  : "# — see Dockerfile.ingress-conformance-test header). The CI"; \
	  : "# build job builds with an empty kubeconfig stub because the"; \
	  : "# kind cluster doesn't exist yet at that point; here we layer"; \
	  : "# the real kubeconfig on top with a single COPY (cached base"; \
	  : "# image = sub-second rebuild)."; \
	  rm -rf /tmp/haptic-ingress-conformance-rebake; \
	  mkdir -p /tmp/haptic-ingress-conformance-rebake; \
	  kind get kubeconfig --internal --name=$(CONFORMANCE_KIND_CLUSTER) > /tmp/haptic-ingress-conformance-rebake/kubeconfig; \
	  printf 'FROM %s\nCOPY kubeconfig /etc/kubeconfig\n' "$(INGRESS_CONFORMANCE_IMAGE)" > /tmp/haptic-ingress-conformance-rebake/Dockerfile; \
	  docker build -t $(INGRESS_CONFORMANCE_IMAGE) /tmp/haptic-ingress-conformance-rebake; \
	  rm -rf /tmp/haptic-ingress-conformance-rebake; \
	fi
	@echo "Running conformance suite..."
	docker run \
		--rm \
		--network $(CONFORMANCE_KIND_NETWORK) \
		$(if $(SHARD_ID),-e SHARD_ID=$(SHARD_ID)) \
		$(if $(SHARD_COUNT),-e SHARD_COUNT=$(SHARD_COUNT)) \
		$(INGRESS_CONFORMANCE_IMAGE) \
		-test.v -test.timeout=$(CONFORMANCE_TIMEOUT) \
		$(if $(TEST_RUN_PATTERN),-test.run "$(TEST_RUN_PATTERN)")

test-e2e: $(if $(SKIP_DOCKER_BUILD),,docker-build-test) ## Run full-stack e2e tests (self-contained — kind + helm install + fixtures)
	@echo "Running e2e tests..."
	@# The chart composes its image tag as "<image.tag>-haproxy<haproxyVersion>".
	@# docker-build-test produces "haptic:test" (with HAProxy $(HAPROXY_VERSION)
	@# bundled, sourced from versions.env). Tag with the suffix the chart looks
	@# for so the chart's auto-matching contract holds end-to-end.
	@# CI sets SKIP_DOCKER_BUILD=1 and pre-tags from the registry-pulled image,
	@# so this re-tag is a no-op there.
	docker tag haptic:test haptic:test-haproxy$(HAPROXY_VERSION) 2>/dev/null || true
	@echo "Note: This creates kind cluster 'haptic-e2e', helm-installs the chart, deploys fixtures."
	@echo "Environment variables:"
	@echo "  KEEP_CLUSTER        - Keep cluster after tests (default: true; set false to destroy)"
	@echo "  KEEP_NAMESPACE      - Keep test namespaces after failure for debugging (default: false)"
	@echo "  SKIP_CLUSTER_CREATE - CI mode: assume cluster already exists; skip kind create"
	@echo "  SKIP_DOCKER_BUILD   - CI mode: assume haptic:test-haproxyX.Y already loaded"
	@echo "  TEST_RUN_PATTERN    - Run specific tests matching pattern"
	@echo "  PARALLEL            - Max concurrent tests. Defaults to Go's default (GOMAXPROCS,"
	@echo "                        i.e. nproc), which auto-scales to the host. Verified stable"
	@echo "                        from 4 up through 16 on a 16-core box. Override with"
	@echo "                        PARALLEL=N for constrained environments"
ifdef TEST_RUN_PATTERN
	HAPTIC_HAPROXY_VERSION=$(HAPROXY_VERSION) $(GO) test -mod=mod -tags=e2e -v -timeout 30m $(if $(PARALLEL),-parallel $(PARALLEL)) -run "$(TEST_RUN_PATTERN)" ./tests/e2e/...
else
	HAPTIC_HAPROXY_VERSION=$(HAPROXY_VERSION) $(GO) test -mod=mod -tags=e2e -v -timeout 30m $(if $(PARALLEL),-parallel $(PARALLEL)) ./tests/e2e/...
endif

build-integration-test: ## Build integration test binary (without running)
	@echo "Building integration test binary..."
	@mkdir -p bin
	$(GO) test -c -o bin/integration.test ./tests/integration/...

test-coverage: ## Run unit tests with coverage report
	@echo "Running unit tests with coverage..."
	$(GO) test -race -coverprofile=coverage.out -covermode=atomic -coverpkg=$(COVERAGE_PACKAGES) ./pkg/...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated at coverage.html"

test-integration-coverage: ## Run integration tests with coverage (requires kind cluster)
	@echo "Running integration tests with coverage..."
	@mkdir -p coverage
	$(GO) test -tags=integration -race -timeout 15m -coverprofile=coverage/integration.out -covermode=atomic -coverpkg=$(COVERAGE_PACKAGES) ./tests/integration/...
	@echo "Integration coverage report generated at coverage/integration.out"

test-coverage-combined: ## Run unit and integration tests with combined coverage
	@echo "Running combined coverage (unit + integration tests)..."
	@mkdir -p coverage
	$(GO) test -race -coverprofile=coverage/unit.out -covermode=atomic -coverpkg=$(COVERAGE_PACKAGES) ./pkg/...
	$(GO) test -tags=integration -race -timeout 15m -coverprofile=coverage/integration.out -covermode=atomic -coverpkg=$(COVERAGE_PACKAGES) ./tests/integration/...
	@echo "Merging coverage profiles..."
	$(GO) run github.com/wadey/gocovmerge@latest coverage/unit.out coverage/integration.out > coverage/combined.out
	$(GO) tool cover -func=coverage/combined.out
	$(GO) tool cover -html=coverage/combined.out -o coverage/combined.html
	@echo "Combined coverage report generated at coverage/combined.html"

bench: ## Run benchmarks (usage: make bench PKG=./pkg/templating/ BENCH=BenchmarkVMPool COUNT=6)
	@echo "Running benchmarks..."
	$(GO) tool gotestsum --format testname -- \
		-run='^$$' \
		-bench=$${BENCH:-'.'} \
		-benchmem \
		-count=$${COUNT:-1} \
		-timeout=$${TIMEOUT:-5m} \
		$${PKG:-./...}

## Schema extraction (for offline validate)

# SCHEMA_DIR is where extract-schemas writes its output. The default
# matches the path the chart's tests reference; operators can override
# to populate any local directory they later pass to
# `haptic-controller validate --schema-dir=<path>`.
SCHEMA_DIR ?= tests/schemas

extract-schemas: ## Extract CustomResourceDefinitions into $(SCHEMA_DIR) for offline `validate --schema-dir`
	@echo "Extracting schemas to $(SCHEMA_DIR)/..."
	@mkdir -p $(SCHEMA_DIR)
	@# Chart-bundled CRDs first — the haptic project's own CRDs
	@# (haproxytemplateconfigs, haproxycfgs, the auxiliary file
	@# CRDs) are always relevant for chart authors who validate
	@# templates that reference them.
	@if [ -d charts/haptic/crds ]; then \
		find charts/haptic/crds -name '*.yaml' -exec cp -v {} $(SCHEMA_DIR)/ \; ; \
	fi
	@# Upstream Gateway API CRDs from the module cache. Picks up
	@# whichever release go.mod is currently on, so the extracted
	@# schemas track the build's actual dependency version. The
	@# Dir field on `go list -m -json` is unpopulated for modules
	@# that aren't vendored, so we reconstruct the path from
	@# GOMODCACHE + module@version explicitly.
	@# Upstream Gateway API resources include a ValidatingAdmissionPolicy
	@# alongside the CRDs (gateway.networking.k8s.io_vap_safeupgrades.yaml).
	@# Skip non-CRDs at extraction time so the output directory is
	@# CRD-only and clean for downstream tools. DirFetcher would also
	@# silently skip it at load time, but operators get a tidier
	@# directory listing this way.
	@# `-mod=mod` reads the version from go.mod directly, bypassing any
	@# vendor/modules.txt drift. extract-schemas pulls schemas from
	@# GOMODCACHE (not vendor/), so vendor-side inconsistencies are
	@# irrelevant to the extraction itself — but the default `go list -m`
	@# mode validates the active modules graph and exits non-zero on a
	@# checkout where the last `go mod vendor` ran before the most recent
	@# `go.mod` edit (transient between a Renovate dep bump and the next
	@# vendor sync). Without -mod=mod, version detection silently fails
	@# and the target emits the haptic CRDs only, surfacing as a
	@# confusing "where are the Gateway API schemas?" moment downstream.
	@gw_api_version="$$($(GO) list -mod=mod -m -f '{{.Version}}' sigs.k8s.io/gateway-api 2>/dev/null || true)"; \
	gw_api_dir="$$($(GO) env GOMODCACHE)/sigs.k8s.io/gateway-api@$${gw_api_version}"; \
	if [ -n "$$gw_api_version" ] && [ -d "$$gw_api_dir/config/crd/standard" ]; then \
		for f in $$gw_api_dir/config/crd/standard/gateway.networking.k8s.io_*.yaml; do \
			kind="$$(head -3 "$$f" | grep '^kind:' | awk '{print $$2}')"; \
			if [ "$$kind" = "CustomResourceDefinition" ]; then \
				cp -v "$$f" $(SCHEMA_DIR)/ ; \
			fi \
		done \
	fi
	@echo
	@echo "Schemas written to $(SCHEMA_DIR)/. Usage:"
	@echo "  haptic-controller validate -f config.yaml --schema-dir=$(SCHEMA_DIR)"
	@echo "  HAPTIC_SCHEMA_DIR=$(SCHEMA_DIR) haptic-controller validate -f config.yaml"

validate-helm-libraries: build ## Render the chart and run `controller validate` against the merged HAProxyTemplateConfig (thin wrapper around scripts/test-templates.sh)
	@# Smoke-tests that the chart's bundled libraries merge cleanly and
	@# that the resulting config passes the controller's offline
	@# validate path — engine compile + chart validationTests. Used by
	@# CI (.validate-helm-libraries-base in .gitlab-ci.yml) and by chart
	@# authors who want to reproduce the CI check locally.
	@#
	@# Delegates to scripts/test-templates.sh so the render-and-validate
	@# flow has one source of truth — every flag/path/library decision
	@# lives in the script. HAPROXY_VERSION is forwarded via env so CI's
	@# per-version matrix renders with the matching haproxyVersion
	@# value.
	@HAPROXY_VERSION=$(HAPROXY_VERSION) bash scripts/test-templates.sh

## Build targets

build: ## Build the controller binary for local development (with PGO if profile exists)
	@echo "Building controller..."
	@echo "  Version: $(VERSION)"
	@echo "  Git commit: $(GIT_COMMIT)"
	@if [ -f cmd/controller/default.pgo ]; then echo "  PGO: enabled (using cmd/controller/default.pgo)"; else echo "  PGO: disabled (no profile found)"; fi
	@mkdir -p bin
	$(GO) build \
		-pgo=auto \
		-ldflags="-X main.version=$(VERSION) -X main.commit=$(GIT_COMMIT) -X main.date=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)" \
		-o bin/haptic-controller \
		./cmd/controller

build-for-docker: ## Build binary in platform-structured path for Docker builds with --build-context
	@echo "Building controller for Docker..."
	@echo "  Version: $(VERSION)"
	@echo "  Platform: linux/amd64"
	@mkdir -p dist/linux/amd64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build \
		-trimpath \
		-buildvcs=false \
		-ldflags="-s -w -X main.version=$(VERSION) -X main.commit=$(GIT_COMMIT)" \
		-o dist/linux/amd64/haptic-controller \
		./cmd/controller
	@echo "✓ Binary built: dist/linux/amd64/haptic-controller"
	@echo "  Use with: docker buildx build --platform linux/amd64 --build-context binary=dist --target runtime ..."

## Docker targets

docker-build: ## Build Docker image
	@echo "Building Docker image: $(FULL_IMAGE)"
	@echo "  Git commit:      $(GIT_COMMIT)"
	@echo "  Git tag:         $(GIT_TAG)"
	@echo "  HAProxy version: $(HAPROXY_VERSION) (from versions.env)"
	DOCKER_BUILDKIT=1 docker build \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg GIT_TAG=$(GIT_TAG) \
		--build-arg HAPROXY_VERSION=$(HAPROXY_VERSION) \
		-t $(FULL_IMAGE) \
		.
	@echo "✓ Image built: $(FULL_IMAGE)"

docker-build-test: ## Build Docker image with test tag for acceptance tests
	IMAGE_TAG=test $(MAKE) docker-build

docker-build-multiarch: ## Build multi-platform Docker image for local testing (linux/amd64 only)
	@echo "Building multi-platform Docker image: $(FULL_IMAGE)"
	@echo "  Platform: linux/amd64 (single platform for local load)"
	@echo "  Git commit: $(GIT_COMMIT)"
	@echo "  Git tag: $(GIT_TAG)"
	DOCKER_BUILDKIT=1 docker buildx build \
		--platform linux/amd64 \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg GIT_TAG=$(GIT_TAG) \
		--load \
		-t $(FULL_IMAGE) \
		.
	@echo "✓ Multi-platform image built and loaded: $(FULL_IMAGE)"

docker-build-multiarch-push: ## Build and push multi-platform Docker image (linux/amd64,linux/arm64)
	@if [ -z "$(REGISTRY)" ]; then \
		echo "Error: REGISTRY variable must be set for multi-arch push"; \
		echo "Example: make docker-build-multiarch-push REGISTRY=registry.gitlab.com/myorg"; \
		exit 1; \
	fi
	@echo "Building and pushing multi-platform Docker image: $(FULL_IMAGE)"
	@echo "  Platforms: linux/amd64,linux/arm64"
	@echo "  Git commit: $(GIT_COMMIT)"
	@echo "  Git tag: $(GIT_TAG)"
	DOCKER_BUILDKIT=1 docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg GIT_TAG=$(GIT_TAG) \
		--push \
		-t $(FULL_IMAGE) \
		.
	@echo "✓ Multi-platform image pushed: $(FULL_IMAGE)"

docker-load-kind: docker-build ## Build Docker image and load into kind cluster
	@echo "Loading image into kind cluster: $(KIND_CLUSTER)"
	@if ! kind get clusters 2>/dev/null | grep -q "^$(KIND_CLUSTER)$$"; then \
		echo "Error: Kind cluster '$(KIND_CLUSTER)' not found"; \
		echo "Available clusters:"; \
		kind get clusters 2>/dev/null || echo "  (none)"; \
		exit 1; \
	fi
	kind load docker-image $(FULL_IMAGE) --name $(KIND_CLUSTER)
	@echo "✓ Image loaded into kind cluster: $(KIND_CLUSTER)"

docker-push: docker-build ## Build and push Docker image to registry
	@if [ -z "$(REGISTRY)" ]; then \
		echo "Error: REGISTRY variable must be set"; \
		echo "Example: make docker-push REGISTRY=registry.gitlab.com/myorg"; \
		exit 1; \
	fi
	@echo "Pushing Docker image: $(FULL_IMAGE)"
	docker push $(FULL_IMAGE)
	@echo "✓ Image pushed: $(FULL_IMAGE)"

docker-clean: ## Remove Docker images and build cache
	@echo "Removing Docker images..."
	-docker rmi $(IMAGE_NAME):$(IMAGE_TAG) 2>/dev/null || true
	@if [ -n "$(REGISTRY)" ]; then \
		docker rmi $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG) 2>/dev/null || true; \
	fi
	@echo "Pruning build cache..."
	-docker builder prune -f
	@echo "✓ Docker cleanup complete"

## SPOA hub image targets

spoa-prep: ## Download and verify plugin .so files into plugins/<arch>/ (uses cosign)
	@bash scripts/prep-spoa-plugins.sh

spoa-bundle-render: ## Render docs/.../spoa-hub.md bundled-versions table from versions-spoa.env
	@bash scripts/render-spoa-bundle.sh

spoa-bundle-check: ## Verify docs/.../spoa-hub.md is in sync with versions-spoa.env (CI guard)
	@bash scripts/render-spoa-bundle.sh --check

spoa-hub-image: spoa-prep ## Build spoa-hub image locally (single-arch amd64, tagged spoa-hub:dev)
	@set -a; . ./versions-spoa.env; set +a; \
	HUB_TAG="$${SPOA_HUB_VERSION#v}"; \
	echo "Building spoa-hub:dev (linux/amd64, FROM hub $$HUB_TAG)"; \
	docker buildx build \
		--platform linux/amd64 \
		--build-arg "SPOA_HUB_VERSION=$$HUB_TAG" \
		--build-context plugins=plugins \
		--load \
		-f Dockerfile.spoa-hub \
		-t spoa-hub:dev \
		.
	@echo "Built spoa-hub:dev"

## Dependency management

tidy: ## Run go mod tidy
	@echo "Running go mod tidy..."
	$(GO) mod tidy

vendor: ## Sync vendor directory with go.mod (auto-runs before lint/audit)
	@$(GO) mod vendor

verify: ## Verify dependencies
	@echo "Verifying dependencies..."
	$(GO) mod verify

verify-generate: ## Verify generated code (CRDs, DeepCopy) is up-to-date
	@echo "Verifying generated code is up-to-date..."
	@$(MAKE) generate-crds generate-deepcopy
	@if ! git diff --quiet --exit-code -- charts/haptic/crds/ 'pkg/apis/**/zz_generated.*.go'; then \
		echo ""; \
		echo "ERROR: Generated files are out of date:"; \
		git diff --stat -- charts/haptic/crds/ 'pkg/apis/**/zz_generated.*.go'; \
		echo ""; \
		echo "Run 'make generate-crds generate-deepcopy' and commit the result."; \
		git checkout -- charts/haptic/crds/ 'pkg/apis/**/zz_generated.*.go'; \
		exit 1; \
	fi
	@echo "✓ Generated code is up-to-date"

## Code generation

generate: generate-crds generate-deepcopy generate-clientset generate-dataplaneapi-all generate-validators ## Run all code generation

generate-crds: ## Generate CRD manifests from Go types
	@echo "Generating CRD manifests..."
	@mkdir -p charts/haptic/crds
	$(CONTROLLER_GEN) crd:crdVersions=v1 \
		paths=./pkg/apis/haproxytemplate/v1alpha1/... \
		output:crd:dir=./charts/haptic/crds/
	@echo "✓ CRD manifests generated in charts/haptic/crds/"

generate-deepcopy: ## Generate DeepCopy methods for API types
	@echo "Generating DeepCopy methods..."
	$(CONTROLLER_GEN) object:headerFile=hack/boilerplate.go.txt \
		paths=./pkg/apis/haproxytemplate/v1alpha1/...
	@echo "✓ DeepCopy methods generated"

generate-clientset: ## Generate Kubernetes clientset, informers, and listers
	@echo "Generating Kubernetes clientset, informers, and listers..."
	./hack/update-codegen.sh
	@echo "✓ Clientset, informers, and listers generated"

# DataPlane API client versions, derived from versions.env: one entry per
# HAPROXY_VERSIONS value (community), plus one per value that also has a matching
# HAPROXY_ENTERPRISE_<n> (enterprise). Adding or removing a HAProxy version is
# therefore a versions.env edit (plus its spec.json and oapi-codegen-<v>.yaml).
DATAPLANE_API_VERSIONS := $(shell sh -c '. ./versions.env; \
	for v in $$HAPROXY_VERSIONS; do printf "v%s " "$$(echo $$v | tr -d .)"; done; \
	for v in $$HAPROXY_VERSIONS; do n=$$(echo $$v | tr -d .); eval "ee=\$$HAPROXY_ENTERPRISE_$$n"; [ -n "$$ee" ] && printf "v%see " "$$n"; done')

generate-dataplaneapi-all: ## Generate all HAProxy DataPlane API clients (community + enterprise)
	@set -e; for v in $(DATAPLANE_API_VERSIONS); do \
		echo "Generating DataPlane API $$v client (models + client)..."; \
		mkdir -p pkg/generated/dataplaneapi/$$v; \
		$(OAPI_CODEGEN) -config hack/oapi-codegen-$$v.yaml pkg/generated/dataplaneapi/$$v/spec.json; \
	done
	@echo "✓ All DataPlane API clients generated (community + enterprise)"

generate-validators: ## Generate zero-allocation OpenAPI validators
	@echo "Generating zero-allocation validators..."
	go run ./cmd/gen-validators
	@echo "✓ Validators generated in pkg/generated/validators/"

## Cleanup

clean: ## Clean build artifacts
	@echo "Cleaning..."
	rm -rf bin/
	rm -rf coverage/
	rm -f coverage.out coverage.html
	rm -f controller integration.test *.test

## Development helpers

fmt: ## Format code with gofmt
	@echo "Formatting code..."
	$(GO) fmt ./...

fix: ## Run automated code modernizers (go fix + gofmt)
	@echo "Running go fix..."
	$(GO) fix ./cmd/... ./examples/... ./pkg/... ./tests/... ./tools/...
	@echo "Formatting code..."
	$(GO) fmt ./...

vet: ## Run go vet
	@echo "Running go vet..."
	$(GO) vet ./...

## Installation helpers

install-tools: ## Install/sync all tool dependencies (from go.mod tools section)
	@echo "Installing tool dependencies..."
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	$(GO) install golang.org/x/vuln/cmd/govulncheck
	$(GO) install github.com/arch-go/arch-go/v2
	$(GO) install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen
	$(GO) install sigs.k8s.io/controller-tools/cmd/controller-gen@latest
	@echo "✓ All tools installed!"

## Convenience targets

dev: clean build test lint ## Clean, build, test, and lint (common dev workflow)
	@echo "✓ Development build complete!"

## Release targets

release-controller: ## Create a controller release (usage: make release-controller VERSION=0.1.0)
	@if [ -z "$(VERSION)" ] || [ "$(VERSION)" = "dev" ]; then \
		echo "Error: VERSION must be specified (e.g., make release-controller VERSION=0.1.0)"; \
		exit 1; \
	fi
	@./scripts/release-controller.sh $(VERSION)

release-chart: ## Create a chart release (usage: make release-chart CHART_VERSION=0.1.0)
	@if [ -z "$(CHART_VERSION)" ] || [ "$(CHART_VERSION)" = "dev" ]; then \
		echo "Error: CHART_VERSION must be specified (e.g., make release-chart CHART_VERSION=0.1.0)"; \
		exit 1; \
	fi
	@./scripts/release-chart.sh $(CHART_VERSION)

goreleaser-snapshot: ## Test GoReleaser locally (no push)
	goreleaser release --snapshot --clean

## PGO (Profile-Guided Optimization) targets

pgo-profile: ## Collect CPU profile from dev environment for PGO
	@echo "Collecting 30-second CPU profile from dev environment..."
	@echo ""
	@echo "Prerequisites:"
	@echo "  1. Dev environment running: ./scripts/start-dev-env.sh"
	@echo "  2. Port-forward active: kubectl -n haptic port-forward deploy/haptic-controller 8080:8080"
	@echo ""
	@echo "Starting profile collection (30 seconds)..."
	curl -o cmd/controller/default.pgo http://localhost:8080/debug/pprof/profile?seconds=30
	@echo ""
	@echo "Profile saved to cmd/controller/default.pgo"
	@echo "Rebuild with: make build"

pgo-merge: ## Merge multiple PGO profiles into one
	@if [ -z "$(PROFILES)" ]; then echo "Usage: make pgo-merge PROFILES='profile1.pgo profile2.pgo'"; exit 1; fi
	@echo "Merging PGO profiles: $(PROFILES)"
	$(GO) tool pprof -proto $(PROFILES) > cmd/controller/default.pgo
	@echo "Merged profile saved to cmd/controller/default.pgo"
