admission_connection_pending() {
  local line pending=false
  while IFS= read -r line; do
    case "$line" in
      "Error from server (InternalError): "*"Internal error occurred: failed calling webhook "*"failed to call webhook: "*"connect: connection refused") pending=true ;;
      "Error from server (InternalError): "*"Internal error occurred: failed calling webhook "*"no endpoints available for service "*) pending=true ;;
      ""|Warning:*) ;;
      *) return 1 ;;
    esac
  done <<< "$1"
  [ "$pending" = true ]
}

wait_admission_ready() {
  local manifest="$1" deadline=$((SECONDS + ${2:-180})) output request_timeout
  while [ "$SECONDS" -lt "$deadline" ]; do
    request_timeout=$((deadline - SECONDS))
    [ "$request_timeout" -le 10 ] || request_timeout=10
    # Create forces admission; applying unchanged fixtures can send no write.
    if output=$(k create --dry-run=server --request-timeout="${request_timeout}s" -f "$manifest" 2>&1 >/dev/null); then
      [ -z "$output" ] || printf '%s\n' "$output"
      return 0
    fi
    printf '%s\n' "$output" >&2
    admission_connection_pending "$output" || return 1
    sleep 1
  done
  printf 'Admission webhook did not become reachable before the readiness deadline.\n' >&2
  return 1
}
