#!/usr/bin/env bash
#
# ============================================================================
#   HAPTIC playground — try your rendered config locally
# ============================================================================
#
#   This self-contained script carries the exact HAProxy configuration you
#   just rendered in the HAPTIC playground, plus every map, error file, and
#   TLS certificate it references. Running it lets you:
#
#     1. WRITE those files to a folder (default: ./haptic-tryout),
#     2. VALIDATE the config semantically  (haproxy -c),
#     3. RUN HAProxy locally               (Docker or Podman),
#     4. LAUNCH it as a Pod in a cluster   (kubectl).
#
#   Usage:
#     ./haptic-tryout.sh            # interactive menu
#     ./haptic-tryout.sh check      # validate only (haproxy -c)
#     ./haptic-tryout.sh run        # run locally in a container
#     ./haptic-tryout.sh k8s        # launch as a Pod via kubectl
#     ./haptic-tryout.sh files      # just write the files, do nothing else
#     ./haptic-tryout.sh emit-netpol # print an allow-egress NetworkPolicy (stdout)
#
#   Environment overrides:
#     HAPTIC_TRYOUT_DIR=./somewhere        where files are written
#     HAPTIC_TRYOUT_NAMESPACE=my-ns        kubectl namespace for 'k8s' mode
#     HAPTIC_TRYOUT_LABELS=k=v,k2=v2       extra Pod labels (match an existing
#                                          NetworkPolicy; see the k8s NetworkPolicy note)
#
#   ─────────────────────────────────────────────────────────────────────────
#   IMPORTANT — this is a STATIC snapshot, not a working ingress.
#
#   In a real deployment the HAPTIC controller is what keeps this config
#   alive: it watches your cluster and rewrites each backend's server list as
#   pods come and go. This script runs HAProxy WITHOUT that controller, so the
#   backend server IPs are frozen at the moment you rendered. If a target
#   app's pod restarts and gets a new IP, HAProxy here keeps sending traffic
#   to the old, dead IP and you will get 503 Service Unavailable. That is
#   expected. Use this to inspect, validate, and demo the config — not to
#   serve production traffic. For a live setup, install HAPTIC.
# ============================================================================
set -euo pipefail

# Portability note: this stays within bash 3.2 (macOS ships that as /bin/bash) —
# no associative arrays, no `${empty_array[@]}` under set -u. It also runs under
# WSL and Git-Bash/MSYS on Windows. On MSYS, stop the path mangler from rewriting
# container paths like /etc/haproxy and let `docker -v` accept a C:/… source.
export MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL='*'

# is_windows_bash reports Git-Bash / MSYS / Cygwin, where Docker paths and TTYs
# need special handling.
is_windows_bash() { case "$(uname -s 2>/dev/null)" in MINGW*|MSYS*|CYGWIN*) return 0 ;; *) return 1 ;; esac; }

HAPROXY_VERSION="__VERSION__"
IMAGE="haproxytech/haproxy-debian:${HAPROXY_VERSION}"
WORKDIR="${HAPTIC_TRYOUT_DIR:-./haptic-tryout}"
NAMESPACE="${HAPTIC_TRYOUT_NAMESPACE:-}"   # empty → kubectl's current namespace

# ---- pretty output (colours only on a TTY) ----------------------------------
if [ -t 1 ]; then B=$'\e[1m'; C=$'\e[36m'; Y=$'\e[33m'; G=$'\e[32m'; RD=$'\e[31m'; N=$'\e[0m'; else B=''; C=''; Y=''; G=''; RD=''; N=''; fi
say()   { printf '%s\n' "$*"; }
title() { printf '\n%s== %s ==%s\n' "${B}${C}" "$*" "$N"; }
note()  { printf '%s  %s%s\n' "$C" "$*" "$N"; }
warn()  { printf '%s!  %s%s\n' "$Y" "$*" "$N"; }
ok()    { printf '%s✓  %s%s\n' "$G" "$*" "$N"; }
oops()  { printf '%s✗  %s%s\n' "$RD" "$*" "$N" >&2; }

# base64 decode differs between GNU (-d) and BSD/macOS (-D).
B64D="base64 -d"; printf '' | base64 -d >/dev/null 2>&1 || B64D="base64 -D"

# ---- write the embedded rendered files --------------------------------------
write_files() {
  title "Writing rendered files into ${WORKDIR}"
  rm -rf "$WORKDIR"; mkdir -p "$WORKDIR"
__WRITE_FILES__
  ok "Wrote $(find "$WORKDIR" -type f | wc -l | tr -d ' ') files under ${WORKDIR}/"
  note "haproxy.cfg plus maps/ general/ ssl/ exactly as the config references them"
  note "(paths resolve from 'default-path origin /etc/haproxy')."
}

# ---- helpers ----------------------------------------------------------------
detect_runtime() {
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then RUNTIME=docker
  elif command -v podman >/dev/null 2>&1 && podman info >/dev/null 2>&1; then RUNTIME=podman
  else RUNTIME=""; fi
}
# Absolute path to the work dir, in a form the container runtime accepts. On
# Git-Bash `pwd -W` yields a Windows path (C:/…) that Docker Desktop mounts;
# elsewhere a normal POSIX path.
abs_workdir() { if is_windows_bash; then (cd "$WORKDIR" && pwd -W); else (cd "$WORKDIR" && pwd); fi; }
# kubectl with the namespace flag injected when one is set — a function rather
# than an array so it works on bash 3.2 (expanding an empty array under set -u
# errors there). NS_NOTE is the same flag for copy-paste hints.
kc() { kubectl ${NAMESPACE:+-n "$NAMESPACE"} "$@"; }
NS_NOTE=""; [ -n "$NAMESPACE" ] && NS_NOTE="-n $NAMESPACE"
# TCP ports HAProxy binds (from the frontend 'bind' lines).
bind_ports() { grep -oE '^[[:space:]]*bind[[:space:]]+[^[:space:]]*:[0-9]+' "$WORKDIR/haproxy.cfg" 2>/dev/null | grep -oE '[0-9]+$' | sort -un; }
# A hostname the config routes on, for a copy-pasteable curl example.
sample_host() { cat "$WORKDIR"/maps/*host*.map 2>/dev/null | awk 'NF && $1 !~ /^#/ {print $1; exit}'; }

# ---- mode: validate (haproxy -c) --------------------------------------------
do_check() {
  detect_runtime
  [ -n "$RUNTIME" ] || { oops "Neither Docker nor Podman is available — cannot run 'haproxy -c'."; return 1; }
  title "Validating the config with 'haproxy -c'  (${RUNTIME} · ${IMAGE})"
  note "This parses haproxy.cfg and every file it references and reports any semantic error."
  if "$RUNTIME" run --rm -v "$(abs_workdir):/etc/haproxy:ro" "$IMAGE" \
       haproxy -c -f /etc/haproxy/haproxy.cfg; then
    ok "Config is semantically valid."
  else
    oops "Config is INVALID — see the haproxy messages above."; return 1
  fi
}

# ---- mode: run locally ------------------------------------------------------
do_run() {
  detect_runtime
  [ -n "$RUNTIME" ] || { oops "Neither Docker nor Podman is available — cannot run HAProxy."; return 1; }
  local ports pflags="" host
  ports="$(bind_ports)"; host="$(sample_host)"; host="${host:-your-app.example.com}"
  for p in $ports; do pflags="$pflags -p ${p}:${p}"; done
  title "Running HAProxy locally  (${RUNTIME} · ${IMAGE})"
  warn "No HAPTIC controller here: backend server IPs are frozen at render time."
  warn "If a target pod restarts you will get 503s — that is expected for a static snapshot."
  note "Publishing port(s): ${ports:-<none found in binds>}"
  note "Reach it by sending the Host header your routes expect, for example:"
  for p in $ports; do note "    curl -H 'Host: ${host}' http://localhost:${p}/"; done
  note "Press Ctrl-C to stop the container."
  note "(the folder is mounted read-write so HAProxy can create its runtime"
  note " unix sockets there — a few *.sock files will appear and are harmless.)"
  say ""
  # TTY allocation: Git-Bash needs `winpty` for `docker run -it`; without it,
  # fall back to -i only (HAProxy still runs in the foreground; Ctrl-C stops it).
  local ttyflag="-it" pre=""
  if is_windows_bash; then
    if command -v winpty >/dev/null 2>&1; then pre="winpty"; else ttyflag="-i"; warn "No winpty found — running without a TTY (colours off, Ctrl-C still stops it)."; fi
  fi
  # -db forces the foreground (overrides a 'daemon' directive in global).
  # The mount is read-write: this config binds unix sockets under /etc/haproxy
  # (peers + the H1/H2C loopback frontends), which a read-only mount would reject.
  # shellcheck disable=SC2086
  $pre "$RUNTIME" run --rm $ttyflag --name haptic-tryout $pflags \
    -v "$(abs_workdir):/etc/haproxy" "$IMAGE" \
    haproxy -db -f /etc/haproxy/haproxy.cfg
}

# ---- mode: launch as a Kubernetes Pod ---------------------------------------
do_k8s() {
  command -v kubectl >/dev/null 2>&1 || { oops "kubectl not found on PATH."; return 1; }
  kubectl version >/dev/null 2>&1 || { oops "kubectl cannot reach a cluster — check your kubeconfig/context."; return 1; }
  local ns_shown; ns_shown="${NAMESPACE:-$(kubectl config view --minify -o jsonpath='{..namespace}' 2>/dev/null || true)}"; ns_shown="${ns_shown:-default}"

  # Pod labels: 'app=haptic-tryout' plus any HAPTIC_TRYOUT_LABELS=k=v,k2=v2 so the
  # Pod can match a NetworkPolicy you already have (e.g. one that allows your
  # HAProxy fleet's egress — see the NetworkPolicy note below). A user-supplied
  # key overrides the default (later value wins, each key emitted once). Built
  # with awk to avoid bash-4 associative arrays (macOS bash 3.2 has none).
  local pod_labels
  pod_labels="$(printf '%s' "app=haptic-tryout,${HAPTIC_TRYOUT_LABELS:-}" | awk -v RS=',' '
    NF {
      k = $0; sub(/=.*/, "", k);
      v = ($0 ~ /=/) ? substr($0, index($0, "=") + 1) : "";
      if (!(k in val)) order[++n] = k;
      val[k] = v;
    }
    END { sep = ""; for (i = 1; i <= n; i++) { printf "%s%s: \"%s\"", sep, order[i], val[order[i]]; sep = ", " } }')"
  title "Launching HAProxy as a Pod in your cluster"
  note "Context:   $(kubectl config current-context 2>/dev/null || echo '?')"
  note "Namespace: ${ns_shown}"
  warn "This runs the STATIC config in-cluster WITHOUT the HAPTIC controller."
  warn "Backend IPs are frozen; a restart of any target pod will produce 503s until"
  warn "you re-render. Install HAPTIC for a self-healing, live configuration."
  say ""

  # ConfigMap keys cannot contain '/', so use one ConfigMap per directory.
  kc delete configmap haptic-tryout-cfg haptic-tryout-maps haptic-tryout-general haptic-tryout-ssl --ignore-not-found >/dev/null 2>&1 || true
  kc create configmap haptic-tryout-cfg --from-file="$WORKDIR/haproxy.cfg" >/dev/null
  for d in maps general ssl; do
    if [ -d "$WORKDIR/$d" ] && [ -n "$(ls -A "$WORKDIR/$d" 2>/dev/null)" ]; then
      kc create configmap "haptic-tryout-$d" --from-file="$WORKDIR/$d/" >/dev/null
    else
      kc create configmap "haptic-tryout-$d" --from-literal=.keep="" >/dev/null
    fi
  done
  ok "Created ConfigMaps: haptic-tryout-{cfg,maps,general,ssl}"

  kc apply -f - >/dev/null <<POD
apiVersion: v1
kind: Pod
metadata:
  name: haptic-tryout
  labels: { ${pod_labels} }
spec:
  containers:
    - name: haproxy
      image: ${IMAGE}
      args: ["-db", "-f", "/etc/haproxy/haproxy.cfg"]
      volumeMounts:
        - { name: cfg,     mountPath: /etc/haproxy/haproxy.cfg, subPath: haproxy.cfg }
        - { name: maps,    mountPath: /etc/haproxy/maps }
        - { name: general, mountPath: /etc/haproxy/general }
        - { name: ssl,     mountPath: /etc/haproxy/ssl }
  volumes:
    - { name: cfg,     configMap: { name: haptic-tryout-cfg } }
    - { name: maps,    configMap: { name: haptic-tryout-maps } }
    - { name: general, configMap: { name: haptic-tryout-general } }
    - { name: ssl,     configMap: { name: haptic-tryout-ssl } }
POD
  ok "Applied Pod 'haptic-tryout'."
  note "Waiting for it to become Ready…"
  if ! kc wait --for=condition=Ready pod/haptic-tryout --timeout=60s >/dev/null 2>&1; then
    warn "Pod is not Ready yet. Inspect it with:"
    note "    kubectl $NS_NOTE describe pod haptic-tryout"
    note "    kubectl $NS_NOTE logs haptic-tryout"
  else
    ok "Pod 'haptic-tryout' is running."
  fi

  local ports p1 host
  ports="$(bind_ports)"; p1="$(echo "$ports" | head -1)"; p1="${p1:-8080}"
  host="$(sample_host)"; host="${host:-your-app.example.com}"

  title "If backends stay unreachable: NetworkPolicy"
  note "If this namespace has a default-deny EGRESS NetworkPolicy AND your cluster"
  note "runs a policy-enforcing CNI (Calico, Cilium, Antrea, kube-router/k3s, …),"
  note "this Pod may be blocked from reaching the backend app pods — you would then"
  note "get 503s that are a policy block, not (only) the frozen-IP caveat above."
  note "By default this Pod is labelled 'app=haptic-tryout', so a NetworkPolicy"
  note "written for HAPTIC's real HAProxy fleet does not select it."
  note "Port-forward (below) still reaches the Pod regardless of any ingress policy —"
  note "it tunnels via the API server into the Pod's loopback, not pod-to-pod (a"
  note "Kubernetes guarantee; only a node/host firewall or RBAC could stop it)."
  note "To let it reach backends, do ONE of:"
  note "  • run it in a namespace that has no default-deny egress, or"
  note "  • apply the ready-made allow-egress policy this script can print:"
  note "      ./$(basename "$0") emit-netpol | kubectl apply ${NAMESPACE:+-n $NAMESPACE }-f -"
  note "  • re-run with labels that match a policy you already allow, e.g.:"
  note "      HAPTIC_TRYOUT_LABELS=app=haproxy,role=proxy ./$(basename "$0") k8s"

  title "Reach it with port-forwarding"
  note "There is no Service or Ingress in front of this Pod, so forward a bound port"
  note "from the Pod straight to your machine (run in a second terminal):"
  for p in $ports; do note "    kubectl $NS_NOTE port-forward pod/haptic-tryout ${p}:${p}"; done
  note "then send the Host header your routes expect, for example:"
  note "    curl -H 'Host: ${host}' http://localhost:${p1}/"
  title "Clean up when you are done"
  note "    kubectl $NS_NOTE delete pod haptic-tryout"
  note "    kubectl $NS_NOTE delete configmap haptic-tryout-cfg haptic-tryout-maps haptic-tryout-general haptic-tryout-ssl"
}

# ---- mode: print an allow-egress NetworkPolicy (prints only, never applies) --
do_emit_netpol() {
  cat <<EOF
# Allows the try-out Pod (app=haptic-tryout) to make outbound connections even
# under a default-deny egress NetworkPolicy. Review it, then apply:
#   ./$(basename "$0") emit-netpol | kubectl apply ${NAMESPACE:+-n $NAMESPACE }-f -
# Remove it when you are done:
#   kubectl ${NAMESPACE:+-n $NAMESPACE }delete networkpolicy haptic-tryout-allow-egress
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: haptic-tryout-allow-egress
spec:
  podSelector:
    matchLabels:
      app: haptic-tryout
  policyTypes:
    - Egress
  egress:
    - {}
EOF
}

# ---- menu -------------------------------------------------------------------
intro() {
  title "HAPTIC playground — try your rendered config"
  note "This carries your rendered HAProxy config and its map/error/cert files."
  warn "It is a STATIC snapshot: without the HAPTIC controller the backend server"
  warn "IPs are frozen, so a restart of any target app's pod leads to 503s."
}
menu() {
  intro
  title "What would you like to do?"
  say "  1) Validate the config          (haproxy -c, needs Docker/Podman)"
  say "  2) Run HAProxy locally          (Docker/Podman)"
  say "  3) Launch it in Kubernetes      (kubectl)"
  say "  4) Just keep the files          (already written to ${WORKDIR})"
  say "  5) Quit"
  printf 'Choose [1-5]: '
  local choice; read -r choice || choice=5
  case "$choice" in
    1) do_check ;;
    2) do_run ;;
    3) do_k8s ;;
    4) ok "Files are in ${WORKDIR}/." ;;
    *) say "Nothing to do. Bye." ;;
  esac
}

main() {
  # These modes print to stdout only (pipeable) — no file writing, no banner.
  case "${1:-menu}" in
    emit-netpol|--emit-netpol) do_emit_netpol; return ;;
    -h|--help)                 sed -n '3,40p' "$0" | sed -E 's/^#? ?//'; return ;;
  esac
  write_files
  case "${1:-menu}" in
    check|-c|--check) do_check ;;
    run|-r|--run)     do_run ;;
    k8s|-k|--k8s)     do_k8s ;;
    files|--files)    ok "Files written to ${WORKDIR}/. Nothing else to do." ;;
    menu|"")          menu ;;
    *)                oops "Unknown command: $1"; say "Try: check | run | k8s | emit-netpol | files | (no arg for menu)"; exit 1 ;;
  esac
}
main "$@"
