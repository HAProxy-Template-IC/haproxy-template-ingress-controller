#!/bin/bash
# Build the cross-version keyword table from the d_server_keywords-*.txt outputs.
D="$(cd "$(dirname "$0")" && pwd)"
KEYS="check inter fastinter downinter rise fall port addr weight maxconn maxqueue minconn backup cookie guid ssl sni verify verifyhost ca-file crt crl-file alpn ciphers ciphersuites ssl-min-ver ssl-max-ver proto send-proxy send-proxy-v2 slowstart agent-check agent-port agent-inter agent-send on-marked-down observe init-state disabled enabled no-check"
printf '%-16s %-8s %-8s %-8s %-8s %-8s\n' KEYWORD 3.0 3.1 3.2 3.3 3.4
printf '%-16s %-8s %-8s %-8s %-8s %-8s\n' ---------------- -------- -------- -------- -------- --------
for k in $KEYS; do
  line=$(printf '%-16s' "$k")
  for v in 3.0 3.1 3.2 3.3 3.4; do
    f="$D/out/d_server_keywords-$v.txt"
    val=$(awk -v k="$k" '/=== D1\. keyword matrix/{i=1} /=== D1b\./{i=0} i && $1==k && NF>1 {print $2; exit}' "$f")
    [ -z "$val" ] && val="?"
    line="$line $(printf '%-8s' "$val")"
  done
  echo "$line"
done
echo
echo "== non-ACCEPTED verdicts, verbatim =="
for v in 3.0 3.1 3.2 3.3 3.4; do
  f="$D/out/d_server_keywords-$v.txt"
  echo "--- $v"
  awk '/=== D1\. keyword matrix/,/=== D1b\./' "$f" | grep -E '(UNKNOWN|OTHER)' | sed 's/^/    /'
done
echo
echo "== D1b (ssl files preloaded) =="
for v in 3.0 3.1 3.2 3.3 3.4; do
  echo "--- $v"; awk '/=== D1b\./,/=== D2\./' "$D/out/d_server_keywords-$v.txt" | grep '^  pre_'
done
echo
echo "== D2 balance matrix =="
for v in 3.0 3.1 3.2 3.3 3.4; do
  echo "--- $v"; awk '/=== D2\./,/=== D3\./' "$D/out/d_server_keywords-$v.txt" | grep '^  balance'
done
echo
echo "== D4 time-to-traffic =="
for v in 3.0 3.1 3.2 3.3 3.4; do
  echo "--- $v"; awk '/=== D4\./,/=== D5\./' "$D/out/d_server_keywords-$v.txt" | grep -vE '^$|^=== D5'
done
echo
echo "== D0 add server usage strings =="
for v in 3.0 3.1 3.2 3.3 3.4; do
  echo "--- $v"; awk '/--- @1 add server$/,/--- @1 add server help/' "$D/out/d_server_keywords-$v.txt" | head -4
  grep -A1 -- "--- @1 add server kw/h2" "$D/out/d_server_keywords-$v.txt" | head -2
done
