# CI image with Go, HAProxy, Docker CLI, and Kubernetes tools
# All version ARGs should be passed from .gitlab-ci.yml to keep versions in sync
#
# Uses haproxytech/haproxy-debian as the base image so HAProxy is natively
# available for all versions without depending on haproxy.debian.net.
# Go toolchain is copied from the official golang image.

# Global ARGs must be declared before the first FROM to be available to all stages
# renovate: datasource=docker depName=golang
ARG GO_VERSION=1.25
# renovate: datasource=github-tags depName=haproxy/haproxy versioning=regex:^v(?<major>\d+)\.(?<minor>\d+)$
ARG HAPROXY_VERSION=3.2

# Stage 1: Go toolchain source
FROM golang:${GO_VERSION}-bookworm AS go-toolchain

# Stage 2: CI image based on official HAProxy image
FROM haproxytech/haproxy-debian:${HAPROXY_VERSION}

# Copy Go toolchain from official image
COPY --from=go-toolchain /usr/local/go /usr/local/go
ENV GOPATH=/go \
    PATH="/usr/local/go/bin:/go/bin:${PATH}"
RUN mkdir -p "$GOPATH/src" "$GOPATH/bin" && chmod -R 1777 "$GOPATH"

# renovate: datasource=docker depName=docker
ARG DOCKER_VERSION=29
# renovate: datasource=github-releases depName=kubernetes-sigs/kind
ARG KIND_VERSION=0.31.0
# renovate: datasource=github-releases depName=helm/chart-testing
ARG CT_VERSION=3.14.0
# renovate: datasource=github-releases depName=helm-unittest/helm-unittest
ARG HELM_UNITTEST_VERSION=1.0.3
# renovate: datasource=github-releases depName=yannh/kubeconform
ARG KUBECONFORM_VERSION=0.7.0
# renovate: datasource=pypi depName=yamllint
ARG YAMLLINT_VERSION=1.37.1
# markdownlint-cli2 pinned to 0.14.0 - newer versions require Node.js 20+
ARG MARKDOWNLINT_VERSION=0.14.0
# renovate: datasource=github-releases depName=goreleaser/goreleaser
ARG GORELEASER_VERSION=2.13.2
# renovate: datasource=github-releases depName=sigstore/cosign
ARG COSIGN_VERSION=3.0.3
# renovate: datasource=github-releases depName=anchore/syft
ARG SYFT_VERSION=1.39.0

# Install git (for go modules), Docker CLI, and other build dependencies
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
       ca-certificates gnupg git make \
       jq python3-pip nodejs npm \
    && curl -fsSL https://download.docker.com/linux/debian/gpg \
       | gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg \
    && . /etc/os-release \
    && echo "deb [arch=amd64 signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] \
       https://download.docker.com/linux/debian ${VERSION_CODENAME} stable" \
       > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
       docker-ce-cli=5:${DOCKER_VERSION}.\* docker-buildx-plugin \
    && rm -rf /var/lib/apt/lists/*

# Install yamllint, yamale (for ct), and markdownlint-cli2 for config file linting
# Pin markdownlint-cli2@0.14.0 - newer versions pull string-width 7.x which requires Node.js 20+
RUN pip3 install --break-system-packages yamllint==${YAMLLINT_VERSION} yamale \
    && npm install -g markdownlint-cli2@${MARKDOWNLINT_VERSION}

# Install kind
RUN curl -fsSL -o /usr/local/bin/kind \
    "https://kind.sigs.k8s.io/dl/v${KIND_VERSION}/kind-linux-amd64" \
    && chmod +x /usr/local/bin/kind

# Install kubectl (latest stable)
# Use retry flags to handle transient 503 errors from dl.k8s.io
RUN curl -fsSL --retry 5 --retry-delay 2 --retry-all-errors -o /usr/local/bin/kubectl \
    "https://dl.k8s.io/release/$(curl -fsSL --retry 5 --retry-delay 2 --retry-all-errors https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" \
    && chmod +x /usr/local/bin/kubectl

# Install Helm
RUN curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash

# Install yq
RUN curl -fsSL -o /usr/local/bin/yq \
    "https://github.com/mikefarah/yq/releases/latest/download/yq_linux_amd64" \
    && chmod +x /usr/local/bin/yq

# Install gocover-cobertura for coverage report conversion
RUN go install github.com/boumenot/gocover-cobertura@latest

# Install chart-testing (ct) with configuration files
RUN mkdir -p /etc/ct \
    && curl -fsSL "https://github.com/helm/chart-testing/releases/download/v${CT_VERSION}/chart-testing_${CT_VERSION}_linux_amd64.tar.gz" \
    | tar xzf - -C /tmp \
    && mv /tmp/ct /usr/local/bin/ct \
    && mv /tmp/etc/* /etc/ct/ \
    && rm -rf /tmp/etc

# Install helm-unittest plugin
RUN helm plugin install https://github.com/helm-unittest/helm-unittest.git --version v${HELM_UNITTEST_VERSION}

# Install kubeconform
RUN curl -fsSL "https://github.com/yannh/kubeconform/releases/download/v${KUBECONFORM_VERSION}/kubeconform-linux-amd64.tar.gz" \
    | tar xzf - -C /usr/local/bin kubeconform

# Install goreleaser
RUN curl -fsSL "https://github.com/goreleaser/goreleaser/releases/download/v${GORELEASER_VERSION}/goreleaser_Linux_x86_64.tar.gz" \
    | tar xzf - -C /usr/local/bin goreleaser

# Install cosign
RUN curl -fsSL -o /usr/local/bin/cosign \
    "https://github.com/sigstore/cosign/releases/download/v${COSIGN_VERSION}/cosign-linux-amd64" \
    && chmod +x /usr/local/bin/cosign

# Install syft for SBOM generation
RUN curl -fsSL "https://github.com/anchore/syft/releases/download/v${SYFT_VERSION}/syft_${SYFT_VERSION}_linux_amd64.tar.gz" \
    | tar xzf - -C /usr/local/bin syft

# Verify installations
RUN go version && haproxy -v && docker --version && kind version && kubectl version --client && helm version && yq --version && jq --version && gocover-cobertura -h 2>&1 | head -1 && yamllint --version && markdownlint-cli2 --help | head -1 && ct version && helm unittest --help | head -1 && kubeconform -v && goreleaser --version && cosign version && syft version
