.PHONY: help version lint lint-fix audit check-all \
        test test-integration test-acceptance test-acceptance-parallel build-integration-test test-coverage \
        build docker-build docker-build-multiarch docker-build-multiarch-push docker-load-kind docker-push docker-clean \
        tidy verify generate clean fmt vet install-tools dev

.DEFAULT_GOAL := help

# Variables
GO := go
# renovate: datasource=github-releases depName=golangci/golangci-lint
GOLANGCI_LINT_VERSION := v2.7.1
GOLANGCI_LINT := $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GOVULNCHECK := $(GO) run golang.org/x/vuln/cmd/govulncheck
ARCH_GO := $(shell which arch-go 2>/dev/null || echo "$(GO) run github.com/arch-go/arch-go")
OAPI_CODEGEN := $(GO) run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen
CONTROLLER_GEN := $(GO) run sigs.k8s.io/controller-tools/cmd/controller-gen

# Docker variables
IMAGE_NAME ?= haproxy-template-ic# Container image name (override: IMAGE_NAME=my-image)
IMAGE_TAG ?= dev# Image tag (override: IMAGE_TAG=v1.0.0)
REGISTRY ?=# Container registry (e.g., registry.gitlab.com/myorg)
FULL_IMAGE := $(if $(REGISTRY),$(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG),$(IMAGE_NAME):$(IMAGE_TAG))
KIND_CLUSTER ?= haproxy-template-ic-dev  # Kind cluster name for local testing
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GIT_TAG := $(shell git describe --tags --exact-match 2>/dev/null || echo "dev")

# Default target
help: ## Show this help message
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-30s\033[0m %s\n", $$1, $$2}'

version: ## Display version information
	@echo "Version Information:"
	@echo "  Git Commit: $(GIT_COMMIT)"
	@echo "  Git Tag:    $(GIT_TAG)"
	@echo "  Image:      $(FULL_IMAGE)"

## Linting targets

lint: ## Run all linters (golangci-lint + arch-go + eventimmutability)
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

test-coverage: ## Run tests with coverage report
	@echo "Running tests with coverage..."
	$(GO) test -race -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated at coverage.html"

## Build targets

build: ## Build the controller binary
	@echo "Building controller..."
	@echo "  Git commit: $(GIT_COMMIT)"
	@echo "  Git tag: $(GIT_TAG)"
	@mkdir -p bin
	$(GO) build \
		-ldflags="-X main.GitCommit=$(GIT_COMMIT) -X main.GitTag=$(GIT_TAG)" \
		-o bin/controller \
		./cmd/controller

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
	@mkdir -p charts/haproxy-template-ic/crds
	$(CONTROLLER_GEN) crd:crdVersions=v1 \
		paths=./pkg/apis/haproxytemplate/v1alpha1/... \
		output:crd:dir=./charts/haproxy-template-ic/crds/
	@echo "✓ CRD manifests generated in charts/haproxy-template-ic/crds/"

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
	$(GO) install github.com/arch-go/arch-go
	$(GO) install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen
	$(GO) install sigs.k8s.io/controller-tools/cmd/controller-gen@latest
	@echo "✓ All tools installed!"

## Convenience targets

dev: clean build test lint ## Clean, build, test, and lint (common dev workflow)
	@echo "✓ Development build complete!"
