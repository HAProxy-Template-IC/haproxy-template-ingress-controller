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

package testutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	streamhttp "k8s.io/streaming/pkg/httpstream"
)

type podLoopbackGetter func(context.Context, string, string) ([]byte, error)

const loopbackRequestTimeout = 15 * time.Second

// LoopbackPodClient reaches a pod-local HTTP endpoint through Kubernetes port-forwarding.
type LoopbackPodClient struct {
	clientset     kubernetes.Interface
	namespace     string
	labelSelector string
	remotePort    int
	nextPod       atomic.Uint64
	getFromPod    podLoopbackGetter
}

// NewLoopbackPodClient creates a client for ready pods matching a label selector.
func NewLoopbackPodClient(
	config *rest.Config,
	clientset kubernetes.Interface,
	namespace string,
	labelSelector string,
	remotePort int,
) *LoopbackPodClient {
	configCopy := rest.CopyConfig(config)
	configCopy.Timeout = loopbackRequestTimeout
	client := &LoopbackPodClient{
		clientset:     clientset,
		namespace:     namespace,
		labelSelector: labelSelector,
		remotePort:    remotePort,
	}
	client.getFromPod = client.portForwardGet(configCopy)
	return client
}

// Get sends a GET through a fresh loopback tunnel and rotates across ready pods.
func (c *LoopbackPodClient) Get(ctx context.Context, path string) ([]byte, error) {
	pods, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: c.labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("list pods for loopback request: %w", err)
	}

	names := readyPodNames(pods.Items)
	if len(names) == 0 {
		return nil, fmt.Errorf("no ready pods match %q in namespace %q", c.labelSelector, c.namespace)
	}

	nameCount := uint64(len(names))
	start := (c.nextPod.Add(1) - 1) % nameCount
	requestErrors := make([]error, 0, len(names))
	for offset := uint64(0); offset < nameCount; offset++ {
		name := names[(start+offset)%nameCount]
		requestCtx, cancel := context.WithTimeout(ctx, loopbackRequestTimeout)
		body, err := c.getFromPod(requestCtx, name, path)
		cancel()
		if err == nil {
			return body, nil
		}
		requestErrors = append(requestErrors, fmt.Errorf("pod %s: %w", name, err))
	}

	return nil, fmt.Errorf("loopback request failed: %w", errors.Join(requestErrors...))
}

func readyPodNames(pods []corev1.Pod) []string {
	names := make([]string, 0, len(pods))
	for i := range pods {
		pod := &pods[i]
		if pod.Status.Phase != corev1.PodRunning || pod.DeletionTimestamp != nil || !podReady(pod) {
			continue
		}
		names = append(names, pod.Name)
	}
	sort.Strings(names)
	return names
}

func podReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func (c *LoopbackPodClient) portForwardGet(config *rest.Config) podLoopbackGetter {
	return func(ctx context.Context, podName, path string) ([]byte, error) {
		forwarder, stop, ready, err := c.newPortForwarder(config, podName)
		if err != nil {
			return nil, err
		}
		defer close(stop)

		localPort, err := awaitForwardedPort(ctx, forwarder, ready)
		if err != nil {
			return nil, err
		}

		return getForwardedLoopback(ctx, localPort, path)
	}
}

func (c *LoopbackPodClient) newPortForwarder(
	config *rest.Config,
	podName string,
) (forwarder *portforward.PortForwarder, stop, ready chan struct{}, err error) {
	requestURL := c.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(c.namespace).
		Name(podName).
		SubResource("portforward").
		URL()

	transport, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create SPDY transport: %w", err)
	}
	spdyDialer := spdy.NewDialer(
		upgrader,
		&http.Client{Transport: transport, Timeout: loopbackRequestTimeout},
		http.MethodPost,
		requestURL,
	)
	websocketDialer, err := portforward.NewSPDYOverWebsocketDialer(requestURL, config)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create WebSocket dialer: %w", err)
	}
	dialer := portforward.NewFallbackDialer(websocketDialer, spdyDialer, func(err error) bool {
		return streamhttp.IsUpgradeFailure(err) || streamhttp.IsHTTPSProxyError(err)
	})

	stop = make(chan struct{})
	ready = make(chan struct{})
	forwarder, err = portforward.NewOnAddresses(
		dialer,
		[]string{"127.0.0.1"},
		[]string{"0:" + strconv.Itoa(c.remotePort)},
		stop,
		ready,
		io.Discard,
		io.Discard,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create port forwarder: %w", err)
	}
	return forwarder, stop, ready, nil
}

func awaitForwardedPort(
	ctx context.Context,
	forwarder *portforward.PortForwarder,
	ready <-chan struct{},
) (uint16, error) {
	forwardErr := make(chan error, 1)
	go func() {
		forwardErr <- forwarder.ForwardPorts()
	}()

	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case err := <-forwardErr:
		if err == nil {
			return 0, errors.New("port forward stopped before becoming ready")
		}
		return 0, fmt.Errorf("start port forward: %w", err)
	case <-ready:
	}

	ports, err := forwarder.GetPorts()
	if err != nil {
		return 0, fmt.Errorf("get forwarded port: %w", err)
	}
	if len(ports) != 1 {
		return 0, fmt.Errorf("port forward returned %d ports, expected 1", len(ports))
	}
	return ports[0].Local, nil
}

func getForwardedLoopback(ctx context.Context, localPort uint16, path string) ([]byte, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"http://127.0.0.1:"+strconv.FormatUint(uint64(localPort), 10)+path,
		http.NoBody,
	)
	if err != nil {
		return nil, fmt.Errorf("create loopback request: %w", err)
	}
	httpClient := &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true},
		Timeout:   loopbackRequestTimeout,
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send loopback request: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read loopback response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("loopback endpoint returned HTTP %d: %s", response.StatusCode, body)
	}
	return body, nil
}
