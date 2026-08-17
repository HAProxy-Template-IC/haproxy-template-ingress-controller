#!/bin/bash
# Shared helpers for the master-socket spike.
#
# Every container is started EXACTLY like the shipped HAPTIC pod:
#   haproxy -dr -W -db -S /etc/haproxy/haproxy-master.sock,level,admin -- /etc/haproxy/haproxy.cfg
# There is NO worker `stats socket` in any config used here.

SPIKE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
W="$SPIKE_DIR/w"
OUT="$SPIKE_DIR/out"
mkdir -p "$W" "$OUT"

MSOCK=/etc/haproxy/haproxy-master.sock
C=""   # current container name

# start <container> <version> <cfg-file-in-W> [extra docker run args...]
start() {
  local name="$1" ver="$2" cfg="$3"; shift 3
  C="$name"
  docker rm -f "$name" >/dev/null 2>&1
  docker run -d --name "$name" -v "$W:/cfg" "$@" \
    --entrypoint haproxy \
    "haproxytech/haproxy-debian:$ver" \
    -dr -W -db -S "$MSOCK,level,admin" -- "/cfg/$cfg" >/dev/null || return 1
  # wait for the master socket to exist AND for a worker to be registered.
  # (the socket accepts connections before the worker exists; '@1' then fails
  #  with "Can't find the target PID matching the prefix '@1'")
  for _ in $(seq 1 100); do
    if docker exec "$name" sh -c "test -S $MSOCK && echo '@1 show info' | socat -t2 stdio unix-connect:$MSOCK" 2>/dev/null \
       | grep -q '^Name: HAProxy'; then return 0; fi
    sleep 0.1
  done
  echo "!! master socket never appeared for $name"; docker logs "$name" 2>&1 | tail -20; return 1
}

stop() { [ -n "$C" ] && docker rm -f "$C" >/dev/null 2>&1; C=""; }

# mc <timeout> -- payload comes from stdin; one TCP^Wunix connection to the MASTER socket.
mc_raw() {
  local t="${1:-5}"
  cat > "$W/.cmd"
  docker exec "$C" sh -c "socat -t$t stdio unix-connect:$MSOCK < /cfg/.cmd" 2>&1
}

# mc "<one line>"  (single connection, single line)
mc() { printf '%s\n' "$1" | mc_raw "${MC_T:-5}"; }

# curlh <host-header-args...> ; runs curl inside the container
cx() { docker exec "$C" sh -c "$1" 2>&1; }

hr() { echo; echo "=== $* ==="; }
