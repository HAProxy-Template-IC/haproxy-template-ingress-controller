// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package availability holds the shell scripts the e2e suite runs inside an
// HAProxy pod to prove a route keeps answering while a test injects a fault.
package availability

// FailureMarker prefixes the line MonitorScript writes to stderr when a probe
// gets anything but 200. Every other non-zero exit is a monitor fault.
const FailureMarker = "HAPTIC_AVAILABILITY_FAILED"

// MonitorScript probes http://127.0.0.1$2 with Host $1 until it is stopped,
// keeping its state in directory $3 under token $4. It prints READY after the
// first 200 and exits 1 with FailureMarker on any other status.
const MonitorScript = `
_state=$3
_token=$4
umask 077
mkdir "$_state" || exit 1
printf '%s\n' "$_token" > "$_state/token" || exit 1

_process_start() {
  _proc=$1
  [ -r "/proc/$_proc/stat" ] || return 1
  set -- $(cat "/proc/$_proc/stat") || return 1
  [ "$#" -ge 22 ] || return 1
  shift 21
  printf '%s\n' "$1"
}

_write_process() {
  _proc=$1
  _name=$2
  if ! _start=$(_process_start "$_proc"); then
    # The shell may reap a fast child before it is recorded; nothing is left to stop then.
    [ -e "/proc/$_proc" ] && return 1
    return 0
  fi
  _tmp="$_state/$_name.tmp.$$"
  printf '%s %s\n' "$_proc" "$_start" > "$_tmp" || return 1
  mv "$_tmp" "$_state/$_name"
}

_matching_pid() {
  _file=$1
  [ -r "$_file" ] || return 1
  read -r _pid _start < "$_file" || return 1
  _actual_start=$(_process_start "$_pid") || return 1
  [ "$_actual_start" = "$_start" ] || return 1
  printf '%s\n' "$_pid"
}

_shutdown() {
  _child=$(_matching_pid "$_state/child") || return 0
  kill -TERM "$_child" 2>/dev/null || true
  wait "$_child" 2>/dev/null || true
}

_finish() {
  _rc=$?
  trap - EXIT HUP INT TERM
  _shutdown
  rm -f "$_state/child" "$_state/pid" "$_state/status" "$_state/curl-error" "$_state"/*.tmp.*
  rm -f "$_state/token" "$_state/stopping"
  rmdir "$_state" 2>/dev/null || true
  exit "$_rc"
}

trap '_shutdown; exit 0' HUP INT TERM
trap _finish EXIT
_write_process $$ pid || exit 1

_ready=0
while :; do
  curl -sS --connect-timeout 1 --max-time 2 -o /dev/null -w '%{http_code}' -H "Host: $1" "http://127.0.0.1$2" > "$_state/status" 2>"$_state/curl-error" &
  _child=$!
  _write_process "$_child" child || exit 1
  wait "$_child" 2>/dev/null
  _wait=$?
  rm -f "$_state/child"
  _status=$(cat "$_state/status" 2>/dev/null || true)
  _curl_error=$(cat "$_state/curl-error" 2>/dev/null || true)
  rm -f "$_state/status" "$_state/curl-error"
  if [ "$_status" != 200 ]; then
    if [ -e "$_state/stopping" ]; then
      exit 0
    fi
    # The probe's exit status tells a refused or timed-out request (7, 28)
    # from a probe something killed (128 + the signal).
    printf 'HAPTIC_AVAILABILITY_FAILED status=%s curl_exit=%s curl_error=%s\n' "$_status" "$_wait" "$_curl_error" >&2
    exit 1
  fi
  if [ "$_ready" -eq 0 ]; then
    printf 'READY\n'
    _ready=1
  fi
  sleep 0.1 &
  _child=$!
  _write_process "$_child" child || exit 1
  wait "$_child" 2>/dev/null || true
  rm -f "$_state/child"
done
`

// StopScript stops the MonitorScript whose state directory is $1 and token
// is $2, then removes that directory. With $3 set to 1 it first waits for the
// monitor to create its state.
const StopScript = `
_state=$1
_token=$2
_wait_for_state=$3

_process_start() {
  _proc=$1
  [ -r "/proc/$_proc/stat" ] || return 1
  set -- $(cat "/proc/$_proc/stat") || return 1
  [ "$#" -ge 22 ] || return 1
  shift 21
  printf '%s\n' "$1"
}

_matching_pid() {
  _file=$1
  [ -r "$_file" ] || return 1
  read -r _pid _start < "$_file" || return 1
  case "$_pid" in ''|*[!0-9]*) return 1 ;; esac
  case "$_start" in ''|*[!0-9]*) return 1 ;; esac
  _actual_start=$(_process_start "$_pid") || return 1
  [ "$_actual_start" = "$_start" ] || return 1
  printf '%s\n' "$_pid"
}

_signal_process() {
  _file=$1
  _signal=$2
  _pid=$(_matching_pid "$_file") || return 0
  kill -"$_signal" "$_pid" 2>/dev/null || true
}

_any_process_alive() {
  _matching_pid "$_state/pid" >/dev/null 2>&1 && return 0
  _matching_pid "$_state/child" >/dev/null 2>&1
}

if [ "$_wait_for_state" = 1 ]; then
  _waited=0
  while [ ! -r "$_state/token" ] && [ "$_waited" -lt 30 ]; do
    sleep 0.1
    _waited=$((_waited + 1))
  done
  if [ ! -r "$_state/token" ]; then
    printf 'availability monitor state did not appear\n' >&2
    exit 1
  fi
fi

[ -d "$_state" ] || exit 0
_actual_token=$(cat "$_state/token" 2>/dev/null || true)
if [ "$_actual_token" != "$_token" ]; then
  [ ! -e "$_state/pid" ] && exit 0
  printf 'availability monitor state token mismatch\n' >&2
  exit 1
fi

# The monitor goes first, and is told why: a probe the stop itself cuts
# short must not be reported as HAProxy being unavailable.
: > "$_state/stopping"
_signal_process "$_state/pid" TERM
_signal_process "$_state/child" TERM

_waited=0
while _any_process_alive && [ "$_waited" -lt 30 ]; do
  sleep 0.1
  _waited=$((_waited + 1))
done

_signal_process "$_state/child" KILL
_signal_process "$_state/pid" KILL

_waited=0
while _any_process_alive && [ "$_waited" -lt 10 ]; do
  sleep 0.1
  _waited=$((_waited + 1))
done
if _any_process_alive; then
  printf 'availability monitor processes survived SIGKILL\n' >&2
  exit 1
fi

rm -rf "$_state"
`
