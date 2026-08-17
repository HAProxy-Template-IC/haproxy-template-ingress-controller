#!/usr/bin/env bash
# Shared harness: start an HAProxy container like the shipped pod and talk to
# the worker through the master CLI socket.
set -uo pipefail

SPIKE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_DIR="$SPIKE_DIR/out"
mkdir -p "$OUT_DIR"

CNAME=""
RUNDIR=""
MSOCK=""
SHORTLINK=""
HTTP_PORT=""
HTTPS_PORT=""
HTTPS2_PORT=""

freeport() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }

# start_hap <version> <workdir-with-haproxy.cfg> [extra docker args...]
start_hap() {
  local ver="$1"; shift
  local wd="$1"; shift
  CNAME="hapspike-${ver//./}-$$-$RANDOM"
  RUNDIR="$wd/run"
  rm -rf "$RUNDIR"
  mkdir -p "$RUNDIR"
  chmod 777 "$RUNDIR"
  # AF_UNIX paths are capped at ~108 bytes; the scratchpad path alone exceeds
  # that, so reach the socket through a short symlink.
  SHORTLINK="/tmp/hs.$$.$RANDOM"
  ln -sfn "$RUNDIR" "$SHORTLINK"
  MSOCK="$SHORTLINK/master.sock"
  WSOCK="$SHORTLINK/admin.sock"
  HTTP_PORT=$(freeport)
  HTTPS_PORT=$(freeport)
  HTTPS2_PORT=$(freeport)
  docker run -d --name "$CNAME" \
    --user "$(id -u):$(id -g)" \
    -v "$wd:/etc/haproxy" \
    -p "127.0.0.1:$HTTP_PORT:8080" \
    -p "127.0.0.1:$HTTPS_PORT:8443" \
    -p "127.0.0.1:$HTTPS2_PORT:8444" \
    "$@" \
    haproxytech/haproxy-debian:"$ver" \
    haproxy -W -db -S /etc/haproxy/run/master.sock,level,admin -f /etc/haproxy/haproxy.cfg >/dev/null || return 1
  local i
  for i in $(seq 1 100); do
    if [ -S "$RUNDIR/master.sock" ]; then sleep 0.3; return 0; fi
    if ! docker ps -q -f name="$CNAME" | grep -q .; then
      echo "!! container died:"
      docker logs "$CNAME" 2>&1 | tail -30
      return 1
    fi
    sleep 0.1
  done
  echo "!! master socket never appeared"
  docker logs "$CNAME" 2>&1 | tail -30
  return 1
}

stop_hap() {
  if [ -n "$CNAME" ]; then docker rm -f "$CNAME" >/dev/null 2>&1; fi
  if [ -n "$SHORTLINK" ]; then rm -f "$SHORTLINK"; fi
  CNAME=""
  SHORTLINK=""
}

WSOCK=""
HAPCLI="python3 $SPIKE_DIR/hapcli.py"

# send  -> stdin goes verbatim to the master socket, full reply on stdout
send() { $HAPCLI "$MSOCK" 2>&1; }
# wsend -> same, but to the worker's own stats socket
wsend() { $HAPCLI "$WSOCK" 2>&1; }

# cli '<command>'  -> sends "@1 <command>" (single command to worker 1)
cli() { printf '@1 %s\n' "$1" | send; }

# wcli '<command>' -> same command straight to the worker's own stats socket
wcli() { printf '%s\n' "$1" | wsend; }

# pay <cmdline> <payload-body>  -> payload command through the master.
# "@1 <cmd> <<" is the only form that works on BOTH 3.0 and 3.4; "@@1" session
# mode does not exist before 3.1 ("Can't find the target PID matching '@@1'").
pay() { { printf '@1 %s <<\n' "$1"; printf '%s\n\n' "$2"; } | send; }

# paysess <cmdline> <payload-body> -> the "@@1" session form (3.1+ only)
paysess() { { printf '@@1\n'; printf '%s <<\n' "$1"; printf '%s\n\n' "$2"; } | send; }

# wpay <cmdline> <payload-body> -> payload command straight to the worker socket
wpay() { { printf '%s <<\n' "$1"; printf '%s\n\n' "$2"; } | wsend; }

# payfile <cmdline> <file> -> payload command whose body is a file, through the master
payfile() { { printf '@1 %s <<\n' "$1"; cat "$2"; printf '\n'; } | send; }

# mcli '<command>' -> sends command to the MASTER process itself
mcli() { printf '%s\n' "$1" | socat -t5 stdio "unix-connect:$MSOCK" 2>&1; }

# cli_raw  -> pipes stdin verbatim into the master socket
cli_raw() { socat -t5 stdio "unix-connect:$MSOCK" 2>&1; }

# sess '<multi-line block>' -> "@@1" session mode, then the block
sess() { { printf '@@1\n'; printf '%s\n' "$1"; } | socat -t5 stdio "unix-connect:$MSOCK" 2>&1; }

hdrs() { curl -sS -D- -o /dev/null "http://127.0.0.1:$HTTP_PORT$1" 2>&1; }
say() { echo; echo "########## $* ##########"; }
