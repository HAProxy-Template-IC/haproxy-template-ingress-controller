//go:build integration

// Copyright 2025 Philipp Hossner
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

package integration

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// Exec runs one command in a container of the HAProxy pod and returns its
// combined output. Every assertion about the pod reads through here: the agent
// exposes no file or runtime endpoint, so the pod's own tree and HAProxy's own
// `show` output are the ground truth.
func (h *HAProxyInstance) Exec(ctx context.Context, container, stdin string, command ...string) (string, error) {
	config, err := h.namespace.cluster.getRestConfig()
	if err != nil {
		return "", fmt.Errorf("rest config: %w", err)
	}

	request := h.namespace.clientset.CoreV1().RESTClient().Post().
		Resource("pods").Name(h.Name).Namespace(h.Namespace).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdin:     stdin != "",
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(config, "POST", request.URL())
	if err != nil {
		return "", fmt.Errorf("exec executor: %w", err)
	}

	// Separate buffers: the two streams are copied by their own goroutines,
	// so one buffer for both is a data race.
	var stdout, stderr bytes.Buffer
	streams := remotecommand.StreamOptions{Stdout: &stdout, Stderr: &stderr}
	if stdin != "" {
		streams.Stdin = strings.NewReader(stdin)
	}
	if err := executor.StreamWithContext(ctx, streams); err != nil {
		return stdout.String() + stderr.String(),
			fmt.Errorf("exec %s in %s: %w (%s)", strings.Join(command, " "), container, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// Worker runs one command on HAProxy's worker stats socket, the socket the
// agent runs every runtime command on.
func (h *HAProxyInstance) Worker(ctx context.Context, command string) (string, error) {
	return h.socket(ctx, WorkerSocketPath, command)
}

func (h *HAProxyInstance) socket(ctx context.Context, socket, command string) (string, error) {
	return h.Exec(ctx, HAProxyContainer, command+"\n",
		"socat", "-t5", "stdio", "unix-connect:"+socket)
}

// ReadFile returns a file of the pod's tree, path relative to the base dir.
func (h *HAProxyInstance) ReadFile(ctx context.Context, path string) (string, error) {
	return h.Exec(ctx, HAProxyContainer, "", "cat", BaseDir+"/"+path)
}

// FileExists reports whether a manifest path is present in the pod's tree.
func (h *HAProxyInstance) FileExists(ctx context.Context, path string) bool {
	_, err := h.Exec(ctx, HAProxyContainer, "", "test", "-e", BaseDir+"/"+path)
	return err == nil
}

// ListDir names the entries of one directory of the pod's tree.
func (h *HAProxyInstance) ListDir(ctx context.Context, dir string) ([]string, error) {
	out, err := h.Exec(ctx, HAProxyContainer, "", "ls", "-A", BaseDir+"/"+dir)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// RuntimeMapEntries parses `show map <path>` into key → value. HAProxy names a
// map by the string the configuration references it with, which is the
// manifest path, so no translation happens anywhere.
func (h *HAProxyInstance) RuntimeMapEntries(ctx context.Context, path string) (map[string]string, error) {
	out, err := h.Worker(ctx, "show map "+path)
	if err != nil {
		return nil, err
	}
	if strings.Contains(out, "Unknown map identifier") {
		return nil, fmt.Errorf("HAProxy has no runtime map %q: %s", path, strings.TrimSpace(out))
	}
	entries := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !strings.HasPrefix(line, "0x") {
			continue
		}
		// id key value — the value keeps every byte after the single space.
		fields := strings.SplitN(line, " ", 3)
		switch len(fields) {
		case 2:
			entries[fields[1]] = ""
		case 3:
			entries[fields[1]] = fields[2]
		}
	}
	return entries, nil
}

// WorkerPID is the identity a reload changes; a runtime apply leaves it alone.
func (h *HAProxyInstance) WorkerPID(ctx context.Context) (int, error) {
	out, err := h.Worker(ctx, "show info")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(out, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "Pid: "); ok {
			var pid int
			if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &pid); err != nil {
				return 0, fmt.Errorf("show info reported Pid %q: %w", value, err)
			}
			return pid, nil
		}
	}
	return 0, fmt.Errorf("show info carried no Pid:\n%s", out)
}
