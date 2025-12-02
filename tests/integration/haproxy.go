//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
)

// HAProxyConfig holds configuration for deploying HAProxy
type HAProxyConfig struct {
	Image           string
	DataplanePort   int32
	DataplaneUser   string
	DataplanePass   string
	HAProxyStatPort int32
	HAProxyBin      string // Path to haproxy binary
	DataplaneBin    string // Path to dataplaneapi binary
}

// DefaultHAProxyConfig returns default HAProxy configuration
func DefaultHAProxyConfig() *HAProxyConfig {
	// Default to HAProxy 3.2 (current LTS release)
	// Can be overridden with HAPROXY_VERSION env var
	version := os.Getenv("HAPROXY_VERSION")
	if version == "" {
		version = "3.2"
	}

	// Check if HAProxy Enterprise should be used
	// Enterprise versions use format X.YrZ (e.g., 3.0r1, 3.1r1, 3.2r1)
	enterprise := os.Getenv("HAPROXY_ENTERPRISE")

	var image, haproxyBin, dataplaneBin string

	if enterprise == "true" {
		// Map community version to enterprise version format and get binary paths
		// Enterprise has versioned paths: /opt/hapee-X.Y/sbin/hapee-lb
		var tag, majorMinor string
		switch {
		case len(version) >= 3 && version[:3] == "3.0":
			tag = "3.0r1"
			majorMinor = "3.0"
		case len(version) >= 3 && version[:3] == "3.1":
			tag = "3.1r1"
			majorMinor = "3.1"
		case len(version) >= 3 && version[:3] == "3.2":
			tag = "3.2r1"
			majorMinor = "3.2"
		default:
			tag = version
			majorMinor = version
		}
		image = fmt.Sprintf("hapee-registry.haproxy.com/haproxy-enterprise:%s", tag)
		haproxyBin = fmt.Sprintf("/opt/hapee-%s/sbin/hapee-lb", majorMinor)
		dataplaneBin = "/opt/hapee-extras/sbin/hapee-dataplaneapi"
	} else {
		// Use debian image which includes dataplaneapi binary
		image = fmt.Sprintf("haproxytech/haproxy-debian:%s", version)
		haproxyBin = "/usr/local/sbin/haproxy"
		dataplaneBin = "/usr/local/bin/dataplaneapi"
	}

	return &HAProxyConfig{
		Image:           image,
		DataplanePort:   5555,
		DataplaneUser:   "admin",
		DataplanePass:   "adminpwd",
		HAProxyStatPort: 8404,
		HAProxyBin:      haproxyBin,
		DataplaneBin:    dataplaneBin,
	}
}

// HAProxyInstance represents a deployed HAProxy instance in Kubernetes
type HAProxyInstance struct {
	Name          string
	Namespace     string
	DataplanePort int32
	LocalPort     int32 // Port on localhost where dataplane is forwarded
	DataplaneUser string
	DataplanePass string
	pod           *corev1.Pod
	namespace     *Namespace
	stopChan      chan struct{}
	readyChan     chan struct{}
}

// DeployHAProxy deploys an HAProxy instance to the given namespace
func DeployHAProxy(ns *Namespace, cfg *HAProxyConfig) (*HAProxyInstance, error) {
	if cfg == nil {
		cfg = DefaultHAProxyConfig()
	}

	ctx := context.Background()
	name := "haproxy-test"

	// Create initial HAProxy config
	// Note: Dataplane API authentication is configured via dataplaneapi.yaml (user section)
	// and does not require a userlist in the HAProxy config
	initialHAProxyConfig := `global
    log stdout format raw local0
    stats socket /etc/haproxy/haproxy-master.sock mode 600 level admin

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

	// Create Dataplane API YAML config
	dataplaneConfig := fmt.Sprintf(`dataplaneapi:
  host: 0.0.0.0
  port: %d
  user:
    - name: %s
      password: %s
      insecure: true
  transaction:
    transaction_dir: /var/lib/dataplaneapi/transactions
    backups_number: 10
    backups_dir: /var/lib/dataplaneapi/backups
  resources:
    maps_dir: /etc/haproxy/maps
    ssl_certs_dir: /etc/haproxy/ssl
    general_storage_dir: /etc/haproxy/general
    spoe_dir: /etc/haproxy/spoe
haproxy:
  config_file: /etc/haproxy/haproxy.cfg
  haproxy_bin: %s
  master_worker_mode: true
  master_runtime: /etc/haproxy/haproxy-master.sock
  reload:
    reload_delay: 5
    reload_cmd: kill -USR2 1
    restart_cmd: kill -USR2 1
    reload_strategy: custom
log_targets:
- log_to: stdout
  log_level: trace
  log_types:
  - access
  - app
`, cfg.DataplanePort, cfg.DataplaneUser, cfg.DataplanePass, cfg.HAProxyBin)

	// Create ConfigMap with both configs
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-config",
			Namespace: ns.Name,
		},
		Data: map[string]string{
			"haproxy.cfg":       initialHAProxyConfig,
			"dataplaneapi.yaml": dataplaneConfig,
		},
	}

	_, err := ns.clientset.CoreV1().ConfigMaps(ns.Name).Create(ctx, configMap, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create configmap: %w", err)
	}

	// Create Pod with HAProxy and Dataplane API sidecar
	// Use two-container pattern: one for HAProxy, one for Dataplane API
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns.Name,
			Labels: map[string]string{
				"app": name,
			},
		},
		Spec: corev1.PodSpec{
			// Init container to set up directories
			InitContainers: []corev1.Container{
				{
					Name:    "init-dirs",
					Image:   cfg.Image,
					Command: []string{"/bin/sh", "-c"},
					Args: []string{`
						mkdir -p /etc/haproxy/maps /etc/haproxy/ssl /etc/haproxy/general /etc/haproxy/spoe
						mkdir -p /var/lib/dataplaneapi/transactions /var/lib/dataplaneapi/backups
						cp /config/haproxy.cfg /etc/haproxy/haproxy.cfg
						cp /config/dataplaneapi.yaml /etc/haproxy/dataplaneapi.yaml
						chown -R haproxy:haproxy /etc/haproxy /var/lib/dataplaneapi 2>/dev/null || true
					`},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "haproxy-runtime",
							MountPath: "/etc/haproxy",
						},
						{
							Name:      "dataplaneapi-data",
							MountPath: "/var/lib/dataplaneapi",
						},
						{
							Name:      "config",
							MountPath: "/config",
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:    "haproxy",
					Image:   cfg.Image,
					Command: []string{cfg.HAProxyBin},
					Args: []string{
						"-W",                                     // master-worker mode
						"-db",                                    // disable background mode
						"-S", "/etc/haproxy/haproxy-master.sock", // master socket
						"--",
						"/etc/haproxy/haproxy.cfg",
					},
					Ports: []corev1.ContainerPort{
						{
							Name:          "stats",
							ContainerPort: cfg.HAProxyStatPort,
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "haproxy-runtime",
							MountPath: "/etc/haproxy",
						},
					},
				},
				{
					Name:    "dataplane",
					Image:   cfg.Image,
					Command: []string{cfg.DataplaneBin},
					Args:    []string{"-f", "/etc/haproxy/dataplaneapi.yaml"},
					Ports: []corev1.ContainerPort{
						{
							Name:          "dataplane",
							ContainerPort: cfg.DataplanePort,
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "haproxy-runtime",
							MountPath: "/etc/haproxy",
						},
						{
							Name:      "dataplaneapi-data",
							MountPath: "/var/lib/dataplaneapi",
						},
					},
					LivenessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/v3/info",
								Port: intstr.FromInt(int(cfg.DataplanePort)),
								HTTPHeaders: []corev1.HTTPHeader{
									{
										Name:  "Authorization",
										Value: fmt.Sprintf("Basic %s", base64Encode(fmt.Sprintf("%s:%s", cfg.DataplaneUser, cfg.DataplanePass))),
									},
								},
							},
						},
						PeriodSeconds:    5,
						FailureThreshold: 3,
					},
					ReadinessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/v3/info",
								Port: intstr.FromInt(int(cfg.DataplanePort)),
								HTTPHeaders: []corev1.HTTPHeader{
									{
										Name:  "Authorization",
										Value: fmt.Sprintf("Basic %s", base64Encode(fmt.Sprintf("%s:%s", cfg.DataplaneUser, cfg.DataplanePass))),
									},
								},
							},
						},
						PeriodSeconds: 5,
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "config",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: name + "-config",
							},
						},
					},
				},
				{
					Name: "haproxy-runtime",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: "dataplaneapi-data",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
		},
	}

	createdPod, err := ns.clientset.CoreV1().Pods(ns.Name).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create pod: %w", err)
	}

	// Find a free local port for port forwarding
	// This allows multiple tests to run in parallel without port conflicts
	localPort, err := getFreePort()
	if err != nil {
		return nil, fmt.Errorf("failed to find free port: %w", err)
	}

	instance := &HAProxyInstance{
		Name:          name,
		Namespace:     ns.Name,
		DataplanePort: cfg.DataplanePort,
		LocalPort:     int32(localPort),
		DataplaneUser: cfg.DataplaneUser,
		DataplanePass: cfg.DataplanePass,
		pod:           createdPod,
		namespace:     ns,
		stopChan:      make(chan struct{}, 1),
		readyChan:     make(chan struct{}),
	}

	// Wait for pod to be ready (5 minutes to account for resource contention)
	if err := instance.WaitReady(5 * time.Minute); err != nil {
		return nil, fmt.Errorf("haproxy pod not ready: %w", err)
	}

	// Set up port forwarding
	if err := instance.setupPortForward(); err != nil {
		return nil, fmt.Errorf("failed to setup port forwarding: %w", err)
	}

	// Wait for port forwarding to be ready
	select {
	case <-instance.readyChan:
		// Port forwarding is ready
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("port forwarding did not become ready in time")
	}

	// Wait for Dataplane API to actually be responding
	if err := instance.waitForDataplaneAPI(30 * time.Second); err != nil {
		return nil, fmt.Errorf("dataplane API not responding: %w", err)
	}

	return instance, nil
}

// WaitReady waits for the HAProxy pod to be ready
func (h *HAProxyInstance) WaitReady(timeout time.Duration) error {
	ctx := context.Background()

	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		pod, err := h.namespace.clientset.CoreV1().Pods(h.Namespace).Get(ctx, h.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		// Check if pod is running and ready
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

	// If we timed out, log diagnostic information
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

// waitForDataplaneAPI polls the Dataplane API until it responds or timeout.
// This is called after port forwarding is established to ensure the API is
// actually responding before returning the HAProxy instance to tests.
func (h *HAProxyInstance) waitForDataplaneAPI(timeout time.Duration) error {
	endpoint := fmt.Sprintf("http://localhost:%d/v3/info", h.LocalPort)
	client := &http.Client{Timeout: 5 * time.Second}

	return WaitForCondition(context.Background(), WaitConfig{
		InitialInterval: 100 * time.Millisecond,
		MaxInterval:     2 * time.Second,
		Timeout:         timeout,
		Multiplier:      2.0,
	}, func(ctx context.Context) (bool, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		if err != nil {
			return false, err
		}
		req.SetBasicAuth(h.DataplaneUser, h.DataplanePass)

		resp, err := client.Do(req)
		if err != nil {
			// Connection errors are transient, keep retrying
			return false, nil
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return true, nil
		}

		// Non-200 responses mean the API is responding but not ready yet
		return false, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	})
}

// DataplaneEndpoint represents connection details for the Dataplane API
type DataplaneEndpoint struct {
	URL      string
	Username string
	Password string
}

// setupPortForward sets up port forwarding from localhost to the HAProxy pod
func (h *HAProxyInstance) setupPortForward() error {
	// Get rest config from the namespace's cluster
	config, err := h.namespace.cluster.getRestConfig()
	if err != nil {
		return fmt.Errorf("failed to get rest config: %w", err)
	}

	// Build the port forward URL
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", h.Namespace, h.Name)
	hostIP := config.Host
	serverURL, err := url.Parse(hostIP)
	if err != nil {
		return fmt.Errorf("failed to parse host: %w", err)
	}
	serverURL.Path = path

	// Create the port forward request
	transport, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		return fmt.Errorf("failed to create round tripper: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", serverURL)

	ports := []string{fmt.Sprintf("%d:%d", h.LocalPort, h.DataplanePort)}
	fw, err := portforward.New(dialer, ports, h.stopChan, h.readyChan, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to create port forwarder: %w", err)
	}

	// Start port forwarding in background
	go func() {
		if err := fw.ForwardPorts(); err != nil {
			// Log error but don't fail - the test will fail when it can't connect
			fmt.Printf("Port forwarding error: %v\n", err)
		}
	}()

	return nil
}

// GetDataplaneEndpoint returns the connection details for accessing the Dataplane API
func (h *HAProxyInstance) GetDataplaneEndpoint() DataplaneEndpoint {
	return DataplaneEndpoint{
		URL:      fmt.Sprintf("http://localhost:%d/v3", h.LocalPort),
		Username: h.DataplaneUser,
		Password: h.DataplanePass,
	}
}

// Delete removes the HAProxy instance and associated resources
func (h *HAProxyInstance) Delete() error {
	ctx := context.Background()

	// Stop port forwarding
	if h.stopChan != nil {
		close(h.stopChan)
	}

	// Delete Pod
	err := h.namespace.clientset.CoreV1().Pods(h.Namespace).Delete(ctx, h.Name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete pod: %w", err)
	}

	// Delete ConfigMap
	err = h.namespace.clientset.CoreV1().ConfigMaps(h.Namespace).Delete(ctx, h.Name+"-config", metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete configmap: %w", err)
	}

	return nil
}

// getFreePort finds an available port on the local machine
func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}

	listener, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer listener.Close()

	return listener.Addr().(*net.TCPAddr).Port, nil
}

// base64Encode encodes a string to base64
func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// GetCurrentConfig reads the current HAProxy configuration from the pod
func (h *HAProxyInstance) GetCurrentConfig() (string, error) {
	ctx := context.Background()

	// Get REST config from the namespace's cluster
	config, err := h.namespace.cluster.getRestConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get rest config: %w", err)
	}

	// Create the exec request
	req := h.namespace.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(h.Name).
		Namespace(h.Namespace).
		SubResource("exec")

	// Configure exec options
	option := &corev1.PodExecOptions{
		Container: "haproxy", // Read from haproxy container
		Command:   []string{"cat", "/etc/haproxy/haproxy.cfg"},
		Stdin:     false,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}

	req.VersionedParams(
		option,
		scheme.ParameterCodec,
	)

	// Create SPDY executor
	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return "", fmt.Errorf("failed to create executor: %w", err)
	}

	// Buffers to capture output
	var stdout, stderr bytes.Buffer

	// Execute the command
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: &stdout,
		Stderr: &stderr,
		Tty:    false,
	})

	if err != nil {
		return "", fmt.Errorf("failed to exec into pod: %w (stderr: %s)", err, stderr.String())
	}

	return stdout.String(), nil
}
