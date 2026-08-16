// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

func execInHAProxyPod(ctx context.Context, pod, container string, argv ...string) (string, error) {
	args := []string{
		"--kubeconfig", kubeconfigPath,
		"-n", ControllerNamespace,
		"exec", pod, "-c", container, "--",
	}
	args = append(args, argv...)
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("kubectl exec %s -c %s %v: %w (stderr: %s)",
			pod, container, argv, err, stderr.String())
	}
	return stdout.String(), nil
}

func podJSONPath(ctx context.Context, pod, expr string) (string, error) {
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"-n", ControllerNamespace,
		"get", "pod", pod, "-o", "jsonpath="+expr,
	)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func apiProxyGet(ctx context.Context, pod string, port int, path string) (string, error) {
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"get", "--raw",
		fmt.Sprintf("/api/v1/namespaces/%s/pods/%s:%d/proxy/%s", ControllerNamespace, pod, port, path),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func selectedHAProxyStatus(ctx context.Context, pod, host, path string) (string, error) {
	out, err := execInHAProxyPod(ctx, pod, "haproxy", "curl",
		"-sS", "--connect-timeout", "1", "--max-time", "2",
		"-o", "/dev/null", "-w", "%{http_code}",
		"-H", "Host: "+host, "http://127.0.0.1"+path)
	return strings.TrimSpace(out), err
}

func availabilityMonitorToken() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("generate availability monitor token: %w", err)
	}
	return hex.EncodeToString(token[:]), nil
}

func stopRemoteHAProxyAvailabilityMonitor(pod, stateDir, token string, waitForState bool) error {
	waitForStateArg := "0"
	if waitForState {
		waitForStateArg = "1"
	}

	cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := execInHAProxyPod(cleanupCtx, pod, "haproxy", "/bin/sh", "-c", `
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

_signal_process "$_state/child" TERM
_signal_process "$_state/pid" TERM

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
`, "haproxy-availability-monitor-cleanup", stateDir, token, waitForStateArg)
	if err != nil {
		return fmt.Errorf("stop remote selected-pod availability monitor: %w", err)
	}
	return nil
}

func startSelectedHAProxyAvailabilityMonitor(
	ctx context.Context,
	pod string,
	host string,
	path string,
) (func() error, error) {
	token, err := availabilityMonitorToken()
	if err != nil {
		return nil, err
	}
	stateDir := "/tmp/haptic-availability-monitor-" + token
	monitorCtx, cancel := context.WithCancel(context.Background())
	args := []string{
		"--kubeconfig", kubeconfigPath,
		"-n", ControllerNamespace,
		"exec", pod, "-c", "haproxy", "--", "/bin/sh", "-c", `
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
  _start=$(_process_start "$_proc") || return 1
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
  rm -f "$_state/child" "$_state/pid" "$_state/status" "$_state"/*.tmp.*
  rm -f "$_state/token"
  rmdir "$_state" 2>/dev/null || true
  exit "$_rc"
}

trap '_shutdown; exit 0' HUP INT TERM
trap _finish EXIT
_write_process $$ pid || exit 1

_ready=0
while :; do
  curl -sS --connect-timeout 1 --max-time 2 -o /dev/null -w '%{http_code}' -H "Host: $1" "http://127.0.0.1$2" > "$_state/status" 2>/dev/null &
  _child=$!
  _write_process "$_child" child || exit 1
  wait "$_child" 2>/dev/null || true
  rm -f "$_state/child"
  _status=$(cat "$_state/status" 2>/dev/null || true)
  rm -f "$_state/status"
  if [ "$_status" != 200 ]; then
    printf 'HAPTIC_AVAILABILITY_FAILED status=%s\n' "$_status" >&2
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
`, "haproxy-availability-monitor", host, path, stateDir, token,
	}
	cmd := exec.CommandContext(monitorCtx, "kubectl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create selected-pod availability output pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start selected-pod availability monitor: %w", err)
	}

	done := make(chan struct{})
	var lifecycleMu sync.Mutex
	commandExited := false
	terminationRequested := false
	unexpectedExit := false
	var commandErr error
	go func() {
		err := cmd.Wait()
		lifecycleMu.Lock()
		commandErr = err
		commandExited = true
		unexpectedExit = !terminationRequested
		lifecycleMu.Unlock()
		close(done)
	}()

	var terminateOnce sync.Once
	terminated := make(chan struct{})
	var terminateErr error
	handshakeReceived := false
	terminate := func() error {
		terminateOnce.Do(func() {
			lifecycleMu.Lock()
			terminationRequested = true
			waitForState := !handshakeReceived && !commandExited
			lifecycleMu.Unlock()

			terminateErr = stopRemoteHAProxyAvailabilityMonitor(pod, stateDir, token, waitForState)
			cancel()
			<-done
			if terminateErr != nil {
				retryErr := stopRemoteHAProxyAvailabilityMonitor(pod, stateDir, token, false)
				if retryErr == nil {
					terminateErr = nil
				} else {
					terminateErr = errors.Join(terminateErr, retryErr)
				}
			}
			close(terminated)
		})
		<-terminated
		return terminateErr
	}

	go func() {
		select {
		case <-ctx.Done():
			_ = terminate()
		case <-done:
			_ = terminate()
		case <-terminated:
		}
	}()

	ready := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if !scanner.Scan() {
			ready <- fmt.Errorf("monitor produced no startup handshake: %v", scanner.Err())
			return
		}
		if scanner.Text() != "READY" {
			ready <- fmt.Errorf("unexpected monitor startup handshake %q", scanner.Text())
			return
		}
		ready <- nil
		for scanner.Scan() {
		}
	}()

	select {
	case err := <-ready:
		if err == nil {
			lifecycleMu.Lock()
			handshakeReceived = true
			lifecycleMu.Unlock()
			if ctxErr := ctx.Err(); ctxErr != nil {
				cleanupErr := terminate()
				return nil, errors.Join(ctxErr, cleanupErr)
			}
			break
		}
		cleanupErr := terminate()
		lifecycleMu.Lock()
		monitorErr := commandErr
		lifecycleMu.Unlock()
		stderrText := strings.TrimSpace(stderr.String())
		if strings.Contains(stderrText, "HAPTIC_AVAILABILITY_FAILED") {
			return nil, errors.Join(err, fmt.Errorf("selected-pod HAProxy request failed before startup: %v (stderr: %s)",
				monitorErr, stderrText), cleanupErr)
		}
		return nil, errors.Join(err, cleanupErr)
	case <-done:
		cleanupErr := terminate()
		lifecycleMu.Lock()
		err := commandErr
		lifecycleMu.Unlock()
		stderrText := strings.TrimSpace(stderr.String())
		if strings.Contains(stderrText, "HAPTIC_AVAILABILITY_FAILED") {
			return nil, errors.Join(fmt.Errorf("selected-pod HAProxy request failed before startup: %v (stderr: %s)",
				err, stderrText), cleanupErr)
		}
		return nil, fmt.Errorf("selected-pod availability monitor exited before startup: %v (stderr: %s)",
			errors.Join(err, cleanupErr), stderrText)
	case <-ctx.Done():
		cleanupErr := terminate()
		return nil, errors.Join(ctx.Err(), cleanupErr)
	}

	var stopOnce sync.Once
	var stopErr error
	return func() error {
		stopOnce.Do(func() {
			cleanupErr := terminate()
			lifecycleMu.Lock()
			err := commandErr
			exitedEarly := unexpectedExit
			lifecycleMu.Unlock()
			stderrText := strings.TrimSpace(stderr.String())
			if strings.Contains(stderrText, "HAPTIC_AVAILABILITY_FAILED") {
				stopErr = errors.Join(fmt.Errorf("selected-pod HAProxy request failed: %w (stderr: %s)",
					err, stderrText), cleanupErr)
				return
			}
			if exitedEarly {
				stopErr = errors.Join(fmt.Errorf("selected-pod availability monitor exited early: %v (stderr: %s)",
					err, stderrText), cleanupErr)
				return
			}
			stopErr = cleanupErr
		})
		return stopErr
	}, nil
}
