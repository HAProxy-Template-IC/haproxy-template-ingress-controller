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

package podclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	streamhttp "k8s.io/streaming/pkg/httpstream"
)

// Exec returns stdout without exposing stderr. Zero maxBytes is unlimited.
func (c *Client) Exec(ctx context.Context, pod, container string, command []string, maxBytes int64) ([]byte, error) {
	if maxBytes < 0 {
		return nil, errors.New("response limit must not be negative")
	}
	ctx, cancel := context.WithTimeout(ctx, loopbackRequestTimeout)
	defer cancel()
	requestURL := c.clientset.CoreV1().RESTClient().Post().Resource("pods").
		Namespace(c.namespace).Name(pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container, Command: command, Stdout: true, Stderr: true,
		}, scheme.ParameterCodec).URL()
	primary, err := remotecommand.NewWebSocketExecutor(c.config, http.MethodGet, requestURL.String())
	if err != nil {
		return nil, fmt.Errorf("create WebSocket executor: %w", err)
	}
	secondary, err := remotecommand.NewSPDYExecutor(c.config, http.MethodPost, requestURL)
	if err != nil {
		return nil, fmt.Errorf("create SPDY executor: %w", err)
	}
	executor, err := remotecommand.NewFallbackExecutor(primary, secondary, func(err error) bool {
		return streamhttp.IsUpgradeFailure(err) || streamhttp.IsHTTPSProxyError(err)
	})
	if err != nil {
		return nil, fmt.Errorf("create pod executor: %w", err)
	}
	output := responseBuffer{limit: maxBytes}
	if err := executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &output, Stderr: io.Discard}); err != nil {
		return nil, fmt.Errorf("read pod command output: %w", err)
	}
	return output.buffer.Bytes(), nil
}

type responseBuffer struct {
	buffer bytes.Buffer
	limit  int64
}

func (b *responseBuffer) Write(data []byte) (int, error) {
	if b.limit > 0 && int64(len(data)) > b.limit-int64(b.buffer.Len()) {
		return 0, errors.New("pod response exceeds the requested limit")
	}
	return b.buffer.Write(data)
}
