#!/bin/sh
# Runs INSIDE the container so the measurement is not dominated by docker-exec.
# usage: d4in.sh <backend> <addargs...> ; the "enable" pair is timed, then curl is
# polled until the backend answers 200.
M=/etc/haproxy/haproxy-master.sock
BE="$1"; shift
MODE="$1"; shift
ADD="$*"
cli() { printf '@1 %s\n' "$1" | socat -t5 stdio unix-connect:$M 2>&1 | grep -v '^$'; }
now() { date +%s%3N; }

printf 'add: %s\n' "$(cli "add server $BE/s1 127.0.0.1:9000 $ADD")"
printf 'state after add:    %s\n' "$(cli "show servers state $BE" | sed -n '3p' | awk '{print "op_state="$6" admin_state="$7" check_status="$11" check_health="$13}')"
T0=$(now)
case "$MODE" in
  enable)        cli "enable health $BE/s1" >/dev/null; cli "enable server $BE/s1" >/dev/null ;;
  enable_health) cli "enable health $BE/s1" >/dev/null; cli "enable server $BE/s1" >/dev/null; cli "set server $BE/s1 health up" >/dev/null ;;
  health_only)   cli "set server $BE/s1 health up" >/dev/null ;;
  server_only)   cli "enable server $BE/s1" >/dev/null ;;
  health_first)  cli "enable health $BE/s1" >/dev/null ;;
  none)          : ;;
esac
T1=$(now)
printf 'state after enable: %s   (cli calls took %sms)\n' \
  "$(cli "show servers state $BE" | sed -n '3p' | awk '{print "op_state="$6" admin_state="$7" check_status="$11" check_health="$13}')" "$((T1-T0))"
i=0
while [ $i -lt 400 ]; do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 -H "x-be: $BE" http://127.0.0.1:8080/)
  if [ "$code" = "200" ]; then
    printf 'FIRST 200 at t+%sms after the enable pair started\n' "$(( $(now) - T0 ))"
    exit 0
  fi
  i=$((i+1))
done
printf 'never served 200 (last code %s) after %sms\n' "$code" "$(( $(now) - T0 ))"
