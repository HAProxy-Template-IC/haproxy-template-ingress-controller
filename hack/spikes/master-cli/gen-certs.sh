#!/bin/bash
# Generate the CA / client cert / CRL that the 'ssl'-family 'add server' keyword
# probes need, so a rejection can only mean "keyword unsupported", never "bad file".
set -eu
D="$(cd "$(dirname "$0")" && pwd)/w/tls"
rm -rf "$D"; mkdir -p "$D/demoCA/newcerts"
cd "$D"
: > demoCA/index.txt; echo 01 > demoCA/crlnumber; echo 01 > demoCA/serial
cat > openssl.cnf <<'EOF'
[ ca ]
default_ca = CA_default
[ CA_default ]
dir             = ./demoCA
database        = $dir/index.txt
new_certs_dir   = $dir/newcerts
certificate     = ./ca.crt
serial          = $dir/serial
private_key     = ./ca.key
crlnumber       = $dir/crlnumber
default_days    = 3650
default_crl_days= 3650
default_md      = sha256
policy          = policy_any
[ policy_any ]
commonName = supplied
EOF
openssl req -x509 -newkey rsa:2048 -nodes -keyout ca.key -out ca.crt -days 3650 -subj "/CN=spike-ca" 2>/dev/null
openssl req -newkey rsa:2048 -nodes -keyout srv.key -out srv.csr -subj "/CN=spike-srv" 2>/dev/null
openssl x509 -req -in srv.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out srv.crt -days 3650 2>/dev/null
cat srv.crt srv.key > client.pem
( cd . && openssl ca -config openssl.cnf -gencrl -out crl.pem 2>/dev/null ) || \
  openssl ca -config openssl.cnf -gencrl -out crl.pem
ls -1
