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

// Package kubeexec scopes test kubectl commands to an explicit cluster.
package kubeexec

import (
	"context"
	"errors"
	"io"
	"path/filepath"

	"gitlab.com/haproxy-haptic/haptic/tests/process"
)

type Client struct {
	Runner     process.Runner
	Kubeconfig string
	Context    string
	Namespace  string
}

func (c Client) Command(input io.Reader, args ...string) (*process.Command, error) {
	if c.Runner == nil || !filepath.IsAbs(c.Kubeconfig) || c.Context == "" {
		return nil, errors.New("kubectl needs a runner, an absolute kubeconfig path, and an explicit context")
	}
	flags := []string{"--kubeconfig", c.Kubeconfig, "--context", c.Context}
	if c.Namespace != "" {
		flags = append(flags, "--namespace", c.Namespace)
	}
	return &process.Command{Name: "kubectl", Args: append(flags, args...), Stdin: input}, nil
}

func (c Client) Run(ctx context.Context, input io.Reader, args ...string) (process.Result, error) {
	command, err := c.Command(input, args...)
	if err != nil {
		return process.Result{ExitCode: -1}, err
	}
	return c.Runner.Run(ctx, command)
}

func (c Client) Start(ctx context.Context, input io.Reader, args ...string) (process.Running, error) {
	command, err := c.Command(input, args...)
	if err != nil {
		return nil, err
	}
	return c.Runner.Start(ctx, command)
}
