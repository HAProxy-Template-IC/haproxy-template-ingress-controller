#!/bin/sh
# Runs INSIDE the container.
# usage: ein.sh <backend> <teardown-mode> <client-mode> [<upstream-port>]
#   teardown-mode: disable | disable_shutsess | shutsess_disable | nothing
#   client-mode:   idle  = one request, then hold the keep-alive connection open
#                  inflight = a request that is still waiting for the upstream
#                  none  = one plain curl, connection closed, only the pooled
#                          server connection remains
M=/etc/haproxy/haproxy-master.sock
BE="$1"; MODE="$2"; CLIENT="$3"; PORT="${4:-9000}"
cli() { printf '@1 %s\n' "$1" | socat -t30 stdio unix-connect:$M 2>&1 | grep -v '^$'; }
now() { date +%s%3N; }
conns() { cli "show servers conn $BE" | grep "^$BE/"; }

cli "add server $BE/s1 127.0.0.1:$PORT check inter 300ms rise 1 fall 1" >/dev/null
cli "enable health $BE/s1" >/dev/null
cli "enable server $BE/s1" >/dev/null
sleep 0.3

case "$CLIENT" in
  idle)
    ( printf 'GET / HTTP/1.1\r\nHost: h\r\nx-be: %s\r\nConnection: keep-alive\r\n\r\n' "$BE"; sleep 60 ) \
      | socat -t60 - TCP:127.0.0.1:8080 > /tmp/hold-$BE.out 2>&1 &
    sleep 1
    echo "  client keep-alive got: [$(head -1 /tmp/hold-$BE.out | tr -d '\r')]"
    ;;
  inflight)
    ( printf 'GET / HTTP/1.1\r\nHost: h\r\nx-be: %s\r\nConnection: keep-alive\r\n\r\n' "$BE"; sleep 60 ) \
      | socat -t60 - TCP:127.0.0.1:8080 > /tmp/hold-$BE.out 2>&1 &
    sleep 0.5
    echo "  client request in flight (upstream sleeps 5s), response so far: [$(head -1 /tmp/hold-$BE.out | tr -d '\r')]"
    ;;
  none)
    curl -s -o /dev/null -H "x-be: $BE" http://127.0.0.1:8080/
    sleep 0.2
    ;;
esac

echo "  show servers conn: $(conns)"
echo "  streams on $BE:    $(cli "show sess" | grep -c "be=$BE")"

T0=$(now)
case "$MODE" in
  disable)          echo "  disable server -> [$(cli "disable server $BE/s1")]" ;;
  disable_shutsess) echo "  disable server -> [$(cli "disable server $BE/s1")]"
                    echo "  shutdown sessions server -> [$(cli "shutdown sessions server $BE/s1")]" ;;
  shutsess_disable) echo "  shutdown sessions server -> [$(cli "shutdown sessions server $BE/s1")]"
                    echo "  disable server -> [$(cli "disable server $BE/s1")]" ;;
  nothing)          : ;;
esac
echo "  show servers conn after teardown: $(conns)"

T1=$(now); R=$(cli "wait 2s srv-removable $BE/s1"); T2=$(now)
echo "  wait 2s srv-removable -> [$R]  took $((T2-T1))ms  (t+$((T2-T0))ms after teardown began)"

if [ "$R" != "Done." ]; then
  T3=$(now); R2=$(cli "wait 30s srv-removable $BE/s1"); T4=$(now)
  echo "  wait 30s srv-removable -> [$R2]  took $((T4-T3))ms  (t+$((T4-T0))ms after teardown began)"
fi
echo "  del server -> [$(cli "del server $BE/s1")]"
