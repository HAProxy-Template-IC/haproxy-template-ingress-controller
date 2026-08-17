#!/usr/bin/env bash
# Probe which map identifier (if any) the Dataplane API accepts for runtime
# map-entry operations in this setup.
set -uo pipefail
B="http://127.0.0.1:${PORT:-5555}/v3/services/haproxy/runtime/maps"
for name in "%2Fetc%2Fhaproxy%2Fmaps%2Fhost.map" "maps%2Fhost.map" "host.map"; do
  code=$(curl -su admin:admin -X POST "$B/$name/entries" \
    -H 'Content-Type: application/json' \
    -d '{"key":"probe.example.com","value":"gtw_default_bench-route-0_bench-svc-0_8080"}' \
    -o /tmp/probe-body.txt -w '%{http_code}')
  echo "POST $name -> $code $(head -c 200 /tmp/probe-body.txt)"
done
echo "--- worker view ---"
docker exec "${NAME:-spike-dpapi}" sh -c 'echo "@1 show map /etc/haproxy/maps/host.map" | socat - UNIX-CONNECT:/etc/haproxy/haproxy-master.sock | head -3'
