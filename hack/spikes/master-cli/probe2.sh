#!/bin/bash
set -u
. "$(dirname "$0")/lib.sh"
cp "$SPIKE_DIR/cfg-b.cfg" "$W/b.cfg"
cat > "$W/slowresp.sh" <<'EOF'
#!/bin/sh
sleep 5
printf 'HTTP/1.1 200 OK\r\nContent-Length: 4\r\nContent-Type: text/plain\r\n\r\nslow'
EOF
chmod +x "$W/slowresp.sh"
cat > "$W/slowup.sh" <<'EOF'
#!/bin/sh
exec socat -T120 TCP-LISTEN:9100,reuseaddr,fork EXEC:/cfg/slowresp.sh
EOF
chmod +x "$W/slowup.sh"
start hapP2 3.4 b.cfg || exit 1
docker exec -d hapP2 /cfg/slowup.sh
sleep 0.5
echo "--- is socat listening?"; cx "ss -ltnp 2>/dev/null | grep 9100 || netstat -ltnp 2>/dev/null | grep 9100 || echo '(no ss/netstat)'; ps aux | grep -c '[s]ocat'"
echo "--- direct curl to the slow upstream:"
cx "curl -s -i --max-time 12 -w 'TOTAL=%{time_total}\n' http://127.0.0.1:9100/ | tr -d '\r' | head -8"
echo "--- via haproxy backend ctrlSlow (no set-timeout):"
cx "curl -s -o /dev/null --max-time 25 -w 'code=%{http_code} total=%{time_total}\n' -H 'x-be: ctrlSlow' http://127.0.0.1:8080/"
echo "--- via haproxy backend fileSlow (be-http, set-timeout server 2s):"
cx "curl -s -o /dev/null --max-time 25 -w 'code=%{http_code} total=%{time_total}\n' -H 'x-be: fileSlow' http://127.0.0.1:8080/"
stop
