.PHONY: help version lint lint-fix lint-chart lint-chart-ci audit check-all \
        test test-integration test-acceptance test-acceptance-parallel build-integration-test \
        test-coverage test-integration-coverage test-coverage-combined \
        build docker-build docker-build-multiarch docker-build-multiarch-push docker-load-kind docker-push docker-clean \
        tidy verify generate clean fmt vet install-tools dev \
        release-controller release-chart goreleaser-snapshot \
        pgo-profile pgo-merge

.DEFAULT_GOAL := help

# Variables
GO := go
# renovate: datasource=github-releases depName=golangci/golangci-lint
GOLANGCI_LINT_VERSION := v2.7.2
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
KIND_CLUSTER ?= haptic-dev  # Kind cluster name for local testing
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GIT_TAG := $(shell git describe --tags --exact-match 2>/dev/null || echo "dev")
VERSION := $(shell cat VERSION 2>/dev/null || echo "dev")
CHART_VERSION := $(shell grep '^version:' charts/haptic/Chart.yaml 2>/dev/null | awk '{print $$2}' || echo "dev")

# Coverage packages (excludes generated code)
COVERAGE_PACKAGES := ./cmd/...,./pkg/controller/...,./pkg/core/...,./pkg/dataplane/...,./pkg/events/...,./pkg/httpstore/...,./pkg/introspection/...,./pkg/k8s/...,./pkg/lifecycle/...,./pkg/metrics/...,./pkg/templating/...,./pkg/webhook/...

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

lint: ## Run all linters (YAML, JSON, Markdown, Go)
	@echo "Linting YAML files..."
	yamllint -c .yamllint.yml .
	@echo "Linting JSON files..."
	@for f in renovate.json; do \
		echo "  Checking $$f..."; \
		jq empty "$$f" || exit 1; \
	done
	@echo "Linting Markdown files..."
	markdownlint-cli2 "**/*.md" "#node_modules" "#.claude"
	@echo "Running golangci-lint..."
ifdef CI
	$(GOLANGCI_LINT) run --output.code-climate.path=gl-code-quality-report.json \
		./cmd/... ./examples/... ./pkg/apis/... ./pkg/controller/... \
		./pkg/core/... ./pkg/dataplane/... ./pkg/events/... ./pkg/k8s/... \
		./pkg/templating/... ./pkg/webhook/... ./tests/... ./tools/...
else
	$(GOLANGCI_LINT) run \
		./cmd/... ./examples/... ./pkg/apis/... ./pkg/controller/... \
		./pkg/core/... ./pkg/dataplane/... ./pkg/events/... ./pkg/k8s/... \
		./pkg/templating/... ./pkg/webhook/... ./tests/... ./tools/...
endif
	@echo "Running arch-go..."
	$(ARCH_GO)
	@echo "Running event immutability checker..."
	@mkdir -p bin
	@cd tools/linters/eventimmutability && $(GO) build -o ../../../bin/eventimmutability ./cmd/eventimmutability
	@./bin/eventimmutability ./...

lint-fix: ## Run golangci-lint with auto-fix
	@echo "Running golangci-lint with auto-fix..."
	$(GOLANGCI_LINT) run --fix ./cmd/... ./examples/... ./pkg/apis/... ./pkg/controller/... ./pkg/core/... ./pkg/dataplane/... ./pkg/events/... ./pkg/k8s/... ./pkg/templating/... ./pkg/webhook/... ./tests/... ./tools/...

## Chart linting

# renovate: datasource=docker depName=quay.io/helmpack/chart-testing versioning=docker
CT_VERSION := v3.14.0
# renovate: datasource=docker depName=helmunittest/helm-unittest versioning=docker
HELM_UNITTEST_VERSION := 3.17.1-0.7.2
# renovate: datasource=docker depName=ghcr.io/yannh/kubeconform versioning=docker
KUBECONFORM_VERSION := v0.7.0-alpine
# renovate: datasource=docker depName=kindest/node
KUBE_VERSION := 1.35.0

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
		| docker run --rm -i ghcr.io/yannh/kubeconform:$(KUBECONFORM_VERSION) \
			-kubernetes-version $(KUBE_VERSION) \
			-schema-location default \
			-schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
			-skip haproxy-haptic.org/v1alpha1/HAProxyTemplateConfig,haproxy-haptic.org/v1alpha1/HAProxyConfig,haproxy-haptic.org/v1alpha1/HAProxyMapFile \
			-summary
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
		| kubeconform \
			-kubernetes-version $(KUBE_VERSION) \
			-schema-location default \
			-schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
			-skip haproxy-haptic.org/v1alpha1/HAProxyTemplateConfig,haproxy-haptic.org/v1alpha1/HAProxyConfig,haproxy-haptic.org/v1alpha1/HAProxyMapFile \
			-summary
	@echo ""
	@echo "All chart linting passed!"

## Security & vulnerability scanning

audit: ## Run security vulnerability scanning
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
	@echo "  Git commit: $(GIT_COMMIT)"
	@echo "  Git tag: $(GIT_TAG)"
	DOCKER_BUILDKIT=1 docker build \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg GIT_TAG=$(GIT_TAG) \
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

## Dependency management

tidy: ## Run go mod tidy
	@echo "Running go mod tidy..."
	$(GO) mod tidy

verify: ## Verify dependencies
	@echo "Verifying dependencies..."
	$(GO) mod verify

## Code generation

generate: generate-crds generate-deepcopy generate-clientset generate-dataplaneapi-all ## Run all code generation

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

generate-dataplaneapi-v30: ## Generate HAProxy DataPlane API v3.0 client
	@echo "Generating DataPlane API v3.0 client (models + client)..."
	@mkdir -p pkg/generated/dataplaneapi/v30
	$(OAPI_CODEGEN) -config hack/oapi-codegen-v30.yaml \
		pkg/generated/dataplaneapi/v30/spec.json
	@echo "✓ DataPlane API v3.0 client generated"

generate-dataplaneapi-v31: ## Generate HAProxy DataPlane API v3.1 client
	@echo "Generating DataPlane API v3.1 client (models + client)..."
	@mkdir -p pkg/generated/dataplaneapi/v31
	$(OAPI_CODEGEN) -config hack/oapi-codegen-v31.yaml \
		pkg/generated/dataplaneapi/v31/spec.json
	@echo "✓ DataPlane API v3.1 client generated"

generate-dataplaneapi-v32: ## Generate HAProxy DataPlane API v3.2 client
	@echo "Generating DataPlane API v3.2 client (models + client)..."
	@mkdir -p pkg/generated/dataplaneapi/v32
	$(OAPI_CODEGEN) -config hack/oapi-codegen-v32.yaml \
		pkg/generated/dataplaneapi/v32/spec.json
	@echo "✓ DataPlane API v3.2 client generated"

generate-dataplaneapi-v30ee: ## Generate HAProxy Enterprise DataPlane API v3.0 client
	@echo "Generating Enterprise DataPlane API v3.0 client (models + client)..."
	@mkdir -p pkg/generated/dataplaneapi/v30ee
	$(OAPI_CODEGEN) -config hack/oapi-codegen-v30ee.yaml \
		pkg/generated/dataplaneapi/v30ee/spec.json
	@echo "✓ Enterprise DataPlane API v3.0 client generated"

generate-dataplaneapi-v31ee: ## Generate HAProxy Enterprise DataPlane API v3.1 client
	@echo "Generating Enterprise DataPlane API v3.1 client (models + client)..."
	@mkdir -p pkg/generated/dataplaneapi/v31ee
	$(OAPI_CODEGEN) -config hack/oapi-codegen-v31ee.yaml \
		pkg/generated/dataplaneapi/v31ee/spec.json
	@echo "✓ Enterprise DataPlane API v3.1 client generated"

generate-dataplaneapi-v32ee: ## Generate HAProxy Enterprise DataPlane API v3.2 client
	@echo "Generating Enterprise DataPlane API v3.2 client (models + client)..."
	@mkdir -p pkg/generated/dataplaneapi/v32ee
	$(OAPI_CODEGEN) -config hack/oapi-codegen-v32ee.yaml \
		pkg/generated/dataplaneapi/v32ee/spec.json
	@echo "✓ Enterprise DataPlane API v3.2 client generated"

generate-dataplaneapi-all: generate-dataplaneapi-v30 generate-dataplaneapi-v31 generate-dataplaneapi-v32 generate-dataplaneapi-v30ee generate-dataplaneapi-v31ee generate-dataplaneapi-v32ee ## Generate all HAProxy DataPlane API versions
	@echo "✓ All DataPlane API clients generated (Community + Enterprise)"

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
	@echo "  2. Port-forward active: kubectl -n haproxy-template-ic port-forward deploy/haproxy-template-ic-controller 8080:8080"
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
