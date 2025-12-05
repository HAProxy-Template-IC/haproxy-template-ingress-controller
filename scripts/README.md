# Scripts

Development and maintenance scripts for the HAProxy Template Ingress Controller.

## extract-dataplane-spec.sh

Extracts HAProxy DataPlane API OpenAPI specifications from running containers.

### Purpose

Downloads the OpenAPI v3 specification from the DataPlane API `/v3/specification_openapiv3` endpoint for a specific HAProxy version. Used to generate version-specific Go HTTP clients for the DataPlane API.

### Usage

```bash
./scripts/extract-dataplane-spec.sh <haproxy-version> [output-file]
```

### Arguments

- `haproxy-version` - HAProxy version to extract (e.g., `3.0`, `3.1`, `3.2`)
- `output-file` - Optional output file path (default: `spec.json` in current directory)

### Examples

```bash
# Extract v3.2 spec to default location
./scripts/extract-dataplane-spec.sh 3.2

# Extract v3.1 spec to specific file
./scripts/extract-dataplane-spec.sh 3.1 /tmp/dataplane-v31.json

# Extract v3.0 spec to project structure
./scripts/extract-dataplane-spec.sh 3.0 pkg/generated/dataplaneapi/v30/spec.json
```

### Requirements

- Docker
- curl
- jq
- nc (netcat) for port availability checking

### How It Works

1. Creates a temporary directory with minimal DataPlane API and HAProxy configs
2. Starts a Docker container with the specified HAProxy version
3. Waits for the DataPlane API to become ready
4. Downloads the OpenAPI v3 specification via authenticated HTTP request
5. Validates and pretty-prints the JSON
6. Cleans up the container and temporary files

### Output

The script outputs a formatted JSON file containing the complete OpenAPI v3 specification for the specified HAProxy DataPlane API version.

### Troubleshooting

**Container fails to start:**

- Check Docker is running: `docker info`
- Verify the HAProxy version exists: `docker pull haproxytech/haproxy-alpine:<version>`

**API not becoming ready:**

- Check container logs manually: `docker logs dataplaneapi-extract-<version>`
- Increase timeout by editing `max_attempts` in the script

**Permission errors during cleanup:**

- The script uses Docker to clean up files created by the container
- Ensure Docker has proper permissions

### Future Versions

To extract specs for future HAProxy versions (e.g., 3.3, 3.4):

```bash
# Extract spec
./scripts/extract-dataplane-spec.sh 3.3 pkg/generated/dataplaneapi/v33/spec.json

# Generate client code
make generate-dataplaneapi-v33

# Update Clientset to include new version
```

## Other Scripts

### start-dev-env.sh

Starts the local development environment with kind cluster.

```bash
./scripts/start-dev-env.sh          # Start or attach to dev cluster
./scripts/start-dev-env.sh restart  # Rebuild and redeploy
./scripts/start-dev-env.sh logs     # View controller logs
./scripts/start-dev-env.sh down     # Tear down dev environment
```

### test-templates.sh

Tests Helm chart template rendering.

```bash
./scripts/test-templates.sh                                    # Run all tests
./scripts/test-templates.sh --test test-httproute-basic       # Run specific test
./scripts/test-templates.sh --dump-rendered --verbose         # Debug mode
```

### test-routes.sh

Tests ingress route functionality in the dev cluster.

```bash
./scripts/test-routes.sh  # Test HTTP routing with live cluster
```
