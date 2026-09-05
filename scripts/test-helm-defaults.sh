#!/usr/bin/env bash
#
# Helm Chart Default Values Test
#
# Tests that the helm chart works out-of-the-box with default values when
# cert-manager is installed. Verifies: pods running, SSL certificate created,
# warning-free config admission and HAProxy configuration, and HTTP/HTTPS
# connectivity.
#
# Usage:
#   ./scripts/test-helm-defaults.sh [options]
#
# Options:
#   --keep-cluster    Don't delete cluster on success (useful for debugging)
#   --image IMAGE     Override image (default: use chart default)
#   --namespace NS    Namespace to install into (default: haptic)
#   --help            Show this help message
#
# Environment variables:
#   CLUSTER_NAME      Kind cluster name (default: helm-defaults)
#   KEEP_CLUSTER      Set to "true" to keep cluster on success
#   IMAGE             Controller image to use (e.g., registry.example.com/image:tag)
#   NAMESPACE         Namespace for installation (default: haptic)
#   TIMEOUT           Timeout in seconds for wait operations (default: 300)
#
# Exit codes:
#   0 - All checks passed
#   1 - Cluster creation failed
#   2 - cert-manager installation/readiness failed
#   3 - Helm chart installation failed
#   4 - Pod readiness timeout
#   5 - Certificate verification failed
#   6 - HTTP smoke test failed
#   7 - HTTPS smoke test failed
#   8 - HAProxy configuration check failed or emitted warnings
#   9 - HAProxy bootstrap worker-retirement check failed
#  10 - HAProxyTemplateConfig admission failed or emitted warnings
#  11 - Controller could not apply a rendered k8sResource (chart RBAC gap)

set -euo pipefail

# Configuration (can be overridden via environment)
CLUSTER_NAME="${CLUSTER_NAME:-helm-defaults}"
NAMESPACE="${NAMESPACE:-haptic}"
RELEASE_NAME="${RELEASE_NAME:-haptic}"
TIMEOUT="${TIMEOUT:-300}"
KEEP_CLUSTER="${KEEP_CLUSTER:-false}"
IMAGE="${IMAGE:-}"
CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.16.2}"

# Temp file tracking (for cleanup)
TEMP_KIND_CONFIG=""

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

#------------------------------------------------------------------------------
# Logging functions
#------------------------------------------------------------------------------

log() {
    local level="$1"
    shift
    local msg="$*"
    local color=""

    case "$level" in
        INFO)  color="$BLUE" ;;
        OK)    color="$GREEN" ;;
        WARN)  color="$YELLOW" ;;
        ERROR) color="$RED" ;;
    esac

    echo -e "${color}[$level]${NC} $msg"
}

info()  { log INFO "$@"; }
ok()    { log OK "$@"; }
warn()  { log WARN "$@"; }
error() { log ERROR "$@"; }

die() {
    error "$@"
    exit "${2:-1}"
}

#------------------------------------------------------------------------------
# DinD detection
#------------------------------------------------------------------------------

is_docker_in_docker() {
    [[ "${DOCKER_HOST:-}" == tcp://* ]]
}

#------------------------------------------------------------------------------
# Cluster management
#------------------------------------------------------------------------------

cluster_exists() {
    kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"
}

create_cluster() {
    info "Creating Kind cluster '$CLUSTER_NAME'..."

    local kind_config=""

    # Use DinD config if in Docker-in-Docker environment
    if is_docker_in_docker; then
        kind_config="$PROJECT_ROOT/.gitlab/ci/kind-config-dind.yaml"
        if [[ ! -f "$kind_config" ]]; then
            die "DinD config not found: $kind_config" 1
        fi
        info "Using DinD configuration: $kind_config"
    else
        # The smoke tests use kubectl port-forward, so the local cluster needs
        # no fixed host-port mappings. Avoiding 30080/30443 keeps this suite
        # isolated from haptic-dev and any other concurrently running kind
        # cluster.
        TEMP_KIND_CONFIG=$(mktemp)
        kind_config="$TEMP_KIND_CONFIG"
        cat > "$kind_config" << 'EOF'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
EOF
        info "Using minimal local configuration"
    fi

    if ! kind create cluster --name "$CLUSTER_NAME" --config "$kind_config" --wait 120s; then
        die "Failed to create Kind cluster" 1
    fi

    # Patch kubeconfig for DinD
    if is_docker_in_docker; then
        info "Patching kubeconfig for DinD..."
        sed -i 's|https://0\.0\.0\.0:|https://docker:|g' ~/.kube/config
    fi

    # Verify cluster is accessible
    if ! kubectl cluster-info &>/dev/null; then
        die "Cluster created but not accessible" 1
    fi

    ok "Cluster '$CLUSTER_NAME' created successfully"
}

delete_cluster() {
    if cluster_exists; then
        info "Deleting Kind cluster '$CLUSTER_NAME'..."
        kind delete cluster --name "$CLUSTER_NAME" || true
    fi
}

#------------------------------------------------------------------------------
# cert-manager installation
#------------------------------------------------------------------------------

install_cert_manager() {
    info "Installing cert-manager $CERT_MANAGER_VERSION..."

    if ! kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"; then
        die "Failed to install cert-manager" 2
    fi

    info "Waiting for cert-manager deployments to be ready..."

    local deployments=("cert-manager" "cert-manager-cainjector" "cert-manager-webhook")
    for deploy in "${deployments[@]}"; do
        if ! kubectl wait --for=condition=Available deployment/"$deploy" \
            -n cert-manager --timeout="${TIMEOUT}s"; then
            die "cert-manager deployment '$deploy' not ready within timeout" 2
        fi
    done

    # Wait for webhook to be fully operational by checking it can serve requests
    info "Waiting for cert-manager webhook to be fully operational..."
    local retries=30
    for ((i=1; i<=retries; i++)); do
        # Check if webhook endpoints exist and are ready
        local endpoints
        endpoints=$(kubectl get endpoints cert-manager-webhook -n cert-manager -o jsonpath='{.subsets[*].addresses[*].ip}' 2>/dev/null || echo "")
        if [[ -n "$endpoints" ]]; then
            # Verify webhook is responding by checking the ValidatingWebhookConfiguration
            if kubectl get validatingwebhookconfiguration cert-manager-webhook -o name &>/dev/null; then
                ok "cert-manager webhook is operational"
                break
            fi
        fi
        if [[ $i -eq $retries ]]; then
            warn "cert-manager webhook may not be fully ready, proceeding anyway"
        else
            info "Webhook not ready yet (attempt $i/$retries)..."
            sleep 2
        fi
    done

    ok "cert-manager installed and ready"
}

#------------------------------------------------------------------------------
# Helm chart installation
#------------------------------------------------------------------------------

load_image_to_cluster() {
    if [[ -z "$IMAGE" ]]; then
        return
    fi

    info "Loading image '$IMAGE' into kind cluster..."

    # Check if image exists locally
    if ! docker image inspect "$IMAGE" &>/dev/null; then
        warn "Image '$IMAGE' not found locally, skipping load (will pull from registry)"
        return
    fi

    if ! kind load docker-image "$IMAGE" --name "$CLUSTER_NAME"; then
        die "Failed to load image into kind cluster" 3
    fi

    ok "Image loaded into cluster"
}

install_helm_chart() {
    info "Installing helm chart with default values..."

    # Load image into cluster if specified
    load_image_to_cluster

    # Don't use --wait here because pods depend on cert-manager creating secrets first
    # We'll wait for certificates, then pods separately
    local helm_args=(
        "upgrade" "--install" "$RELEASE_NAME"
        "$PROJECT_ROOT/charts/haptic"
        "--namespace" "$NAMESPACE"
        "--create-namespace"
    )

    # Override the spoa-hub image tag from SPOA_TAG if set, so MR
    # pipelines test against the per-pipeline `ci-${CI_PIPELINE_ID}`
    # snapshot built by `build-spoa-image-snapshot` rather than
    # whatever happens to be at `main-latest` (which would mask
    # chart-branch version skew — see !893's post-mortem and the
    # matching pattern in scripts/start-dev-env.sh:734).
    #
    # The chart's own default (`spoaHub.image.tag` empty → falls back
    # to `.Chart.AppVersion`) is correct for release-version chart
    # consumers: `build-spoa-image-release` publishes
    # `spoa-hub:<haptic-version>` matching the chart's appVersion on
    # tag pipelines. Pre-release main consumers between releases
    # would still need an override; this script's caller supplies
    # one via SPOA_TAG when it's running CI.
    if [[ -n "${SPOA_TAG:-}" ]]; then
        info "Using SPOA_TAG=${SPOA_TAG} for spoa-hub image"
        helm_args+=("--set" "spoaHub.image.tag=${SPOA_TAG}")
    fi

    # Add image override if specified
    # The chart always appends -haproxy<version> to the tag, so strip
    # the suffix from incoming tags (e.g., ci-123-haproxy3.2 -> ci-123)
    if [[ -n "$IMAGE" ]]; then
        info "Using custom image: $IMAGE"
        helm_args+=("--set" "controller.image.repository=${IMAGE%:*}")
        if [[ "$IMAGE" == *:* ]]; then
            local full_tag="${IMAGE##*:}"
            local base_tag="${full_tag%-haproxy*}"
            helm_args+=("--set" "controller.image.tag=${base_tag}")
            # Extract haproxy version from tag if present
            if [[ "$full_tag" == *-haproxy* ]]; then
                local hp_version="${full_tag##*-haproxy}"
                helm_args+=("--set" "haproxyVersion=${hp_version}")
            fi
        fi
    fi

    if ! helm "${helm_args[@]}"; then
        die "Helm chart installation failed" 3
    fi

    ok "Helm chart installed successfully"
}

#------------------------------------------------------------------------------
# Pod readiness checks
#------------------------------------------------------------------------------

wait_for_pods() {
    info "Waiting for pods to be ready..."

    # Wait for controller pods
    info "Waiting for controller pods..."
    if ! kubectl wait --for=condition=Ready pod \
        -l "app.kubernetes.io/component=controller" \
        -n "$NAMESPACE" --timeout="${TIMEOUT}s"; then
        die "Controller pods not ready within timeout" 4
    fi

    # Wait for HAProxy pods
    info "Waiting for HAProxy pods..."
    if ! kubectl wait --for=condition=Ready pod \
        -l "app.kubernetes.io/component=loadbalancer" \
        -n "$NAMESPACE" --timeout="${TIMEOUT}s"; then
        die "HAProxy pods not ready within timeout" 4
    fi

    ok "All pods are ready"
}

#------------------------------------------------------------------------------
# Live HAProxy configuration validation
#------------------------------------------------------------------------------

verify_haproxy_configs_warning_free() {
    info "Checking every HAProxy replica with haproxy -c (warnings are fatal)..."

    local pods=()
    mapfile -t pods < <(
        kubectl get pods -n "$NAMESPACE" \
            -l "app.kubernetes.io/component=loadbalancer" \
            -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'
    )
    if [[ ${#pods[@]} -eq 0 ]]; then
        die "No HAProxy pods found for configuration validation" 8
    fi

    local pod output rc
    for pod in "${pods[@]}"; do
        output=""
        rc=0
        output=$(kubectl exec -n "$NAMESPACE" "$pod" -c haproxy -- \
            haproxy -c -f /etc/haproxy/haproxy.cfg 2>&1) || rc=$?

        if [[ $rc -ne 0 ]]; then
            error "haproxy -c failed in pod $pod (exit $rc):"
            printf '%s\n' "$output" >&2
            die "HAProxy configuration validation failed in pod $pod" 8
        fi

        # HAProxy returns zero for advisory warnings, including ignored
        # provider-specific directives and options inherited by an
        # incompatible proxy mode. Treat them as test failures: this is the
        # default chart contract, not an operator-supplied custom config.
        if grep -qE '^[[:space:]]*\[WARNING\]|Warnings were found\.' <<< "$output"; then
            error "haproxy -c emitted warnings in pod $pod:"
            printf '%s\n' "$output" >&2
            die "Default chart generated an HAProxy configuration with warnings" 8
        fi
    done

    ok "haproxy -c is clean on all ${#pods[@]} HAProxy replicas"
}

verify_config_admission_warning_free() {
    # Tests ride the library objects inline (ADR-0017). An empty suite passes
    # the load gate vacuously, so its presence is asserted explicitly. Both
    # kinds are counted: a config may carry its own tests too, and looking only
    # at the config would report zero and fail for the wrong reason.
    local total_tests
    total_tests=$(kubectl get haproxytemplateconfigs,haproxytemplatelibraries -n "$NAMESPACE" \
        -l "app.kubernetes.io/instance=${RELEASE_NAME}" -o json 2>/dev/null \
        | python3 -c 'import json,sys; print(sum(len(i.get("spec",{}).get("validationTests") or {}) for i in json.load(sys.stdin)["items"]))')
    if [[ "${total_tests:-0}" -eq 0 ]]; then
        die "No HAProxyTemplateLibrary carries validationTests — an empty suite passes the load gate vacuously" 10
    fi
    info "Inline validationTests across the library objects: ${total_tests}"

    info "Re-applying the HAProxyTemplateConfig through apply-time admission (CEL + schema; warnings are fatal)..."

    local configs=()
    mapfile -t configs < <(
        kubectl get haproxytemplateconfigs -n "$NAMESPACE" \
            -l "app.kubernetes.io/instance=${RELEASE_NAME}" \
            -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'
    )
    if [[ ${#configs[@]} -ne 1 ]]; then
        die "Expected exactly one HAProxyTemplateConfig, found ${#configs[@]} — bulk content belongs in HAProxyTemplateLibrary objects" 10
    fi

    # Every library must be referenced at the revision it reports, or the
    # controller holds last-good and this install is not serving what the
    # chart rendered.
    kubectl get haproxytemplateconfig "${configs[0]}" -n "$NAMESPACE" -o json \
        | python3 -c '
import json, subprocess, sys

cfg = json.load(sys.stdin)
refs = cfg.get("spec", {}).get("libraryRefs") or []
if not refs:
    sys.exit("the config references no HAProxyTemplateLibrary — the libraries would not be merged at all")

namespace = cfg["metadata"]["namespace"]
raw = subprocess.run(
    ["kubectl", "get", "haproxytemplatelibraries", "-n", namespace, "-o", "json"],
    capture_output=True, text=True, check=True,
).stdout
observed = {i["metadata"]["name"]: i.get("spec", {}).get("revision") for i in json.loads(raw)["items"]}

for ref in refs:
    name = ref["name"]
    want = ref["revision"]
    got = observed.get(name)
    if got is None:
        sys.exit("libraryRefs names %s, which does not exist" % name)
    if got != want:
        sys.exit("%s is at revision %r, but the config expects %r" % (name, got, want))

print("All %d library references resolve at the revision they name" % len(refs))
' || die "The config'"'"'s libraryRefs do not resolve — the controller would hold the last-good configuration" 10

    # A server-side dry-run replace routes each object through apply-time
    # validation (the CRD schema and its CEL completeness rule — the
    # per-object config webhook is retired). Re-get inside a retry: the
    # controller writes status between our get and replace, bumping
    # resourceVersion, so a stale replace 409s with "object has been
    # modified". Retry only that optimistic-concurrency conflict.
    local name output rc attempt
    for name in "${configs[@]}"; do
        rc=0
        for attempt in 1 2 3 4 5; do
            rc=0
            output=$(kubectl get haproxytemplateconfig "$name" -n "$NAMESPACE" -o json \
                | kubectl replace --dry-run=server -f - 2>&1) || rc=$?
            if [[ $rc -ne 0 ]] && grep -q 'please apply your changes to the latest version' <<< "$output"; then
                sleep 1
                continue
            fi
            break
        done
        if [[ $rc -ne 0 ]]; then
            error "HAProxyTemplateConfig $name server-side dry-run failed (exit $rc):"
            printf '%s\n' "$output" >&2
            die "Default HAProxyTemplateConfig failed apply-time validation" 10
        fi
        if grep -q '^Warning:' <<< "$output"; then
            error "HAProxyTemplateConfig $name admission emitted warnings:"
            grep '^Warning:' <<< "$output" >&2
            die "Default HAProxyTemplateConfig was not accepted cleanly at apply time" 10
        fi
    done
    ok "All ${#configs[@]} HAProxyTemplateConfig objects pass apply-time validation without warnings"

    # The authoritative gate. The controller merges the set, runs the suite on
    # load, and crash-loops rather than serve a config whose own tests fail.
    # The verdict is a property of the merged set and is stamped on EVERY
    # source at its own generation (ADR-0016), so each object is checked.
    local gen observed status reason
    for name in "${configs[@]}"; do
        gen=$(kubectl get haproxytemplateconfig "$name" -n "$NAMESPACE" -o jsonpath='{.metadata.generation}')
        if ! kubectl wait --for=condition=Validated "haproxytemplateconfig/$name" \
            -n "$NAMESPACE" --timeout="${TIMEOUT}s" >/dev/null 2>&1; then
            status=$(kubectl get haproxytemplateconfig "$name" -n "$NAMESPACE" \
                -o jsonpath='{.status.conditions[?(@.type=="Validated")].status}' 2>/dev/null || true)
            reason=$(kubectl get haproxytemplateconfig "$name" -n "$NAMESPACE" \
                -o jsonpath='{.status.conditions[?(@.type=="Validated")].message}' 2>/dev/null || true)
            error "Load gate did not accept $name within ${TIMEOUT}s:"
            error "  Validated=${status:-<none>}"
            error "  message: ${reason:-<none>}"
            die "Default HAProxyTemplateConfig set was rejected by the controller's load gate" 10
        fi
        # Validated=True alone could be a leftover from an earlier generation,
        # so pin it to the spec the API server is serving right now.
        observed=$(kubectl get haproxytemplateconfig "$name" -n "$NAMESPACE" \
            -o jsonpath='{.status.conditions[?(@.type=="Validated")].observedGeneration}' 2>/dev/null || true)
        if [[ "$observed" != "$gen" ]]; then
            error "HAProxyTemplateConfig $name Validated=True is stale:"
            error "  generation=$gen observedGeneration=${observed:-<none>}"
            die "HAProxyTemplateConfig status lags the current generation" 10
        fi
    done

    ok "All ${#configs[@]} config sources accepted by the load gate (Validated=True at current generation)"
}

verify_bootstrap_workers_retired() {
    info "Checking that bootstrap HAProxy workers retire after the first reload..."

    local pods=()
    mapfile -t pods < <(
        kubectl get pods -n "$NAMESPACE" \
            -l "app.kubernetes.io/component=loadbalancer" \
            -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'
    )
    if [[ ${#pods[@]} -eq 0 ]]; then
        die "No HAProxy pods found for worker-retirement validation" 9
    fi

    local pod output rc reloads old_workers deadline
    for pod in "${pods[@]}"; do
        deadline=$((SECONDS + 30))
        while true; do
            output=""
            rc=0
            output=$(kubectl exec -n "$NAMESPACE" "$pod" -c haproxy -- sh -c \
                "printf 'show proc\\n' | socat - UNIX-CONNECT:/etc/haproxy/haproxy-master.sock" 2>&1) || rc=$?
            if [[ $rc -ne 0 ]]; then
                error "Could not inspect HAProxy processes in pod $pod (exit $rc):"
                printf '%s\n' "$output" >&2
                die "HAProxy worker-retirement validation failed in pod $pod" 9
            fi

            reloads=$(awk '$2 == "master" {print $3; exit}' <<< "$output")
            old_workers=$(awk '
                /^# old workers/ { in_old_workers=1; next }
                in_old_workers && $1 ~ /^[0-9]+$/ { print }
            ' <<< "$output")

            if [[ "${reloads:-0}" =~ ^[0-9]+$ ]] && (( reloads > 0 )) && [[ -z "$old_workers" ]]; then
                break
            fi
            if (( SECONDS >= deadline )); then
                error "Bootstrap HAProxy worker did not retire in pod $pod:"
                printf '%s\n' "$output" >&2
                kubectl exec -n "$NAMESPACE" "$pod" -c haproxy -- \
                    ps -eo pid,ppid,stat,etime,cmd >&2 || true
                die "Default bootstrap worker survived beyond hard-stop-after" 9
            fi
            sleep 1
        done
    done

    ok "Bootstrap workers retired on all ${#pods[@]} HAProxy replicas"
}

# Fails if the controller could not apply something its own templates rendered.
#
# This is the ONLY gate for that class, and it has to read the log because the
# controller deliberately swallows the failure: the resource applier logs at ERROR and
# returns an error outcome that is never counted, never reflected in a status condition,
# never exported as a metric, and never propagated to an exit code. It also never gives
# up — the checksum cache is written only on the success path, so a permanently-forbidden
# apply is retried on every reconciliation forever.
#
# The class this catches: the chart renders a Kubernetes resource via a library's
# k8sResources whose gate is broader than the RBAC gate in templates/role.yaml. That
# shipped once — on pure defaults the chart rendered a Valkey rate-limit StatefulSet and
# PDB it had no permission to apply, producing 704 forbidden applies in 74 seconds while
# every job stayed green. Any future kind added to a k8sResources entry without a
# matching grant fails here.
#
# Position matters: call this AFTER start_port_forward, which has already blocked until
# the controller-applied Service exists. That proves at least one applier pass completed,
# so no sleep or poll loop is needed.
verify_controller_applies_clean() {
    info "Checking that the controller applied every rendered k8sResource..."

    local logs
    logs=$(kubectl logs -n "$NAMESPACE" \
        -l "app.kubernetes.io/component=controller" -c controller \
        --tail=-1 2>/dev/null || true)

    if [[ -z "$logs" ]]; then
        die "Could not read controller logs, so the resource-applier check could not run" 11
    fi

    # Deliberately narrow: these two messages are unambiguous defects on a default
    # install. A bare `level=ERROR` match would be flaky — a real run showed transient
    # apply failures against pods that were still coming up.
    local failures
    failures=$(grep -E 'level=ERROR.*(Failed to apply rendered resource|Failed to resolve GVR for rendered resource)' <<<"$logs" || true)

    if [[ -n "$failures" ]]; then
        error "The controller rendered resources it cannot apply:"
        # Distinct GVR/name pairs, so a hot loop of thousands of lines stays readable.
        grep -oE 'name=[^ ]+ gvr="[^"]+"' <<<"$failures" | sort -u | sed 's/^/    /' >&2
        error "Total failed applies: $(wc -l <<<"$failures")"
        die "Chart RBAC does not cover what the chart's templates render (see templates/role.yaml)" 11
    fi

    ok "No resource-applier failures in the controller log"
}

#------------------------------------------------------------------------------
# Certificate verification
#------------------------------------------------------------------------------

# wait_for_resource <kind> <grep_pattern> <log_label> <die_message>
# Polls "kubectl get <kind> -o name | grep <grep_pattern>" up to 10 times
# (2s apart) for the race against cert-manager. On failure, lists all
# resources of that kind and dies with exit code 5.
wait_for_resource() {
    local kind="$1" pattern="$2" label="$3" die_message="$4"
    local found=false
    for ((i=1; i<=10; i++)); do
        if kubectl get "$kind" -n "$NAMESPACE" -o name 2>/dev/null | grep -q "$pattern"; then
            found=true
            break
        fi
        info "$label not found yet (attempt $i/10)..."
        sleep 2
    done
    if [[ "$found" != "true" ]]; then
        warn "$label not found, listing all ${kind}s:"
        kubectl get "$kind" -n "$NAMESPACE" -o wide || true
        die "$die_message" 5
    fi
}

# wait_cert_ready <name>
# Polls the Ready condition of certificate <name> up to 30 times (5s apart).
# On failure, dumps status/describe and dies with exit code 5.
wait_cert_ready() {
    local name="$1"
    local retries=30
    local ready=false
    for ((i=1; i<=retries; i++)); do
        local status
        status=$(kubectl get certificate "$name" -n "$NAMESPACE" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "")
        if [[ "$status" == "True" ]]; then
            ready=true
            break
        fi
        info "Certificate not ready yet (attempt $i/$retries)..."
        sleep 5
    done

    if [[ "$ready" != "true" ]]; then
        warn "Certificate status:"
        kubectl get certificate -n "$NAMESPACE" -o wide || true
        kubectl describe certificate "$name" -n "$NAMESPACE" || true
        die "Certificate '$name' not ready within timeout" 5
    fi
}

verify_certificates() {
    info "Verifying certificate resources (cert-manager SSL + chart self-signed webhook)..."

    # Check SSL Issuer (with retry for race condition with cert-manager)
    info "Checking SSL Issuer..."
    wait_for_resource issuer "ssl-selfsigned" "SSL Issuer" "SSL self-signed Issuer not found"

    # Check SSL Certificate (with retry for race condition with cert-manager)
    info "Checking SSL Certificate..."
    wait_for_resource certificate "default-ssl-cert" "SSL Certificate" "default-ssl-cert Certificate not found"

    # Wait for certificate to be ready
    info "Waiting for SSL certificate to be ready..."
    wait_cert_ready default-ssl-cert

    # Check TLS Secret exists
    info "Checking SSL TLS Secret..."
    if ! kubectl get secret default-ssl-cert -n "$NAMESPACE" -o jsonpath='{.type}' | grep -q "kubernetes.io/tls"; then
        die "TLS secret 'default-ssl-cert' not found or wrong type" 5
    fi

    # Webhook serving cert: chart-native self-signed by default (no cert-manager).
    # The chart renders the TLS Secret itself and injects the CA straight into the
    # ValidatingWebhookConfiguration's caBundle, so there is no webhook Issuer or
    # Certificate to wait for. Verify the Secret carries a CA and that the caBundle
    # was actually wired (the crux of the zero-dependency default).
    info "Checking Webhook TLS Secret (chart self-signed)..."
    if ! kubectl get secret "${RELEASE_NAME}-webhook-tls" -n "$NAMESPACE" -o jsonpath='{.type}' | grep -q "kubernetes.io/tls"; then
        die "TLS secret '${RELEASE_NAME}-webhook-tls' not found or wrong type" 5
    fi
    if [[ -z "$(kubectl get secret "${RELEASE_NAME}-webhook-tls" -n "$NAMESPACE" -o jsonpath='{.data.ca\.crt}' 2>/dev/null)" ]]; then
        die "Webhook TLS secret '${RELEASE_NAME}-webhook-tls' missing ca.crt" 5
    fi

    info "Checking ValidatingWebhookConfiguration caBundle is wired..."
    # The ValidatingWebhookConfiguration is cluster-scoped, so the chart
    # namespace-qualifies its name (<fullname>-<namespace>-webhook). With the
    # default release name (== fullname), that is ${RELEASE_NAME}-${NAMESPACE}-webhook.
    # The webhook Service keeps the un-qualified ${RELEASE_NAME}-webhook name.
    local vwc="${RELEASE_NAME}-${NAMESPACE}-webhook"
    local ca_bundle
    ca_bundle=$(kubectl get validatingwebhookconfiguration "$vwc" \
        -o jsonpath='{.webhooks[0].clientConfig.caBundle}' 2>/dev/null || echo "")
    if [[ -z "$ca_bundle" ]]; then
        kubectl get validatingwebhookconfiguration "$vwc" -o yaml || true
        die "ValidatingWebhookConfiguration '$vwc' has empty caBundle (self-signed CA not injected)" 5
    fi

    ok "All certificate resources verified"
}

#------------------------------------------------------------------------------
# Smoke tests
#------------------------------------------------------------------------------

# Start port-forward in background and return the PID
# This is more reliable than NodePort in DinD environments
PORT_FORWARD_PID=""
start_port_forward() {
    info "Starting port-forward to HAProxy service..."

    # The HAProxy LoadBalancer Service (svc/${RELEASE_NAME}-haproxy) is
    # emitted at controller runtime (k8sResources.haproxy-service in
    # libraries/base.yaml) — NOT at helm install time. So even after pods
    # are Ready, the Service can take a few seconds to materialize while
    # the controller boots, watches resources, renders, and SSA-applies.
    # kubectl port-forward against a non-existent Service exits non-zero
    # immediately, so we have to wait for the Service to exist.
    local svc_attempts=30
    local svc_attempt=1
    while ! kubectl get svc -n "$NAMESPACE" "${RELEASE_NAME}-haproxy" >/dev/null 2>&1; do
        if [[ $svc_attempt -ge $svc_attempts ]]; then
            die "HAProxy Service '${RELEASE_NAME}-haproxy' did not appear within ${svc_attempts}s — controller may not have started or k8sResources.haproxy-service apply failed" 6
        fi
        if [[ $svc_attempt -eq 1 ]]; then
            info "Waiting for controller-rendered HAProxy Service to appear..."
        fi
        sleep 1
        ((svc_attempt++)) || true
    done
    ok "HAProxy Service exists (after ${svc_attempt}s)"

    # Start port-forward in background. We forward the chart's static
    # ports (80, 443) AND the always-bound stats port (8404) — the
    # smoke tests use 8404 as the liveness probe (the chart's HTTP /
    # HTTPS frontends only bind when an Ingress / Gateway / annotation
    # turns them on, so a chart-default install has no listener on 80
    # or 443; the status frontend is always rendered).
    kubectl port-forward -n "$NAMESPACE" "svc/${RELEASE_NAME}-haproxy" 8080:80 8443:443 8404:8404 >/dev/null 2>&1 &
    PORT_FORWARD_PID=$!

    # Wait for port-forward to be ready (probe stats — always bound).
    local max_attempts=15
    local attempt=1

    while [[ $attempt -le $max_attempts ]]; do
        if curl -s -o /dev/null --connect-timeout 1 "http://localhost:8404/healthz" 2>/dev/null; then
            ok "Port-forward is ready (pid: $PORT_FORWARD_PID)"
            return 0
        fi

        if ! kill -0 "$PORT_FORWARD_PID" 2>/dev/null; then
            die "Port-forward process died unexpectedly" 6
        fi

        if [[ $attempt -lt $max_attempts ]]; then
            info "Port-forward not ready yet (attempt $attempt/$max_attempts), waiting 1s..."
            sleep 1
        fi
        ((attempt++)) || true
    done

    kill "$PORT_FORWARD_PID" 2>/dev/null || true
    die "Port-forward not accessible after $max_attempts attempts" 6
}

stop_port_forward() {
    if [[ -n "$PORT_FORWARD_PID" ]] && kill -0 "$PORT_FORWARD_PID" 2>/dev/null; then
        info "Stopping port-forward (pid: $PORT_FORWARD_PID)"
        kill "$PORT_FORWARD_PID" 2>/dev/null || true
        wait "$PORT_FORWARD_PID" 2>/dev/null || true
        PORT_FORWARD_PID=""
    fi
}

smoke_test_http() {
    info "Running HTTP smoke test..."

    # Probe the stats port: always bound, returns 200 OK on /healthz
    # regardless of whether HTTP / HTTPS frontends are bound. The
    # chart's HTTP / HTTPS binds only render when at least one Ingress
    # / Gateway / annotation turns them on (the chart-default install
    # has no routing resources, so port 80 / 443 are deliberately
    # unbound). Probing /healthz on stats covers the smoke-test scope —
    # "the chart deployed cleanly and HAProxy is alive" — without
    # requiring routing fixtures.
    local url="http://localhost:8404/healthz"

    info "Testing: $url (via port-forward)"

    local http_code
    http_code=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 10 "$url")

    info "HAProxy /healthz response code: $http_code"

    if [[ "$http_code" == "200" ]]; then
        ok "HTTP smoke test passed (HAProxy /healthz returned 200 — process is alive)"
        return 0
    elif [[ "$http_code" == "000" ]]; then
        die "HTTP smoke test failed — connection refused or timeout on stats port" 6
    else
        die "HTTP smoke test failed — /healthz returned $http_code (expected 200)" 6
    fi
}

smoke_test_https() {
    info "Running HTTPS smoke test (Prometheus metrics endpoint)..."

    # Same rationale as smoke_test_http: probe the always-bound stats
    # port (which also serves /metrics) instead of the optional HTTPS
    # frontend. The metrics endpoint confirms the prometheus exporter
    # is wired and HAProxy is producing data.
    local url="http://localhost:8404/metrics"

    info "Testing: $url (via port-forward)"

    local http_code
    http_code=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 10 "$url")

    info "HAProxy /metrics response code: $http_code"

    if [[ "$http_code" == "200" ]]; then
        ok "HTTPS smoke test passed (HAProxy /metrics returned 200 — Prometheus exporter is wired)"
        return 0
    elif [[ "$http_code" == "000" ]]; then
        die "HTTPS smoke test failed — connection refused or timeout on stats port" 7
    else
        die "HTTPS smoke test failed — /metrics returned $http_code (expected 200)" 7
    fi
}

verify_ssl_certificate() {
    info "Verifying SSL certificate via openssl..."

    # The chart's HTTPS frontend (libraries/ssl.yaml) only renders when
    # an Ingress / Gateway / annotation requests HTTPS routing. The
    # chart-default install has no routing fixtures, so port 443 isn't
    # bound and an openssl/curl probe at localhost:8443 (forwarded to
    # pod:443) gets connection-refused — not a chart bug, just nothing
    # to verify against. Skip the SSL chain check and report that
    # cleanly; the previous smoke tests have already confirmed HAProxy
    # is alive (stats /healthz + /metrics).
    local cert_info
    if cert_info=$(timeout 10 openssl s_client -connect "localhost:8443" -servername localhost </dev/null 2>/dev/null | openssl x509 -noout -subject -issuer 2>/dev/null); then
        if [[ -n "$cert_info" ]]; then
            info "Certificate info:"
            echo "$cert_info"
            ok "SSL certificate verification passed"
            return 0
        fi
    fi

    info "openssl could not retrieve a certificate at localhost:8443 — chart-default install has no HTTPS frontend (libraries/ssl.yaml renders only when an Ingress / Gateway / annotation turns it on); SSL chain verification is therefore not applicable to this smoke test scope."
    ok "SSL certificate verification skipped (no HTTPS frontend in chart-default install)"
}

#------------------------------------------------------------------------------
# Debug output (on failure)
#------------------------------------------------------------------------------

dump_debug_info() {
    echo ""
    error "============================================"
    error "SMOKE TEST FAILED - Debug Information"
    error "============================================"
    echo ""

    echo "=== Pod Status ==="
    kubectl get pods -A 2>/dev/null || true
    echo ""

    echo "=== Events (last 50) ==="
    kubectl get events -A --sort-by='.lastTimestamp' 2>/dev/null | tail -50 || true
    echo ""

    echo "=== Controller Logs ==="
    kubectl logs -n "$NAMESPACE" -l "app.kubernetes.io/component=controller" --all-containers --prefix --tail=50000 2>/dev/null || true
    echo ""

    # --all-containers + --prefix so agent and spoa-hub stdout are visible
    # and tagged with their container; default kubectl logs only emits the
    # first container's output, which is haproxy — masking agent crashes.
    # --previous gets stdout from the last terminated instance of crashlooping
    # containers (the current instance might still be starting / not have
    # written anything yet when the smoke test gives up).
    # --tail=50000: the spoa-hub can emit hundreds of lines per second under
    # load; small tails clip the failure window out of the artifact entirely
    # (see tests/e2e/cleanup.go for the same reasoning).
    echo "=== HAProxy Pod Logs (current) ==="
    kubectl logs -n "$NAMESPACE" -l "app.kubernetes.io/component=loadbalancer" --all-containers --prefix --tail=50000 2>/dev/null || true
    echo ""

    echo "=== HAProxy Pod Logs (previous, for crashlooping containers) ==="
    kubectl logs -n "$NAMESPACE" -l "app.kubernetes.io/component=loadbalancer" --all-containers --prefix --previous --tail=50000 2>/dev/null || true
    echo ""

    echo "=== Certificates ==="
    kubectl get certificates -A -o wide 2>/dev/null || true
    echo ""

    echo "=== Issuers ==="
    kubectl get issuers -A -o wide 2>/dev/null || true
    echo ""

    echo "=== Secrets (TLS type) ==="
    kubectl get secrets -A --field-selector type=kubernetes.io/tls 2>/dev/null || true
    echo ""

    echo "=== HAProxy Service ==="
    kubectl get svc -n "$NAMESPACE" -o wide 2>/dev/null || true
    echo ""

    echo "=== HAProxy Endpoints ==="
    kubectl get endpoints -n "$NAMESPACE" 2>/dev/null || true
    echo ""

    echo "=== HAProxy Config (frontends only) ==="
    local haproxy_pod
    haproxy_pod=$(kubectl get pods -n "$NAMESPACE" -l "app.kubernetes.io/component=loadbalancer" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
    if [[ -n "$haproxy_pod" ]]; then
        kubectl exec -n "$NAMESPACE" "$haproxy_pod" -- cat /etc/haproxy/haproxy.cfg 2>/dev/null | grep -A5 "^frontend" || true
    fi
    echo ""

    # The pre-rollout gate runs the load gate in a Job pod labelled
    # component=pre-rollout-validation, so neither selector above reaches it.
    # When it fails, helm reports only "BackoffLimitExceeded" and the reason
    # (which validationTest, which OOM) exists solely in this log.
    echo "=== Hook Jobs ==="
    kubectl get jobs -n "$NAMESPACE" -o wide 2>/dev/null || true
    kubectl describe jobs -n "$NAMESPACE" 2>/dev/null | grep -E "^Name:|^Pods Statuses:|Warning|Error" || true
    echo ""

    echo "=== Hook and failed pod logs ==="
    local pod
    for pod in $(kubectl get pods -n "$NAMESPACE" -o json 2>/dev/null | python3 -c '
import json, sys
try:
    pods = json.load(sys.stdin).get("items", [])
except Exception:
    raise SystemExit
for p in pods:
    labels = p["metadata"].get("labels", {})
    hook = labels.get("app.kubernetes.io/component", "").endswith("-validation")
    if hook or p.get("status", {}).get("phase") in ("Failed", "Unknown"):
        print(p["metadata"]["name"])' 2>/dev/null); do
        echo "--- $pod ---"
        kubectl logs -n "$NAMESPACE" "$pod" --all-containers --prefix --tail=50000 2>&1 || true
    done
    echo ""
}

#------------------------------------------------------------------------------
# Cleanup
#------------------------------------------------------------------------------

cleanup() {
    local exit_code=$?

    # Stop port-forward if running
    stop_port_forward

    if [[ $exit_code -ne 0 ]]; then
        # Also to a file: this trap deletes the cluster below, so a CI
        # after_script has nothing left to query and can only collect what
        # was captured here, while it was still alive.
        mkdir -p debug-logs
        dump_debug_info 2>&1 | tee -a debug-logs/diagnostics.log
    fi

    if [[ "$KEEP_CLUSTER" != "true" ]]; then
        delete_cluster
    else
        info "Keeping cluster '$CLUSTER_NAME' for debugging"
        info "Context: kind-$CLUSTER_NAME"
    fi

    # Clean up temp kind config if created
    if [[ -n "$TEMP_KIND_CONFIG" && -f "$TEMP_KIND_CONFIG" ]]; then
        rm -f "$TEMP_KIND_CONFIG"
    fi

    exit $exit_code
}

#------------------------------------------------------------------------------
# Help
#------------------------------------------------------------------------------

show_help() {
    head -40 "$0" | grep -E "^#" | sed 's/^#//' | sed 's/^ //'
    exit 0
}

#------------------------------------------------------------------------------
# Main
#------------------------------------------------------------------------------

main() {
    # Parse arguments
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --keep-cluster)
                KEEP_CLUSTER="true"
                shift
                ;;
            --image)
                IMAGE="$2"
                shift 2
                ;;
            --namespace)
                NAMESPACE="$2"
                shift 2
                ;;
            --help|-h)
                show_help
                ;;
            *)
                die "Unknown option: $1. Use --help for usage."
                ;;
        esac
    done

    # Set up cleanup trap
    trap cleanup EXIT

    echo ""
    info "============================================"
    info "Helm Chart Default Values Test"
    info "============================================"
    info "Cluster:   $CLUSTER_NAME"
    info "Namespace: $NAMESPACE"
    info "Image:     ${IMAGE:-<chart default>}"
    info "Timeout:   ${TIMEOUT}s"
    info "DinD:      $(is_docker_in_docker && echo "yes" || echo "no")"
    echo ""

    # Run test steps
    if cluster_exists; then
        info "Cluster '$CLUSTER_NAME' already exists, reusing it"
    else
        create_cluster
    fi
    install_cert_manager
    install_helm_chart
    # Verify certificates BEFORE waiting for pods - pods need the cert secrets to start
    verify_certificates
    wait_for_pods
    verify_config_admission_warning_free
    verify_haproxy_configs_warning_free
    verify_bootstrap_workers_retired
    # Start port-forward for smoke tests (more reliable than NodePort in DinD)
    start_port_forward
    smoke_test_http
    smoke_test_https
    verify_ssl_certificate
    verify_controller_applies_clean
    stop_port_forward

    echo ""
    ok "============================================"
    ok "ALL SMOKE TESTS PASSED"
    ok "============================================"
    echo ""
}

main "$@"
