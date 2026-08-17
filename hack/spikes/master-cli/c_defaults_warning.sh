#!/bin/bash
# Question C: is 'defaults haptic-implicit from haptic-base' as the LAST defaults
# section (never referenced by 'from') warning-free under haproxy -c?
set -u
. "$(dirname "$0")/lib.sh"
VER="${1:-3.4}"

# C1 — the HAPTIC pattern.
cat > "$W/c1.cfg" <<'EOF'
global
    log stdout format raw local0 info

defaults haptic-base
    mode http
    timeout connect 5s
    timeout client 30s
    timeout server 30s

defaults haptic-http from haptic-base
    retries 3

defaults haptic-implicit from haptic-base

frontend fe from haptic-http
    bind :8080
    default_backend b1

frontend fe2
    bind :8081
    default_backend b2

backend b1 from haptic-base
    server s1 127.0.0.1:9000

backend b2
    server s1 127.0.0.1:9000

backend b3 from haptic-http
    server s1 127.0.0.1:9000
EOF

# C2 — negative control: the LAST defaults is haptic-base itself, and it is both
#      explicitly referenced and implicitly used.
cat > "$W/c2.cfg" <<'EOF'
global
    log stdout format raw local0 info

defaults haptic-base
    mode http
    timeout connect 5s
    timeout client 30s
    timeout server 30s

frontend fe from haptic-base
    bind :8080
    default_backend b1

backend b1
    server s1 127.0.0.1:9000
EOF

# C3 — negative control: a proxy without 'from' sits BEFORE haptic-implicit, so it
#      implicitly uses haptic-base which is also explicitly referenced.
cat > "$W/c3.cfg" <<'EOF'
global
    log stdout format raw local0 info

defaults haptic-base
    mode http
    timeout connect 5s
    timeout client 30s
    timeout server 30s

backend b0
    server s1 127.0.0.1:9000

defaults haptic-implicit from haptic-base

frontend fe from haptic-base
    bind :8080
    default_backend b0
EOF

# C4 — the HAPTIC pattern plus a runtime-shaped extra: haptic-implicit is the last
#      section and there are NO proxies without 'from' at all.
cat > "$W/c4.cfg" <<'EOF'
global
    log stdout format raw local0 info

defaults haptic-base
    mode http
    timeout connect 5s
    timeout client 30s
    timeout server 30s

defaults haptic-implicit from haptic-base

frontend fe from haptic-base
    bind :8080
    default_backend b1

backend b1 from haptic-base
    server s1 127.0.0.1:9000
EOF

C=hapC
docker rm -f $C >/dev/null 2>&1
docker run -d --name $C -v "$W:/cfg" --entrypoint sleep "haproxytech/haproxy-debian:$VER" 3600 >/dev/null
echo "############ HAProxy $VER ############"
cx "haproxy -v | head -1"
for f in c1 c2 c3 c4; do
  hr "$f.cfg  ->  haproxy -dr -c"
  cx "haproxy -dr -c -f /cfg/$f.cfg 2>&1; echo rc=\$?"
  echo "--- warnings only:"
  cx "haproxy -dr -c -f /cfg/$f.cfg 2>&1 | grep -i 'warning\|implicitly\|explicitly' || echo '(none)'"
done

hr "C5. exact wording of the 'explicitly referenced / implicitly used' warning"
cx "haproxy -dr -c -f /cfg/c2.cfg 2>&1 | grep -i 'defaults' || echo '(no defaults-related line)'"

docker rm -f $C >/dev/null 2>&1
