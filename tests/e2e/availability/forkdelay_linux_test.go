//go:build e2e

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

package availability

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// forkDelayedShell is /bin/sh under ptrace, held for a fixed delay every time
// one of its forks returns: the pause a starved scheduler can put between a
// background fork and the shell's next command.
type forkDelayedShell struct {
	pid                   int
	childExitedDuringHold atomic.Int64
	exited                chan struct{}
	exitCode              int
	traceErr              error
}

func startForkDelayedShell(delay time.Duration, files []*os.File, args ...string) (*forkDelayedShell, error) {
	shell := &forkDelayedShell{exited: make(chan struct{})}
	started := make(chan error, 1)
	go func() {
		// ptrace requests must come from the thread that started the tracee;
		// the thread exits with this goroutine because it is never unlocked.
		runtime.LockOSThread()
		defer close(shell.exited)
		proc, err := os.StartProcess("/bin/sh", append([]string{"/bin/sh"}, args...), &os.ProcAttr{
			Files: files,
			Sys:   &syscall.SysProcAttr{Ptrace: true},
		})
		if err != nil {
			started <- err
			return
		}
		shell.pid = proc.Pid
		started <- nil
		shell.exitCode, shell.traceErr = shell.trace(delay)
		_ = proc.Release()
	}()
	if err := <-started; err != nil {
		return nil, err
	}
	return shell, nil
}

func (s *forkDelayedShell) trace(delay time.Duration) (int, error) {
	var status unix.WaitStatus
	if _, err := unix.Wait4(s.pid, &status, 0, nil); err != nil {
		return -1, fmt.Errorf("wait for the traced shell to exec: %w", err)
	}
	// vfork is left alone: dash uses it only for foreground commands, which it waits for.
	if err := unix.PtraceSetOptions(s.pid, unix.PTRACE_O_TRACEFORK|unix.PTRACE_O_EXITKILL); err != nil {
		return -1, fmt.Errorf("set ptrace options: %w", err)
	}
	signal := 0
	for {
		if err := unix.PtraceCont(s.pid, signal); err != nil {
			return -1, fmt.Errorf("resume the traced shell: %w", err)
		}
		signal = 0
		if _, err := unix.Wait4(s.pid, &status, 0, nil); err != nil {
			return -1, fmt.Errorf("wait for the traced shell: %w", err)
		}
		switch {
		case status.Exited():
			return status.ExitStatus(), nil
		case status.Signaled():
			return 128 + int(status.Signal()), nil
		case status.TrapCause() == unix.PTRACE_EVENT_FORK:
			if err := s.holdAfterFork(delay); err != nil {
				return -1, err
			}
		case status.Stopped():
			signal = int(status.StopSignal())
		}
	}
}

// holdAfterFork lets the new child run untraced while the shell stays stopped
// just before its fork returns.
func (s *forkDelayedShell) holdAfterFork(delay time.Duration) error {
	child, err := unix.PtraceGetEventMsg(s.pid)
	if err != nil {
		return fmt.Errorf("read the forked child's pid: %w", err)
	}
	var status unix.WaitStatus
	if _, err := unix.Wait4(int(child), &status, unix.WALL, nil); err != nil {
		return fmt.Errorf("wait for forked child %d to stop: %w", child, err)
	}
	if err := unix.PtraceDetach(int(child)); err != nil {
		return fmt.Errorf("detach forked child %d: %w", child, err)
	}
	time.Sleep(delay)
	if processIsZombie(int(child)) {
		s.childExitedDuringHold.Add(1)
	}
	return nil
}

func processIsZombie(pid int) bool {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// The state follows the parenthesised command name, which may itself
	// contain spaces or parentheses.
	end := bytes.LastIndexByte(stat, ')')
	return end >= 0 && bytes.HasPrefix(stat[end+1:], []byte(" Z"))
}

func (s *forkDelayedShell) kill() {
	select {
	case <-s.exited:
	default:
		_ = unix.Kill(s.pid, unix.SIGKILL)
		<-s.exited
	}
}
