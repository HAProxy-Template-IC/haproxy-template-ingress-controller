# HAProxy Template I(ngress)C(ontroller)

<div align="center">

<img src="docs/controller/docs/assets/logo.svg" alt="HAProxy Template IC Logo" width="400">

</div>

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Build Status](https://gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/badges/main/pipeline.svg)](https://gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/-/pipelines)
[![Coverage](https://gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/badges/main/coverage.svg)](https://gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/-/graphs/main/charts)

A template-driven [HAProxy](https://www.haproxy.org/) Ingress Controller for Kubernetes that generates HAProxy configurations using [Gonja](https://github.com/NikolaLohinski/gonja) templates and deploys them via the [HAProxy Dataplane API](https://github.com/haproxytech/dataplaneapi).

> **Documentation**: <https://haproxy-template-ic.github.io/>

## Key Features

### Template-Driven Flexibility

Traditional ingress controllers embed configuration logic in code. HAProxy Template IC inverts this:

- **Full HAProxy access** - Write any HAProxy directive using the [configuration language](https://www.haproxy.com/documentation/haproxy-configuration-manual/latest/)
- **Add features without code changes** - New directives are template updates, not controller releases
- **Iterate rapidly** - Deploy configuration changes in minutes, not release cycles
- **Rich template context** - Access any Kubernetes resource, fetch external data via HTTP, and use controller state in your templates
- **Everything is templatable** - Generate not just `haproxy.cfg` but also map files, SSL certificates, CRT-lists, and custom auxiliary files

### Production Ready

- **High availability** - Leader election with automatic failover
- **Comprehensive validation** - Validating webhook, template validation, and CI/CD-runnable tests
- **Observability** - Prometheus metrics, structured logging, and debug endpoints
- **HTTP resource access** - Fetch external data for use in templates

## Quick Start

```bash
helm repo add haproxy-template-ic https://haproxy-template-ic.gitlab.io/haproxy-template-ingress-controller/charts
helm repo update
helm install my-controller haproxy-template-ic/haproxy-template-ic
```

For complete setup instructions including HAProxy pod deployment, see the [Getting Started](https://haproxy-template-ic.github.io/controller/getting-started/) guide.

## Development

### Build and Test

```bash
# Build the controller binary
make build

# Run unit tests
make test

# Run integration tests (requires kind cluster)
make test-integration

# Run linting checks
make lint

# Run all checks (tests + linting)
make check-all

# Build Docker image
make docker-build

# Generate coverage report
make test-coverage
```

### Local Development Environment

The project includes scripts for local development with kind:

```bash
# Start development cluster with controller
./scripts/start-dev-env.sh

# Rebuild and restart controller after code changes
./scripts/start-dev-env.sh --restart

# View controller logs
./scripts/start-dev-env.sh logs

# Check deployment status
./scripts/start-dev-env.sh status

# Test ingress functionality
./scripts/start-dev-env.sh test

# Clean up development environment
./scripts/start-dev-env.sh down
```

> [!WARNING]
> Always use the `kind-haproxy-template-ic-dev` cluster context for development work. The `kind-haproxy-test` context is reserved for integration tests and will be automatically created and destroyed by test runs.

### Pre-commit Hooks

Set up automatic code quality checks using pre-commit:

```bash
# Install pre-commit (one-time setup)
pip install pre-commit
# or: brew install pre-commit

# Install git hooks (one-time per repository clone)
pre-commit install

# Hooks now run automatically on git commit
git commit -m "my changes"  # Runs make lint && make audit

# Skip hooks if needed (for WIP commits)
git commit --no-verify -m "WIP"

# Run hooks manually on all files
pre-commit run --all-files
```

The hooks run `make lint` and `make audit` before each commit to catch issues early.

## Contributing

Contributions are welcome. Before submitting pull requests:

1. Run `make check-all` to verify code quality
2. Add tests for new functionality
3. Update documentation as needed
4. Follow existing code style and patterns

See [CLAUDE.md](CLAUDE.md) for detailed development context and patterns.

## License

Licensed under the Apache License 2.0 - see [LICENSE](LICENSE) file for details.

Copyright 2025 Philipp Hossner

## Acknowledgments

This project builds on open source software:

- [Kubernetes client-go](https://github.com/kubernetes/client-go) - Kubernetes API client library
- [HAProxy client-native](https://github.com/haproxytech/client-native) - HAProxy Dataplane API client
- [Gonja](https://github.com/nikolalohinski/gonja) - Jinja2-like templating engine for Go
