# Security Policy

## Reporting a Vulnerability

To report a security vulnerability, please open a **confidential issue** in the GitLab project:

1. Go to the [HAPTIC GitLab project](https://gitlab.com/haproxy-haptic/haptic)
2. Navigate to **Issues → New issue**
3. Enable **Confidential issue** before submitting

Include in your report:

- A description of the vulnerability
- Steps to reproduce the issue
- The potential impact

We will acknowledge your report within 72 hours and aim to release a fix within 30 days for critical issues. Please do not disclose the vulnerability publicly until a fix has been released.

## Supported Versions

Security fixes are applied to the latest stable release only.

| Version | Supported |
|---------|-----------|
| 0.1.x   | Yes       |

## Supply Chain Security

All release artifacts are signed with [Cosign](https://github.com/sigstore/cosign) using keyless OIDC. See [Supply Chain Security](docs/site/docs/development/releasing.md#supply-chain-security) for instructions on verifying signatures and inspecting SBOM attestations.
