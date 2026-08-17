#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"
VER="${1:-3.4}"
trap stop_hap EXIT
start_hap "$VER" "$SPIKE_DIR/mapenv" || exit 1
say "version"
cli "show info" | grep -E '^(Version|Process_num)'
say "show map (str)"
cli "show map /etc/haproxy/maps/str.map"
say "curl /s1"
curl -sS "http://127.0.0.1:$HTTP_PORT/s1" -H 'Host: sub.example.com'
say "master: show proc"
mcli "show proc"
