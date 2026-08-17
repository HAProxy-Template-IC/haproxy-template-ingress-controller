#!/usr/bin/env bash
# Build the Section C fixture: a CA, four leaf certs, a crt-list and a client cert.
set -e
D="$(cd "$(dirname "$0")" && pwd)/certenv/ssl"
rm -rf "$D"; mkdir -p "$D"
cd "$D"

mkca() { # mkca <name>
  openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
    -keyout "$1.key" -out "$1.crt" -subj "/CN=$1" 2>/dev/null
}

leaf() { # leaf <name> <cn> <serialtag>
  openssl req -newkey rsa:2048 -nodes -keyout "$1.key" -out "$1.csr" \
    -subj "/CN=$2/OU=$3" 2>/dev/null
  openssl x509 -req -in "$1.csr" -CA ca.crt -CAkey ca.key -CAcreateserial \
    -days 3650 -out "$1.crt" \
    -extfile <(printf 'subjectAltName=DNS:%s\n' "$2") 2>/dev/null
  cat "$1.key" "$1.crt" > "$1.pem"
  rm -f "$1.csr"
}

mkca ca
mkca ca2

leaf a a.test  gen1
leaf b b.test  gen1
leaf c c.test  gen1
leaf d d.test  gen1
leaf e e.test  gen1
# second-generation replacements, distinguishable by OU
leaf a2 a.test gen2
leaf b2 b.test gen2

# client cert for the ca-file / verify test
openssl req -newkey rsa:2048 -nodes -keyout client.key -out client.csr \
  -subj "/CN=client1" 2>/dev/null
openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
  -days 3650 -out client.crt 2>/dev/null
cat client.key client.crt > client.pem
# a client cert signed by the OTHER CA -- only accepted after ca-file is updated
openssl req -newkey rsa:2048 -nodes -keyout client2.key -out client2.csr \
  -subj "/CN=client2" 2>/dev/null
openssl x509 -req -in client2.csr -CA ca2.crt -CAkey ca2.key -CAcreateserial \
  -days 3650 -out client2.crt 2>/dev/null
cat client2.key client2.crt > client2.pem
rm -f client.csr client2.csr

printf '/etc/haproxy/ssl/b.pem\n' > list.txt

chmod 644 ./*.pem ./*.crt ./*.key list.txt
ls -1 "$D"
