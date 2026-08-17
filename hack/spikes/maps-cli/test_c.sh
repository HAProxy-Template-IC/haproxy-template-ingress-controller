#!/usr/bin/env bash
# Section C -- runtime certificates through the master socket.
# Usage: test_c.sh <haproxy-version>
source "$(dirname "$0")/lib.sh"
VER="${1:-3.4}"
SSL=/etc/haproxy/ssl
HOSTSSL="$SPIKE_DIR/certenv/ssl"
LIST=$SSL/list.txt
trap stop_hap EXIT
start_hap "$VER" "$SPIKE_DIR/certenv" || exit 1
echo "=== $(cli 'show info' | grep -m1 ^Version:) (image $VER) ==="

# served <port> <sni> -> subject + OU (gen tag) + serial of the cert HAProxy presents
served() {
  echo | openssl s_client -connect "127.0.0.1:$1" -servername "$2" 2>/dev/null \
    | openssl x509 -noout -subject -serial 2>/dev/null | tr '\n' ' '
  echo
}
pemof() { cat "$HOSTSSL/$1"; }
setcert() { # setcert <runtime-name> <host-pem-file>
  { printf '@1 set ssl cert %s <<\n' "$1"; pemof "$2"; printf '\n'; } | send
}

say "C6  how the runtime identifies a cert: 'show ssl cert'"
cli "show ssl cert"
echo "-- detail for the bind's cert:"
cli "show ssl cert $SSL/a.pem" | head -14

say "C-setup  what is served before any runtime change"
echo "  8443 SNI a.test : $(served "$HTTPS_PORT" a.test)"
echo "  8443 SNI b.test : $(served "$HTTPS_PORT" b.test)"
echo "  8443 SNI zz.test: $(served "$HTTPS_PORT" zz.test)   (falls back to the bind default)"
echo "-- crt-list content:"
cli "show ssl crt-list -n $LIST"

# --------------------------------------------------------------------------
say "C1  set ssl cert + commit ssl cert for a cert referenced by 'bind ... crt'"
echo "-- set ssl cert $SSL/a.pem << (a2.pem, OU=gen2):"
setcert "$SSL/a.pem" a2.pem | sed 's/^/    /'
echo "-- served BEFORE commit (must still be gen1):"
echo "    $(served "$HTTPS_PORT" a.test)"
echo "-- show ssl cert $SSL/a.pem (committed view):"
cli "show ssl cert $SSL/a.pem" | grep -E 'Subject|Serial|Status|not' | head -6 | sed 's/^/    /'
echo "-- show ssl cert *$SSL/a.pem (the pending/transaction view uses a leading '*'):"
cli "show ssl cert *$SSL/a.pem" | grep -E 'Subject|Serial|Status|not' | head -6 | sed 's/^/    /'
echo "-- commit ssl cert $SSL/a.pem:"
cli "commit ssl cert $SSL/a.pem" | sed 's/^/    /'
echo "-- served AFTER commit (expect OU=gen2, new serial):"
echo "    $(served "$HTTPS_PORT" a.test)"
echo "-- 8444 (second bind referencing the same file) also updated?"
echo "    $(served "$HTTPS2_PORT" a.test)"

say "C1b same for a cert referenced only by a CRT-LIST (b.pem)"
echo "-- before: $(served "$HTTPS_PORT" b.test)"
setcert "$SSL/b.pem" b2.pem | sed 's/^/    /'
cli "commit ssl cert $SSL/b.pem" | sed 's/^/    /'
echo "-- after:  $(served "$HTTPS_PORT" b.test)"

# --------------------------------------------------------------------------
say "C2  abort ssl cert / a second set while one is uncommitted"
echo "-- set ssl cert $SSL/a.pem << (back to a.pem gen1), do NOT commit:"
setcert "$SSL/a.pem" a.pem | sed 's/^/    /'
echo "-- served (unchanged, still gen2): $(served "$HTTPS_PORT" a.test)"
echo "-- a SECOND set while the first is uncommitted (a2.pem again):"
setcert "$SSL/a.pem" a2.pem | sed 's/^/    /'
echo "-- pending view after the second set:"
cli "show ssl cert *$SSL/a.pem" | grep -E 'Subject|Serial' | sed 's/^/    /'
echo "-- abort ssl cert $SSL/a.pem:"
cli "abort ssl cert $SSL/a.pem" | sed 's/^/    /'
echo "-- abort again (nothing pending):"
cli "abort ssl cert $SSL/a.pem" | sed 's/^/    /'
echo "-- commit after abort (nothing pending):"
cli "commit ssl cert $SSL/a.pem" | sed 's/^/    /'
echo "-- served: $(served "$HTTPS_PORT" a.test)"
echo "-- pending view after abort:"
cli "show ssl cert *$SSL/a.pem" | head -3 | sed 's/^/    /'

say "C2b set ssl cert with a BROKEN payload (certificate without its private key)"
{ printf '@1 set ssl cert %s <<\n' "$SSL/a.pem"; cat "$HOSTSSL/e.crt"; printf '\n'; } | send | sed 's/^/    /'
echo "-- commit the broken transaction:"
cli "commit ssl cert $SSL/a.pem" | sed 's/^/    /'
echo "-- served (must be unchanged): $(served "$HTTPS_PORT" a.test)"
cli "abort ssl cert $SSL/a.pem" >/dev/null 2>&1
echo "-- set ssl cert with GARBAGE payload:"
{ printf '@1 set ssl cert %s <<\n' "$SSL/a.pem"; printf 'not a pem at all\n'; printf '\n'; } | send | sed 's/^/    /'
cli "abort ssl cert $SSL/a.pem" >/dev/null 2>&1
echo "-- served (must be unchanged): $(served "$HTTPS_PORT" a.test)"

# --------------------------------------------------------------------------
say "C3  bring a NEW SNI cert online with no reload: new ssl cert + set + commit + add ssl crt-list"
NEW=$SSL/c.pem
echo "-- served for c.test BEFORE: $(served "$HTTPS_PORT" c.test)"
echo "-- new ssl cert $NEW:"
cli "new ssl cert $NEW" | sed 's/^/    /'
echo "-- show ssl cert right after 'new':"
cli "show ssl cert" | grep -F "$NEW" | sed 's/^/    /'
echo "-- set ssl cert $NEW << (c.pem):"
setcert "$NEW" c.pem | sed 's/^/    /'
echo "-- commit ssl cert $NEW:"
cli "commit ssl cert $NEW" | sed 's/^/    /'
echo "-- is it served yet (still no crt-list entry)? $(served "$HTTPS_PORT" c.test)"
echo "-- add ssl crt-list $LIST $NEW:"
cli "add ssl crt-list $LIST $NEW" | sed 's/^/    /'
echo "-- served now: $(served "$HTTPS_PORT" c.test)"
echo "-- show ssl crt-list -n $LIST:"
cli "show ssl crt-list -n $LIST" | sed 's/^/    /'

say "C3b 'new ssl cert' for a name with NO file on disk"
GHOST=$SSL/ghost.pem
cli "new ssl cert $GHOST" | sed 's/^/    /'
setcert "$GHOST" d.pem | sed 's/^/    /'
cli "commit ssl cert $GHOST" | sed 's/^/    /'
cli "add ssl crt-list $LIST $GHOST" | sed 's/^/    /'
echo "-- ghost.pem does not exist on disk: $(ls "$HOSTSSL/ghost.pem" 2>&1 | head -1)"
echo "-- served for d.test: $(served "$HTTPS_PORT" d.test)"

say "C3c 'add ssl crt-list' for a file that IS on disk but was never loaded into the store"
echo "-- e.pem exists on disk: $(ls -la "$HOSTSSL/e.pem" | awk '{print $NF, $5" bytes"}')"
cli "add ssl crt-list $LIST $SSL/e.pem" | sed 's/^/    /'
echo "-- served for e.test: $(served "$HTTPS_PORT" e.test)"
echo "-- => the store, not the filesystem, is what 'add ssl crt-list' resolves against."
echo "-- load it properly first (new + set + commit), then add:"
cli "new ssl cert $SSL/e.pem" | sed 's/^/    /'
setcert "$SSL/e.pem" e.pem | sed 's/^/    /'
cli "commit ssl cert $SSL/e.pem" | sed 's/^/    /'
cli "add ssl crt-list $LIST $SSL/e.pem" | sed 's/^/    /'
echo "-- served for e.test now: $(served "$HTTPS_PORT" e.test)"
echo "-- show ssl cert now lists:"
cli "show ssl cert" | sed 's/^/    /'

# --------------------------------------------------------------------------
say "C4  add/del ssl crt-list syntax: ssl options and SNI filters"
echo "-- put d.pem in the store first (new + set + commit):"
cli "new ssl cert $SSL/d.pem" | sed 's/^/    /'
setcert "$SSL/d.pem" d.pem | sed 's/^/    /'
cli "commit ssl cert $SSL/d.pem" | sed 's/^/    /'
echo "-- payload form with SNI filters: 'add ssl crt-list <list> <<' + '<cert> [opts] [!]<sni>...'"
{ printf '@1 add ssl crt-list %s <<\n' "$LIST"; \
  printf '%s/d.pem [alpn h2,http/1.1] alias.test *.wild.test\n' "$SSL"; printf '\n'; } | send | sed 's/^/    /'
echo "-- show ssl crt-list -n $LIST:"
cli "show ssl crt-list -n $LIST" | sed 's/^/    /'
echo "-- served for alias.test:     $(served "$HTTPS_PORT" alias.test)"
echo "-- served for x.wild.test:    $(served "$HTTPS_PORT" x.wild.test)"
echo "-- served for d.test (its CN, but not a listed filter): $(served "$HTTPS_PORT" d.test)"
echo "-- line form with an ssl option + filter (cert already in the store):"
cli "add ssl crt-list $LIST $SSL/e.pem [alpn http/1.1] e2.test" | sed 's/^/    /'
echo "-- served for e2.test: $(served "$HTTPS_PORT" e2.test)"
echo "-- line form with a NEGATIVE filter '!bad.test':"
cli "add ssl crt-list $LIST $SSL/e.pem [alpn http/1.1] *.neg.test !bad.neg.test" | sed 's/^/    /'
echo "-- served for ok.neg.test:  $(served "$HTTPS_PORT" ok.neg.test)"
echo "-- served for bad.neg.test: $(served "$HTTPS_PORT" bad.neg.test)"
echo "-- show ssl crt-list (no -n):"
cli "show ssl crt-list $LIST" | sed 's/^/    /'
echo "-- show ssl crt-list (no args, list the lists):"
cli "show ssl crt-list" | sed 's/^/    /'

say "C4b del ssl crt-list"
echo "-- del ssl crt-list $LIST $SSL/c.pem:"
cli "del ssl crt-list $LIST $SSL/c.pem" | sed 's/^/    /'
echo "-- served for c.test now: $(served "$HTTPS_PORT" c.test)"
echo "-- crt-list now:"
cli "show ssl crt-list -n $LIST" | sed 's/^/    /'
echo "-- del a cert that has SEVERAL lines in the list (d.pem appears twice):"
cli "del ssl crt-list $LIST $SSL/d.pem" | sed 's/^/    /'
cli "show ssl crt-list -n $LIST" | sed 's/^/    /'
echo "-- del by line-number form 'del ssl crt-list <list> <cert> [<line>]' is not offered; delete the remaining one:"
cli "del ssl crt-list $LIST $SSL/d.pem" | sed 's/^/    /'
cli "show ssl crt-list -n $LIST" | sed 's/^/    /'
echo "-- del the LAST/only cert of a bind (b.pem):"
cli "del ssl crt-list $LIST $SSL/b.pem" | sed 's/^/    /'
cli "show ssl crt-list -n $LIST" | sed 's/^/    /'
echo "-- served for b.test after removal: $(served "$HTTPS_PORT" b.test)"
echo "-- del a cert not in the list:"
cli "del ssl crt-list $LIST $SSL/zzz.pem" | sed 's/^/    /'
echo "-- del ssl cert for a cert still referenced:"
cli "del ssl cert $SSL/a.pem" | sed 's/^/    /'
echo "-- del ssl cert for an unreferenced one (c.pem, removed from the list above):"
cli "del ssl cert $SSL/c.pem" | sed 's/^/    /'
cli "show ssl cert" | sed 's/^/    /'

# --------------------------------------------------------------------------
say "C5  ca-file: show / set / add + commit  (client cert signed by ca2 must start failing/passing)"
CAF=$SSL/ca.crt
echo "-- show ssl ca-file:"
cli "show ssl ca-file" | sed 's/^/    /'
echo "-- show ssl ca-file $CAF:"
cli "show ssl ca-file $CAF" | sed 's/^/    /'
mtls() { # mtls <clientpem>
  curl -sS --max-time 5 --cacert "$HOSTSSL/ca.crt" --resolve "a.test:$HTTPS2_PORT:127.0.0.1" \
    ${1:+--cert "$HOSTSSL/$1" --key "$HOSTSSL/${1%.pem}.key"} \
    "https://a.test:$HTTPS2_PORT/" 2>&1 | head -2
}
echo "-- mTLS with client.pem (signed by ca):  $(mtls client.pem)"
echo "-- mTLS with client2.pem (signed by ca2): $(mtls client2.pem)"
echo "-- set ssl ca-file $CAF << (ca2.crt ONLY -- replaces the whole file):"
{ printf '@1 set ssl ca-file %s <<\n' "$CAF"; cat "$HOSTSSL/ca2.crt"; printf '\n'; } | send | sed 's/^/    /'
echo "-- before commit, client2 still rejected: $(mtls client2.pem)"
echo "-- commit ssl ca-file $CAF:"
cli "commit ssl ca-file $CAF" | sed 's/^/    /'
echo "-- after commit, client2: $(mtls client2.pem)"
echo "-- after commit, client1: $(mtls client.pem)"
echo "-- add ssl ca-file $CAF << (append ca.crt back):"
{ printf '@1 add ssl ca-file %s <<\n' "$CAF"; cat "$HOSTSSL/ca.crt"; printf '\n'; } | send | sed 's/^/    /'
echo "-- does 'add ssl ca-file' need a commit? client1 right after add: $(mtls client.pem)"
cli "commit ssl ca-file $CAF" | sed 's/^/    /'
echo "-- client1 after commit: $(mtls client.pem)"
echo "-- client2 after commit: $(mtls client2.pem)"
echo "-- abort ssl ca-file:"
{ printf '@1 set ssl ca-file %s <<\n' "$CAF"; cat "$HOSTSSL/ca2.crt"; printf '\n'; } | send >/dev/null
cli "abort ssl ca-file $CAF" | sed 's/^/    /'
echo "-- show ssl ca-file $CAF:"
cli "show ssl ca-file $CAF" | sed 's/^/    /'
echo "-- new ssl ca-file (a name not in the config):"
cli "new ssl ca-file $SSL/newca.crt" | sed 's/^/    /'
cli "show ssl ca-file" | sed 's/^/    /'

say "C6b exact naming: does the runtime accept a relative / differently-spelled path?"
echo "-- 'show ssl cert a.pem' (basename only):"
cli "show ssl cert a.pem" | head -3 | sed 's/^/    /'
echo "-- 'show ssl cert /etc/haproxy/ssl/../ssl/a.pem' (equivalent but different string):"
cli "show ssl cert /etc/haproxy/ssl/../ssl/a.pem" | head -3 | sed 's/^/    /'
echo "-- 'show ssl cert $SSL/a.pem' (exact config string):"
cli "show ssl cert $SSL/a.pem" | head -3 | sed 's/^/    /'
echo "-- crt-list entry naming: 'show ssl crt-list -n list.txt' (basename):"
cli "show ssl crt-list -n list.txt" | head -3 | sed 's/^/    /'
