#!/bin/sh
# Runs INSIDE the container.  usage:
#   ein2.sh <backend> <teardown> <client> <port> "<wait cmd>" <hold-seconds> <framing>
#     teardown: disable | disable_shutsess | nothing
#     client:   idle | inflight | none
#     framing:  at1        one-shot '@1', write side closed  (the 'printf | socat' idiom)
#               at1open    one-shot '@1', write side held open for <hold> seconds
#               at2closed  '@@1' session, write side closed immediately
#               at2open    '@@1' session, write side held open for <hold> seconds
M=/etc/haproxy/haproxy-master.sock
BE="$1"; MODE="$2"; CLIENT="$3"; PORT="$4"; WCMD="$5"; HOLD="$6"; FRAMING="$7"
ms() { date +%s%3N; }

# 3.0 has no '@@' in its master CLI; fall back to one '@1' per command.
AT2=1
printf '@@1\nshow info\n' | socat -t3 stdio unix-connect:$M 2>&1 | grep -q "Can't find the target PID" && AT2=0
cli() {
  if [ "$AT2" = 1 ]; then
    printf '@@1\n%s\n' "$1" | socat -t10 stdio unix-connect:$M 2>&1 | grep -v '^$'
  else
    printf '%s\n' "$1" | tr ';' '\n' | sed 's/^[[:space:]]*//' | while IFS= read -r c; do
      [ -z "$c" ] && continue
      printf '@1 %s\n' "$c" | socat -t10 stdio unix-connect:$M 2>&1 | grep -v '^$'
    done
  fi
}
[ "$AT2" = 0 ] && echo "  (this build has no '@@1' master-CLI session mode)"

cli "add server $BE/s1 127.0.0.1:$PORT check inter 300ms rise 1 fall 1; enable health $BE/s1; enable server $BE/s1" >/dev/null
sleep 0.3
case "$CLIENT" in
  idle)
    ( printf 'GET / HTTP/1.1\r\nHost: h\r\nx-be: %s\r\nConnection: keep-alive\r\n\r\n' "$BE"; sleep 90 ) \
      | socat -t90 - TCP:127.0.0.1:8080 > /tmp/h-$BE.out 2>&1 &
    sleep 1; echo "  client got: [$(head -1 /tmp/h-$BE.out | tr -d '\r')] (connection still open and idle)" ;;
  inflight)
    ( printf 'GET / HTTP/1.1\r\nHost: h\r\nx-be: %s\r\nConnection: keep-alive\r\n\r\n' "$BE"; sleep 90 ) \
      | socat -t90 - TCP:127.0.0.1:8080 > /tmp/h-$BE.out 2>&1 &
    sleep 0.5; echo "  client request still in flight (upstream sleeps 5s)" ;;
  none)
    curl -s -o /dev/null -H "x-be: $BE" http://127.0.0.1:8080/; sleep 0.2 ;;
esac
echo "  conns before: $(cli "show servers conn $BE" | grep "^$BE/")"

T0=$(ms)
case "$MODE" in
  disable)          cli "disable server $BE/s1" >/dev/null ;;
  disable_shutsess) cli "disable server $BE/s1; shutdown sessions server $BE/s1" >/dev/null ;;
  nothing)          : ;;
esac
echo "  conns after teardown: $(cli "show servers conn $BE" | grep "^$BE/")"

echo "  --- $FRAMING: $WCMD"
case "$FRAMING" in
  at1)       ( printf '@1 %s\n' "$WCMD" ) | socat -t$((HOLD+5)) stdio unix-connect:$M 2>&1 ;;
  at1open)   ( printf '@1 %s\n' "$WCMD"; sleep "$HOLD" ) | socat -t$((HOLD+5)) stdio unix-connect:$M 2>&1 ;;
  at2closed) ( printf '@@1\n%s\n' "$WCMD" ) | socat -t$((HOLD+5)) stdio unix-connect:$M 2>&1 ;;
  at2open)   ( printf '@@1\n%s\n' "$WCMD"; sleep "$HOLD" ) | socat -t$((HOLD+5)) stdio unix-connect:$M 2>&1 ;;
esac | while IFS= read -r l; do [ -n "$l" ] && echo "    t+$(( $(ms) - T0 ))ms  [$l]"; done

echo "  del server -> [$(cli "del server $BE/s1")]"
