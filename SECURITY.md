# Security Policy

## Reporting a Vulnerability

To report a security vulnerability, use GitHub's private vulnerability reporting:

1. Go to the [HAPTIC security page](https://github.com/haproxy-haptic/HAPTIC/security)
2. Select **Report a vulnerability**

Include in your report:

- A description of the vulnerability
- Steps to reproduce the issue
- The potential impact

We will acknowledge your report within 72 hours and aim to release a fix within 30 days for critical issues. Please do not disclose the vulnerability publicly until a fix has been released.

## Supported Versions

Security fixes are applied to the latest stable release only.

| Version | Supported |
|---------|-----------|
| 0.2.x   | Yes       |

## Supply Chain Security

All release artifacts are signed with [Cosign](https://github.com/sigstore/cosign) using keyless OIDC. See [Supply Chain Security](docs/site/docs/development/releasing.md#supply-chain-security) for instructions on verifying signatures and inspecting SBOM attestations.
