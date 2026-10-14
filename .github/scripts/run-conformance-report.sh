#!/usr/bin/env bash
set -euo pipefail

case "${1:-report}" in
    report) CONFORMANCE_TARGET=github-conformance-report ;;
    startup) CONFORMANCE_TARGET=github-conformance-startup ;;
    *) echo "Unknown conformance operation" >&2; exit 1 ;;
esac
mkdir -p build/gateway-conformance debug-logs/conformance-report
{
    lscpu
    free -b
    df -B1 . /var/lib/docker
} > debug-logs/conformance-report/host.txt
python3 .github/scripts/runner-metrics.py debug-logs/conformance-report/host-metrics.jsonl &
metrics_pid=$!
cleanup() {
    kill "$metrics_pid" 2>/dev/null || true
    wait "$metrics_pid" 2>/dev/null || true
    sudo chown -R "$(id -u):$(id -g)" build/gateway-conformance debug-logs
}
trap cleanup EXIT
test "$(uname -m)" = aarch64
make test-github-native-tools > debug-logs/conformance-report/native-tools.txt 2>&1
make "$CONFORMANCE_TARGET"
