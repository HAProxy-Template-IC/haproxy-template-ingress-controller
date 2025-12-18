# CI image with Go, HAProxy, Docker CLI, and Kubernetes tools
# All version ARGs should be passed from .gitlab-ci.yml to keep versions in sync

# renovate: datasource=docker depName=golang
ARG GO_VERSION=1.25
# Use bookworm variant because haproxy.debian.net has no trixie backports yet
FROM golang:${GO_VERSION}-bookworm

# renovate: datasource=github-tags depName=haproxy/haproxy versioning=regex:^v(?<major>\d+)\.(?<minor>\d+)$
ARG HAPROXY_VERSION=3.2
# renovate: datasource=docker depName=docker
ARG DOCKER_VERSION=29
# renovate: datasource=github-releases depName=kubernetes-sigs/kind
ARG KIND_VERSION=0.31.0

# Install HAProxy from haproxy.debian.net and Docker CLI from Docker's apt repository
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl ca-certificates gnupg \
    && curl -fsSL https://haproxy.debian.net/haproxy-archive-keyring.gpg \
       -o /usr/share/keyrings/haproxy-archive-keyring.gpg \
    && . /etc/os-release \
    && echo "deb [signed-by=/usr/share/keyrings/haproxy-archive-keyring.gpg] \
       https://haproxy.debian.net ${VERSION_CODENAME}-backports-${HAPROXY_VERSION} main" \
       > /etc/apt/sources.list.d/haproxy.list \
    && curl -fsSL https://download.docker.com/linux/debian/gpg \
       | gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg \
    && echo "deb [arch=amd64 signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] \
       https://download.docker.com/linux/debian ${VERSION_CODENAME} stable" \
       > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
       haproxy=${HAPROXY_VERSION}.\* docker-ce-cli=5:${DOCKER_VERSION}.\* docker-buildx-plugin \
       jq python3-pip nodejs npm \
    && rm -rf /var/lib/apt/lists/*

# Install yamllint and markdownlint-cli2 for config file linting
# Pin markdownlint-cli2@0.14.0 - newer versions pull string-width 7.x which requires Node.js 20+
RUN pip3 install --break-system-packages yamllint \
    && npm install -g markdownlint-cli2@0.14.0

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

# Verify installations
RUN go version && haproxy -v && docker --version && kind version && kubectl version --client && helm version && yq --version && jq --version && gocover-cobertura -h 2>&1 | head -1 && yamllint --version && markdownlint-cli2 --help | head -1
