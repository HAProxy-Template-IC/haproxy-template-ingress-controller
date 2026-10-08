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

// Package process owns external processes used by test infrastructure.
package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"sync"
	"time"
)

type Command struct {
	Name   string
	Args   []string
	Dir    string
	Env    map[string]string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type Result struct {
	Stdout   string
	Stderr   string
	Combined string
	ExitCode int
}

type Running interface {
	Done() <-chan struct{}
	Output() Result
	Wait() (Result, error)
	Stop() error
}

type Runner interface {
	Run(context.Context, *Command) (Result, error)
	Start(context.Context, *Command) (Running, error)
}

type Executor struct{}

func (e Executor) Run(ctx context.Context, command *Command) (Result, error) {
	running, err := e.Start(ctx, command)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	return running.Wait()
}

func (Executor) Start(ctx context.Context, command *Command) (Running, error) {
	if command == nil || command.Name == "" {
		return nil, errors.New("process executable is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	processCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(processCtx, command.Name, command.Args...)
	cmd.Dir, cmd.Stdin = command.Dir, command.Stdin
	cmd.Env = append(cmd.Environ(), environment(command.Env)...)
	cmd.WaitDelay = time.Second
	if err := configureTree(cmd); err != nil {
		cancel()
		return nil, err
	}
	running := &child{cancel: cancel, done: make(chan struct{}), exitCode: -1}
	cmd.Stdout = capture(io.MultiWriter(&running.stdout, &running.combined), command.Stdout)
	cmd.Stderr = capture(io.MultiWriter(&running.stderr, &running.combined), command.Stderr)
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start %s: %w", command.Name, err)
	}
	go running.reap(processCtx, cmd)
	return running, nil
}

type child struct {
	cancel   context.CancelFunc
	done     chan struct{}
	stdout   outputBuffer
	stderr   outputBuffer
	combined outputBuffer
	mu       sync.Mutex
	exitCode int
	err      error
	stopErr  error
}

func (c *child) reap(ctx context.Context, cmd *exec.Cmd) {
	err := cmd.Wait()
	cleanupErr := stopTree(cmd)
	c.mu.Lock()
	c.exitCode = cmd.ProcessState.ExitCode()
	c.err = errors.Join(err, ctx.Err(), cleanupErr)
	c.stopErr = cleanupErr
	c.mu.Unlock()
	c.cancel()
	close(c.done)
}

func (c *child) Done() <-chan struct{} { return c.done }

func (c *child) Output() Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Result{Stdout: c.stdout.String(), Stderr: c.stderr.String(), Combined: c.combined.String(), ExitCode: c.exitCode}
}

func (c *child) Wait() (Result, error) {
	<-c.done
	return c.Output(), c.err
}

func (c *child) Stop() error {
	c.cancel()
	<-c.done
	return c.stopErr
}

type outputBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *outputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *outputBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func capture(buffer, stream io.Writer) io.Writer {
	if stream == nil {
		return buffer
	}
	return io.MultiWriter(buffer, stream)
}

func environment(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}
