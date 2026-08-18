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
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// The pod's file tree, as the chart lays it out: one volume at BaseDir, a
// second one at GeneralDir so a sidecar can read rendered files without
// reaching private keys. Manifest paths are relative to BaseDir and are the
// same strings HAProxy names maps and certificates by at runtime.
const (
	BaseDir          = "/etc/haproxy"
	ConfigPath       = "haproxy.cfg"
	MapsDir          = "maps"
	SSLDir           = "ssl"
	GeneralDir       = "general"
	MasterSocketPath = BaseDir + "/haproxy-master.sock"
	WorkerSocketPath = BaseDir + "/haproxy-worker.sock"
)

// Container names, matching the chart's HAProxy pod.
const (
	HAProxyContainer = "haproxy"
	AgentContainer   = "agent"
)

// HAProxyConfig holds configuration for deploying the HAProxy pod.
type HAProxyConfig struct {
	Image           string
	AgentPort       int32
	AgentUser       string
	AgentPass       string
	HAProxyStatPort int32
}

// DefaultHAProxyConfig returns the default HAProxy pod configuration. The
// image is built once per run (image.go) and carries both binaries.
func DefaultHAProxyConfig(image string) *HAProxyConfig {
	return &HAProxyConfig{
		Image:           image,
		AgentPort:       5555,
		AgentUser:       "admin",
		AgentPass:       "adminpwd",
		HAProxyStatPort: 8404,
	}
}

// HAProxyInstance represents a deployed HAProxy pod: the HAProxy container in
// master-worker mode plus the agent container that owns its file tree.
type HAProxyInstance struct {
	Name      string
	Namespace string
	AgentPort int32
	LocalPort int32 // port on localhost the agent API is forwarded to
	AgentUser string
	AgentPass string
	pod       *corev1.Pod
	namespace *Namespace
	stopChan  chan struct{}
	readyChan chan struct{}
}

// bootstrapConfig is what the pod starts on before the first apply, matching
// the chart's initialConfig: HAProxy parses it, binds the status frontend and
// opens the worker stats socket the agent runs its commands on.
const bootstrapConfig = `global
    log stdout format raw local0
    stats socket ` + WorkerSocketPath + ` mode 600 level admin

defaults
    log     global
    mode    http
    option  httplog
    timeout connect 5000ms
    timeout client  50000ms
    timeout server  50000ms

frontend status
    bind *:8404
    http-request return status 200 content-type text/plain string "OK" if { path /healthz }
`

// DeployHAProxy deploys an HAProxy pod with its agent into the given namespace.
func DeployHAProxy(ns *Namespace, cfg *HAProxyConfig) (*HAProxyInstance, error) {
	ctx := context.Background()
	name := "haproxy-test"

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-config",
			Namespace: ns.Name,
		},
		Data: map[string]string{ConfigPath: bootstrapConfig},
	}
	if _, err := ns.clientset.CoreV1().ConfigMaps(ns.Name).Create(ctx, configMap, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("failed to create configmap: %w", err)
	}

	pod := haproxyPod(name, ns.Name, cfg)
	createdPod, err := ns.clientset.CoreV1().Pods(ns.Name).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create pod: %w", err)
	}

	instance := &HAProxyInstance{
		Name:      name,
		Namespace: ns.Name,
		AgentPort: cfg.AgentPort,
		AgentUser: cfg.AgentUser,
		AgentPass: cfg.AgentPass,
		pod:       createdPod,
		namespace: ns,
	}

	// 5 minutes to account for resource contention on a busy CI runner.
	if err := instance.WaitReady(5 * time.Minute); err != nil {
		return nil, fmt.Errorf("haproxy pod not ready: %w", err)
	}
	if err := instance.forwardAgentPort(); err != nil {
		return nil, err
	}
	if err := instance.waitForAgent(60 * time.Second); err != nil {
		return nil, fmt.Errorf("agent not responding: %w", err)
	}
	return instance, nil
}

// initScript creates the directories the manifest writes into. The agent owns
// files, not the tree's skeleton, exactly as in the chart.
const initScript = `set -e
mkdir -p ` + BaseDir + `/` + MapsDir + ` ` + BaseDir + `/` + SSLDir + ` ` + BaseDir + `/` + GeneralDir + `
cp /config/` + ConfigPath + ` ` + BaseDir + `/` + ConfigPath + `
chown -R haproxy:haproxy ` + BaseDir + ` 2>/dev/null || true
`

func haproxyPod(name, namespace string, cfg *HAProxyConfig) *corev1.Pod {
	runtimeMount := corev1.VolumeMount{Name: "haproxy-runtime", MountPath: BaseDir}
	generalMount := corev1.VolumeMount{Name: "general-storage", MountPath: BaseDir + "/" + GeneralDir}
	podMounts := []corev1.VolumeMount{runtimeMount, generalMount}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app": name},
		},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{
				Name:         "init-dirs",
				Image:        cfg.Image,
				Command:      []string{"/bin/sh", "-c"},
				Args:         []string{initScript},
				VolumeMounts: append(podMounts, corev1.VolumeMount{Name: "config", MountPath: "/config"}),
			}},
			Containers: []corev1.Container{
				{
					Name:    HAProxyContainer,
					Image:   cfg.Image,
					Command: []string{"haproxy"},
					Args: []string{
						"-W", "-db",
						"-S", MasterSocketPath + ",level,admin",
						"--", BaseDir + "/" + ConfigPath,
					},
					Ports:        []corev1.ContainerPort{{Name: "stats", ContainerPort: cfg.HAProxyStatPort}},
					VolumeMounts: podMounts,
				},
				{
					Name:    AgentContainer,
					Image:   cfg.Image,
					Command: []string{"/usr/local/bin/haptic"},
					Args: []string{
						"agent",
						"--base-dir", BaseDir,
						"--config", ConfigPath,
						"--listen", fmt.Sprintf(":%d", cfg.AgentPort),
						// Short enough that a test never waits on the pacing
						// window, long enough to still exercise scheduling.
						"--reload-interval-min", "1s",
					},
					Env: []corev1.EnvVar{
						{Name: "DATAPLANE_USERNAME", Value: cfg.AgentUser},
						{Name: "DATAPLANE_PASSWORD", Value: cfg.AgentPass},
						{Name: "LOG_LEVEL", Value: "debug"},
					},
					Ports:        []corev1.ContainerPort{{Name: "dataplane", ContainerPort: cfg.AgentPort}},
					VolumeMounts: podMounts,
					// Readiness means "the agent can accept applies" and is
					// never tied to an apply outcome, so a NACK must not drain
					// the pod. That is the chart's probe layout too.
					StartupProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
							Path: api.PathReadyz,
							Port: intstr.FromInt(int(cfg.AgentPort)),
						}},
						PeriodSeconds:    2,
						FailureThreshold: 60,
					},
					LivenessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
							Path: api.PathHealthz,
							Port: intstr.FromInt(int(cfg.AgentPort)),
						}},
						PeriodSeconds:    5,
						FailureThreshold: 3,
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "config",
					VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: name + "-config"},
					}},
				},
				{Name: "haproxy-runtime", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
				{Name: "general-storage", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
			},
		},
	}
}

// WaitReady waits for the HAProxy pod to be ready.
func (h *HAProxyInstance) WaitReady(timeout time.Duration) error {
	ctx := context.Background()

	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		pod, err := h.namespace.clientset.CoreV1().Pods(h.Namespace).Get(ctx, h.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if pod.Status.Phase != corev1.PodRunning {
			return false, nil
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})

	if err != nil {
		pod, getErr := h.namespace.clientset.CoreV1().Pods(h.Namespace).Get(ctx, h.Name, metav1.GetOptions{})
		if getErr == nil {
			fmt.Printf("\nPod '%s' failed to become ready:\n", h.Name)
			fmt.Printf("  Phase: %s\n", pod.Status.Phase)
			fmt.Printf("  Conditions:\n")
			for _, cond := range pod.Status.Conditions {
				fmt.Printf("    %s: %s - %s\n", cond.Type, cond.Status, cond.Message)
			}
			fmt.Printf("  Container Statuses:\n")
			for _, cs := range pod.Status.ContainerStatuses {
				fmt.Printf("    %s: Ready=%v, RestartCount=%d\n", cs.Name, cs.Ready, cs.RestartCount)
				if cs.State.Waiting != nil {
					fmt.Printf("      Waiting: %s - %s\n", cs.State.Waiting.Reason, cs.State.Waiting.Message)
				}
				if cs.State.Terminated != nil {
					fmt.Printf("      Terminated: %s (exit %d) - %s\n", cs.State.Terminated.Reason, cs.State.Terminated.ExitCode, cs.State.Terminated.Message)
				}
			}
		}
	}

	return err
}

// forwardAgentPort forwards a free local port to the agent's API port. In
// parallel runs another test can take the port between choosing and binding
// it, so a collision is retried with a new one.
func (h *HAProxyInstance) forwardAgentPort() error {
	const maxPortRetries = 5
	var lastErr error
	for attempt := 1; attempt <= maxPortRetries; attempt++ {
		localPort, err := getFreePort()
		if err != nil {
			return fmt.Errorf("failed to find free port: %w", err)
		}

		h.LocalPort = int32(localPort)
		h.stopChan = make(chan struct{}, 1)
		h.readyChan = make(chan struct{})

		if err := h.setupPortForward(); err != nil {
			lastErr = err
			fmt.Printf("Port forward attempt %d failed: %v (retrying with new port)\n", attempt, err)
			continue
		}

		select {
		case <-h.readyChan:
			return nil
		case <-time.After(10 * time.Second):
			close(h.stopChan)
			lastErr = fmt.Errorf("port forwarding did not become ready in time (attempt %d)", attempt)
			fmt.Printf("Port forward attempt %d timed out (retrying with new port)\n", attempt)
		}
	}
	return fmt.Errorf("failed to setup port forwarding after %d attempts: %w", maxPortRetries, lastErr)
}

// waitForAgent polls the agent's readiness endpoint through the forwarded
// port, so a test never sends the first apply into a half-open tunnel.
func (h *HAProxyInstance) waitForAgent(timeout time.Duration) error {
	endpoint := h.AgentURL() + api.PathReadyz
	client := &http.Client{Timeout: 5 * time.Second}

	return testutil.WaitForCondition(context.Background(), testutil.WaitConfig{
		InitialInterval: 100 * time.Millisecond,
		MaxInterval:     2 * time.Second,
		Timeout:         timeout,
		Multiplier:      2.0,
	}, func(ctx context.Context) (bool, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
		if err != nil {
			return false, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, nil // the tunnel is still coming up
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode == http.StatusOK, nil
	})
}

// AgentURL is the base URL of this pod's agent through the forwarded port.
func (h *HAProxyInstance) AgentURL() string {
	return fmt.Sprintf("http://localhost:%d", h.LocalPort)
}

// setupPortForward sets up port forwarding from localhost to the HAProxy pod.
func (h *HAProxyInstance) setupPortForward() error {
	config, err := h.namespace.cluster.getRestConfig()
	if err != nil {
		return fmt.Errorf("failed to get rest config: %w", err)
	}

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", h.Namespace, h.Name)
	serverURL, err := url.Parse(config.Host)
	if err != nil {
		return fmt.Errorf("failed to parse host: %w", err)
	}
	serverURL.Path = path

	transport, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		return fmt.Errorf("failed to create round tripper: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", serverURL)

	ports := []string{fmt.Sprintf("%d:%d", h.LocalPort, h.AgentPort)}
	fw, err := portforward.New(dialer, ports, h.stopChan, h.readyChan, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to create port forwarder: %w", err)
	}

	go func() {
		if err := fw.ForwardPorts(); err != nil {
			// The test fails on the connection it cannot make; logging keeps
			// the cause visible.
			fmt.Printf("Port forwarding error: %v\n", err)
		}
	}()

	return nil
}

// Delete removes the HAProxy instance and associated resources.
func (h *HAProxyInstance) Delete() error {
	ctx := context.Background()

	if h.stopChan != nil {
		close(h.stopChan)
	}

	err := h.namespace.clientset.CoreV1().Pods(h.Namespace).Delete(ctx, h.Name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete pod: %w", err)
	}

	err = h.namespace.clientset.CoreV1().ConfigMaps(h.Namespace).Delete(ctx, h.Name+"-config", metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete configmap: %w", err)
	}

	return nil
}

// getFreePort finds an available port on the local machine.
func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}

	listener, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer func() { _ = listener.Close() }()

	return listener.Addr().(*net.TCPAddr).Port, nil
}

// GetContainerLogs fetches logs from the specified container in the HAProxy pod.
func (h *HAProxyInstance) GetContainerLogs(containerName string, tailLines int64) (string, error) {
	ctx := context.Background()

	opts := &corev1.PodLogOptions{
		Container: containerName,
		TailLines: &tailLines,
	}

	req := h.namespace.clientset.CoreV1().Pods(h.Namespace).GetLogs(h.Name, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get logs for container %s: %w", containerName, err)
	}
	defer func() { _ = stream.Close() }()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, stream); err != nil {
		return "", fmt.Errorf("failed to read logs: %w", err)
	}

	return buf.String(), nil
}

// DumpLogsOnFailure prints container logs if the test has failed.
// Call this in t.Cleanup() to capture logs on any failure.
func (h *HAProxyInstance) DumpLogsOnFailure(t *testing.T) {
	if !t.Failed() {
		return
	}

	for _, container := range []string{HAProxyContainer, AgentContainer} {
		t.Logf("\n========== %s container logs (last 100 lines) ==========", container)
		logs, err := h.GetContainerLogs(container, 100)
		if err != nil {
			t.Logf("Failed to get %s logs: %v", container, err)
			continue
		}
		t.Logf("%s", logs)
	}
}
