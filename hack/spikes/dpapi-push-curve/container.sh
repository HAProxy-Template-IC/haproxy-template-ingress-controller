#!/usr/bin/env bash
# Start/stop the spike container: HAProxy in master-worker mode with an admin
# master socket, plus (optionally) dataplaneapi inside it.
#
#   container.sh up   <workdir> [with-dpapi|no-dpapi]
#   container.sh down
set -euo pipefail
IMAGE=${IMAGE:-haproxytech/haproxy-debian:3.4}
NAME=${NAME:-spike-dpapi}
PORT=${PORT:-5555}

case "${1:-}" in
up)
  workdir="$2"; mode="${3:-with-dpapi}"
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  rm -f "$workdir/haproxy-master.sock"
  docker run -d --name "$NAME" --user 0:0 -w /etc/haproxy \
    -v "$workdir:/etc/haproxy" \
    -p "127.0.0.1:${PORT}:${PORT}" \
    -e SELF_POD_NAME=spike -e SELF_NODE_NAME=spike \
    --entrypoint /usr/local/sbin/haproxy \
    "$IMAGE" \
    -dr -W -db -S "/etc/haproxy/haproxy-master.sock,mode,666,level,admin" \
    -f /etc/haproxy/haproxy.cfg >/dev/null
  # Wait for the master socket to answer. AF_UNIX paths cap at ~107 bytes, so
  # talk to it from inside the directory rather than by absolute path.
  for _ in $(seq 1 240); do
    if (cd "$workdir" && echo "show version" | socat -T2 stdio unix-connect:haproxy-master.sock) >/dev/null 2>&1; then
      break
    fi
    sleep 0.5
  done
  if [[ "$mode" == "with-dpapi" ]]; then
    docker exec "$NAME" mkdir -p /var/lib/dataplaneapi/transactions /var/lib/dataplaneapi/backups
    docker exec -d "$NAME" sh -c '/usr/local/bin/dataplaneapi -f /etc/haproxy/dataplaneapi.yaml > /etc/haproxy/dpapi.log 2>&1'
    for _ in $(seq 1 120); do
      if curl -fsu admin:admin "http://127.0.0.1:${PORT}/v3/info" >/dev/null 2>&1; then
        break
      fi
      sleep 0.5
    done
  fi
  docker ps --filter "name=$NAME" --format '{{.Names}} {{.Status}}'
  ;;
down)
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  echo "removed $NAME"
  ;;
*)
  echo "usage: container.sh up <workdir> [with-dpapi|no-dpapi] | container.sh down" >&2
  exit 2
  ;;
esac
