#!/bin/bash
set -euo pipefail

# Extract HAProxy DataPlane API OpenAPI specification from a running container
#
# This script starts a DataPlane API server in a Docker container and downloads
# the OpenAPI v3 specification from the /v3/specification_openapiv3 endpoint.
#
# Usage:
#   ./extract-dataplane-spec.sh [--enterprise] <haproxy-version> [output-file]
#
# Examples:
#   ./extract-dataplane-spec.sh 3.2                                              # Community
#   ./extract-dataplane-spec.sh 3.1 /tmp/spec.json                               # Community
#   ./extract-dataplane-spec.sh 3.0 pkg/generated/dataplaneapi/v30/spec.json     # Community
#   ./extract-dataplane-spec.sh --enterprise 3.0r1                               # Enterprise
#   ./extract-dataplane-spec.sh -e 3.1r1 pkg/generated/dataplaneapi/v31ee/spec.json
#
# Options:
#   --enterprise, -e  Use HAProxy Enterprise registry (hapee-registry.haproxy.com)
#
# Arguments:
#   haproxy-version  HAProxy version (e.g., 3.0, 3.1, 3.2 for community; 3.0r1, 3.1r1, 3.2r1 for enterprise)
#   output-file      Optional output file path (default: spec.json in current directory)
#
# Requirements:
#   - Docker
#   - curl
#   - jq
#
# For HAProxy Enterprise:
#   You must authenticate to the enterprise registry before running this script:
#   docker login hapee-registry.haproxy.com

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $*"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $*"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $*"
}

log_step() {
    echo -e "${BLUE}[STEP]${NC} $*"
}

# Print usage
usage() {
    cat <<EOF
Usage: $0 [--enterprise] <haproxy-version> [output-file]

Extract HAProxy DataPlane API OpenAPI specification from a Docker container.

Options:
  --enterprise, -e   Use HAProxy Enterprise registry

Arguments:
  haproxy-version    HAProxy version (e.g., 3.0, 3.1, 3.2 for community; 3.0r1, 3.1r1, 3.2r1 for enterprise)
  output-file        Optional output file path (default: spec.json)

Examples:
  $0 3.2                                              # Community edition
  $0 3.1 /tmp/spec-v31.json                           # Community edition
  $0 3.0 pkg/generated/dataplaneapi/v30/spec.json     # Community edition
  $0 --enterprise 3.0r1                               # Enterprise edition
  $0 -e 3.1r1 pkg/generated/dataplaneapi/v31ee/spec.json

Requirements:
  - Docker
  - curl
  - jq

For HAProxy Enterprise:
  You must authenticate to the enterprise registry before running this script:
  docker login hapee-registry.haproxy.com
EOF
    exit 1
}

# Check dependencies
check_dependencies() {
    local missing=0
    for cmd in docker curl jq; do
        if ! command -v "$cmd" &> /dev/null; then
            log_error "Required command not found: $cmd"
            missing=1
        fi
    done

    if [ $missing -eq 1 ]; then
        log_error "Please install missing dependencies"
        exit 1
    fi
}

# Cleanup function
cleanup() {
    local container=$1
    local temp_dir=$2

    if [ -n "$container" ] && docker ps -a --format '{{.Names}}' | grep -q "^${container}$"; then
        log_step "Cleaning up container: $container"
        docker rm -f "$container" >/dev/null 2>&1 || true
    fi

    if [ -n "$temp_dir" ] && [ -d "$temp_dir" ]; then
        log_step "Cleaning up temporary directory"
        # Use docker to clean up files created by container (permission issues)
        docker run --rm -v "$temp_dir:/cleanup" alpine sh -c "rm -rf /cleanup/*" 2>/dev/null || true
        rmdir "$temp_dir" 2>/dev/null || true
    fi
}

# Extract spec from DataPlane API
extract_spec() {
    local haproxy_version=$1
    local output_file=$2
    local is_enterprise=$3
    local container_name="dataplaneapi-extract-${haproxy_version//./-}"
    local temp_dir="/tmp/dpapi-extract-$$"
    local port=5555
    local docker_image
    local dataplaneapi_bin
    local haproxy_config_path

    # Select Docker image and paths based on edition
    local haproxy_bin_path
    if [ "$is_enterprise" = "true" ]; then
        docker_image="hapee-registry.haproxy.com/haproxy-enterprise:${haproxy_version}"
        container_name="${container_name}-ee"
        dataplaneapi_bin="/opt/hapee-extras/sbin/hapee-dataplaneapi"
        # Enterprise uses version-specific paths (e.g., /etc/hapee-3.0/, /opt/hapee-3.0/)
        local major_minor="${haproxy_version%r*}"  # Strip rN suffix: 3.0r1 -> 3.0
        haproxy_config_path="/etc/hapee-${major_minor}/hapee-lb.cfg"
        haproxy_bin_path="/opt/hapee-${major_minor}/sbin/hapee-lb"
    else
        docker_image="haproxytech/haproxy-alpine:${haproxy_version}"
        dataplaneapi_bin="/usr/local/bin/dataplaneapi"
        haproxy_config_path="/etc/haproxy/haproxy.cfg"
        haproxy_bin_path="/usr/local/sbin/haproxy"
    fi

    # Find available port
    while nc -z localhost $port 2>/dev/null; do
        port=$((port + 1))
    done

    local edition_label="Community"
    if [ "$is_enterprise" = "true" ]; then
        edition_label="Enterprise"
    fi

    log_info "Extracting OpenAPI spec for HAProxy DataPlane API v${haproxy_version} (${edition_label})"
    log_info "Using Docker image: $docker_image"
    log_info "Using port: $port"

    # Setup cleanup trap
    trap "cleanup '$container_name' '$temp_dir'" EXIT INT TERM

    # Create temporary config directory
    log_step "Creating temporary configuration..."
    mkdir -p "$temp_dir"

    # Create DataPlane API config
    cat > "$temp_dir/dataplaneapi.yaml" <<EOF
config_version: 2
name: spec_extractor
dataplaneapi:
  host: 0.0.0.0
  port: 5555
  user:
    - name: admin
      insecure: true
      password: adminpwd
haproxy:
  config_file: ${haproxy_config_path}
  haproxy_bin: ${haproxy_bin_path}
  reload:
    reload_cmd: "true"
    restart_cmd: "true"
    reload_strategy: custom
log_targets:
  - log_to: stdout
    log_level: warning
EOF

    # Create minimal HAProxy config
    cat > "$temp_dir/haproxy.cfg" <<'EOF'
# Minimal HAProxy configuration for DataPlane API spec extraction
global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 30s
    timeout server 30s
EOF

    # Start DataPlane API container
    log_step "Starting DataPlane API container..."
    local docker_volumes
    local docker_cmd

    if [ "$is_enterprise" = "true" ]; then
        # Enterprise: create directory structure and mount it
        local hapee_version="${haproxy_version%r*}"  # 3.0r1 -> 3.0
        mkdir -p "$temp_dir/hapee-extras"
        mkdir -p "$temp_dir/hapee-${hapee_version}"
        cp "$temp_dir/dataplaneapi.yaml" "$temp_dir/hapee-extras/dataplaneapi.yml"
        cp "$temp_dir/haproxy.cfg" "$temp_dir/hapee-${hapee_version}/hapee-lb.cfg"
        docker_volumes="-v $temp_dir/hapee-extras:/etc/hapee-extras:z -v $temp_dir/hapee-${hapee_version}:/etc/hapee-${hapee_version}:z"
        docker_cmd="${dataplaneapi_bin} -f /etc/hapee-extras/dataplaneapi.yml"
    else
        # Community: mount to /etc/haproxy/ with config file
        docker_volumes="-v $temp_dir:/etc/haproxy:z"
        docker_cmd="${dataplaneapi_bin} -f /etc/haproxy/dataplaneapi.yaml"
    fi

    local entrypoint_opt=""
    if [ "$is_enterprise" = "true" ]; then
        # Enterprise containers use s6 init which auto-starts dataplaneapi
        # Use --entrypoint to bypass and run directly
        entrypoint_opt="--entrypoint ${dataplaneapi_bin}"
        docker_cmd="-f /etc/hapee-extras/dataplaneapi.yml"
    fi

    if ! docker run -d \
        --name "$container_name" \
        -p "${port}:5555" \
        $docker_volumes \
        $entrypoint_opt \
        "$docker_image" \
        $docker_cmd \
        >/dev/null 2>&1; then
        log_error "Failed to start container"
        log_error "If using enterprise edition, ensure you are logged in:"
        log_error "  docker login hapee-registry.haproxy.com"
        return 1
    fi

    # Wait for DataPlane API to become ready
    log_step "Waiting for DataPlane API to become ready..."
    local max_attempts=30
    local attempt=0
    while [ $attempt -lt $max_attempts ]; do
        if curl -s -f -u admin:adminpwd \
            "http://localhost:${port}/v3/info" >/dev/null 2>&1; then
            break
        fi
        attempt=$((attempt + 1))
        sleep 1
    done

    if [ $attempt -eq $max_attempts ]; then
        log_error "DataPlane API did not become ready after ${max_attempts} seconds"
        log_info "Container logs:"
        docker logs "$container_name" 2>&1 | tail -20
        return 1
    fi

    # Get API version info
    local api_version
    api_version=$(curl -s -u admin:adminpwd \
        "http://localhost:${port}/v3/info" 2>/dev/null | \
        jq -r '.api.version // "unknown"')

    log_info "DataPlane API version: ${api_version}"

    # Download OpenAPI specification
    log_step "Downloading OpenAPI v3 specification..."
    if ! curl -s -f -u admin:adminpwd \
        "http://localhost:${port}/v3/specification_openapiv3" \
        -o "$output_file"; then
        log_error "Failed to download specification"
        return 1
    fi

    # Verify it's valid JSON
    if ! jq empty "$output_file" 2>/dev/null; then
        log_error "Downloaded spec is not valid JSON"
        return 1
    fi

    # Pretty-print the JSON
    log_step "Formatting JSON..."
    jq . "$output_file" > "${output_file}.tmp" && mv "${output_file}.tmp" "$output_file"

    # Get spec details
    local spec_version
    local spec_title
    spec_version=$(jq -r '.info.version // "unknown"' "$output_file")
    spec_title=$(jq -r '.info.title // "unknown"' "$output_file")

    # Print success summary
    echo
    log_info "======================================================"
    log_info "Successfully extracted OpenAPI specification!"
    log_info "======================================================"
    log_info "HAProxy edition:    ${edition_label}"
    log_info "HAProxy version:    ${haproxy_version}"
    log_info "Docker image:       ${docker_image}"
    log_info "API version:        ${api_version}"
    log_info "Spec version:       ${spec_version}"
    log_info "Spec title:         ${spec_title}"
    log_info "Output file:        ${output_file}"
    log_info "File size:          $(wc -c < "$output_file") bytes"
    log_info "======================================================"

    return 0
}

# Main execution
main() {
    local is_enterprise="false"
    local haproxy_version=""
    local output_file=""

    # Parse arguments
    while [[ $# -gt 0 ]]; do
        case $1 in
            --enterprise|-e)
                is_enterprise="true"
                shift
                ;;
            -*)
                log_error "Unknown option: $1"
                usage
                ;;
            *)
                if [ -z "$haproxy_version" ]; then
                    haproxy_version="$1"
                elif [ -z "$output_file" ]; then
                    output_file="$1"
                else
                    log_error "Too many arguments"
                    usage
                fi
                shift
                ;;
        esac
    done

    # Check required arguments
    if [ -z "$haproxy_version" ]; then
        log_error "Missing required argument: haproxy-version"
        usage
    fi

    # Set default output file
    output_file="${output_file:-spec.json}"

    # Validate version format based on edition
    if [ "$is_enterprise" = "true" ]; then
        # Enterprise versions: X.Yr1 (e.g., 3.0r1, 3.1r1, 3.2r1)
        if ! [[ "$haproxy_version" =~ ^[0-9]+\.[0-9]+r[0-9]+$ ]]; then
            log_error "Invalid enterprise version format: $haproxy_version"
            log_error "Expected format: X.YrZ (e.g., 3.0r1, 3.1r1, 3.2r1)"
            exit 1
        fi
    else
        # Community versions: X.Y (e.g., 3.0, 3.1, 3.2)
        if ! [[ "$haproxy_version" =~ ^[0-9]+\.[0-9]+$ ]]; then
            log_error "Invalid community version format: $haproxy_version"
            log_error "Expected format: X.Y (e.g., 3.0, 3.1, 3.2)"
            exit 1
        fi
    fi

    # Check dependencies
    check_dependencies

    # Create output directory if needed
    local output_dir
    output_dir=$(dirname "$output_file")
    if [ "$output_dir" != "." ] && [ ! -d "$output_dir" ]; then
        log_step "Creating output directory: $output_dir"
        mkdir -p "$output_dir"
    fi

    # Extract the spec
    if extract_spec "$haproxy_version" "$output_file" "$is_enterprise"; then
        exit 0
    else
        log_error "Failed to extract specification"
        exit 1
    fi
}

main "$@"
