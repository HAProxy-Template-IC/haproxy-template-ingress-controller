# CI image for documentation builds with MkDocs
# Build with: docker build -t python-docs:latest .

# renovate: datasource=docker depName=python
ARG PYTHON_VERSION=3.14
FROM python:${PYTHON_VERSION}-slim

# Install git, curl, jq, and unzip (needed for MR previews and version management)
RUN apt-get update && apt-get install -y --no-install-recommends git curl jq unzip \
    && rm -rf /var/lib/apt/lists/*

# Install MkDocs with Material theme and versioning support
# Pin mkdocs<2 to avoid incompatibility with mkdocs-material
# (see https://squidfunk.github.io/mkdocs-material/blog/2026/02/18/mkdocs-2.0/)
RUN pip install --no-cache-dir \
    "mkdocs>=1.6,<2" \
    mkdocs-material \
    mike

# Verify installations
RUN mkdocs --version && mike --version
