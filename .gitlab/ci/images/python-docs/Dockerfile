ARG PYTHON_DOCS_BASE_IMAGE=python:must-be-supplied@sha256:0000000000000000000000000000000000000000000000000000000000000000
FROM ${PYTHON_DOCS_BASE_IMAGE}

ARG MKDOCS_VERSION
ARG MKDOCS_MATERIAL_VERSION
ARG MIKE_VERSION
ARG DEBIAN_SNAPSHOT

RUN . /etc/os-release \
    && rm -f /etc/apt/sources.list /etc/apt/sources.list.d/* \
    && printf 'deb [check-valid-until=no] https://snapshot.debian.org/archive/debian/%s %s main\n' \
       "${DEBIAN_SNAPSHOT}" "${VERSION_CODENAME}" >/etc/apt/sources.list \
    && apt-get -o Acquire::Check-Valid-Until=false update \
    && apt-get install -y --no-install-recommends git curl jq unzip \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.lock /tmp/requirements.lock
RUN pip install --no-cache-dir --require-hashes -r /tmp/requirements.lock \
    && rm /tmp/requirements.lock

# Verify installations
RUN mkdocs --version && mike --version
