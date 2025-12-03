# CI image for documentation builds with MkDocs
# Build with: docker build -t python-docs:latest .

# renovate: datasource=docker depName=python
ARG PYTHON_VERSION=3.12
FROM python:${PYTHON_VERSION}-slim

# Install MkDocs with Material theme and versioning support
RUN pip install --no-cache-dir \
    mkdocs-material \
    mike

# Verify installations
RUN mkdocs --version && mike --version
