//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/flowcontrol"
	"sigs.k8s.io/kind/pkg/cluster"
	"sigs.k8s.io/kind/pkg/cmd"

	"haptic/tests/kindutil"
)

// KindClusterConfig holds configuration for creating a Kind cluster
type KindClusterConfig struct {
	Name string
	// Image is the Kind node image to use (e.g., "kindest/node:v1.32.0")
	// If empty, uses the image from KIND_NODE_IMAGE env var or defaults to kindest/node:v1.32.0
	Image string
}

// GetNodePortHost returns the hostname for accessing NodePort services.
// In Docker-in-Docker, this is the dind hostname; otherwise localhost.
func GetNodePortHost() string {
	return kindutil.GetNodePortHost()
}

// KindCluster represents a Kind (Kubernetes in Docker) cluster for testing
type KindCluster struct {
	Name       string
	Kubeconfig string
	provider   *cluster.Provider
	clientset  *kubernetes.Clientset
}

// SetupKindCluster creates or reuses a Kind cluster for integration testing
func SetupKindCluster(cfg *KindClusterConfig) (*KindCluster, error) {
	provider := cluster.NewProvider(
		cluster.ProviderWithLogger(cmd.NewLogger()),
	)

	// Check if cluster already exists
	clusters, err := provider.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list clusters: %w", err)
	}

	clusterExists := false
	for _, c := range clusters {
		if c == cfg.Name {
			clusterExists = true
			fmt.Printf("♻️  Reusing existing Kind cluster '%s'\n", cfg.Name)
			break
		}
	}

	// Only create if doesn't exist
	if !clusterExists {
		fmt.Printf("🆕 Creating new Kind cluster '%s'\n", cfg.Name)

		// Determine node image to use
		nodeImage := cfg.Image
		if nodeImage == "" {
			// Check environment variable (full image path like "kindest/node:v1.32.0")
			nodeImage = os.Getenv("KIND_NODE_IMAGE")
			// If env var is not set, use default known-working version
			if nodeImage == "" {
				nodeImage = "kindest/node:v1.32.0"
			}
		}

		// Prepare cluster creation options
		createOpts := []cluster.CreateOption{
			cluster.CreateWithWaitForReady(5 * time.Minute),
		}

		// Add node image if specified
		if nodeImage != "" {
			createOpts = append(createOpts, cluster.CreateWithNodeImage(nodeImage))
		}

		// Use custom config for increased pod limits (500 vs default 110)
		// This is required for parallel integration tests that create many namespaces
		if kindutil.IsDockerInDocker() {
			fmt.Printf("📦 Detected Docker-in-Docker environment, using dind-compatible config\n")
			createOpts = append(createOpts, cluster.CreateWithRawConfig([]byte(kindutil.DindKindConfig)))
		} else {
			fmt.Printf("📦 Using base config with increased pod limits\n")
			createOpts = append(createOpts, cluster.CreateWithRawConfig([]byte(kindutil.BaseKindConfig)))
		}

		// Create the cluster
		if err := provider.Create(cfg.Name, createOpts...); err != nil {
			return nil, fmt.Errorf("failed to create kind cluster: %w", err)
		}
	}

	// Get kubeconfig
	kubeconfig, err := provider.KubeConfig(cfg.Name, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	// If running in Docker-in-Docker, patch the kubeconfig to use the dind hostname
	// instead of localhost/0.0.0.0 (which are not accessible from the CI job container)
	if kindutil.IsDockerInDocker() {
		kubeconfig = kindutil.PatchKubeconfigForDind(kubeconfig)
		fmt.Printf("📦 Patched kubeconfig for Docker-in-Docker (server: https://%s:...)\n", kindutil.GetDindHostname())
	}

	// Create Kubernetes client
	config, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfig))
	if err != nil {
		return nil, fmt.Errorf("failed to create rest config: %w", err)
	}

	// Disable client-side rate limiting for parallel tests
	// Default is QPS=5, Burst=10 which is too restrictive
	// Note: Setting QPS=0 and Burst=0 means "use defaults", not "disable"
	// Use FakeAlwaysRateLimiter to actually disable rate limiting
	config.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	cluster := &KindCluster{
		Name:       cfg.Name,
		Kubeconfig: kubeconfig,
		provider:   provider,
		clientset:  clientset,
	}

	// Wait for API server to be fully ready
	// This ensures the API server is accepting connections before tests proceed
	fmt.Printf("⏳ Waiting for API server to become ready...\n")
	if err := waitForAPIServer(clientset, 2*time.Minute); err != nil {
		return nil, fmt.Errorf("API server failed to become ready: %w", err)
	}
	fmt.Printf("✓ API server is ready\n")

	// Trigger background cleanup of old test namespaces
	// This runs asynchronously and doesn't block test execution
	cluster.CleanupOldTestNamespacesAsync()

	// Load HAProxy Enterprise image if enterprise mode is enabled
	// This is necessary because the enterprise registry requires authentication
	// and Kind cannot pull from it directly
	if os.Getenv("HAPROXY_ENTERPRISE") == "true" {
		haproxyImage := getHAProxyEnterpriseImage()

		// Pull the image from the registry first
		fmt.Printf("📥 Pulling HAProxy Enterprise image '%s'...\n", haproxyImage)
		if err := PullDockerImage(haproxyImage); err != nil {
			return nil, fmt.Errorf("failed to pull HAProxy Enterprise image (ensure you are logged in to hapee-registry.haproxy.com): %w", err)
		}
		fmt.Printf("✓ HAProxy Enterprise image pulled\n")

		// Load into Kind cluster
		fmt.Printf("📦 Loading HAProxy Enterprise image into Kind cluster...\n")
		if err := cluster.LoadDockerImage(haproxyImage); err != nil {
			return nil, fmt.Errorf("failed to load HAProxy Enterprise image: %w", err)
		}
		fmt.Printf("✓ HAProxy Enterprise image loaded\n")
	}

	return cluster, nil
}

// waitForAPIServer polls the API server until it responds successfully or timeout occurs.
// This is necessary because cluster creation may complete before the API server is fully ready
// to accept connections, leading to "connection refused" errors in tests.
func waitForAPIServer(clientset *kubernetes.Clientset, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for API server to become ready")
		case <-ticker.C:
			// Try to get server version - this is a lightweight API call
			// that confirms the API server is responding
			_, err := clientset.Discovery().ServerVersion()
			if err == nil {
				return nil // API server is ready
			}
			// Continue waiting on connection errors
			fmt.Printf("  API server not ready yet (will retry): %v\n", err)
		}
	}
}

// CreateNamespace creates a new namespace in the cluster
func (k *KindCluster) CreateNamespace(name string) (*Namespace, error) {
	ctx := context.Background()

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	created, err := k.clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create namespace: %w", err)
	}

	return &Namespace{
		Name:      created.Name,
		cluster:   k,
		clientset: k.clientset,
	}, nil
}

// CleanupOldTestNamespacesAsync triggers asynchronous cleanup of old test namespaces.
// This function returns immediately without blocking - cleanup happens in the background.
// It lists all namespaces with the "test-" prefix that are older than 5 minutes and deletes them.
// The 5-minute age threshold ensures newly created test namespaces are not affected.
func (k *KindCluster) CleanupOldTestNamespacesAsync() {
	go func() {
		ctx := context.Background()

		// List all namespaces with test- prefix
		namespaces, err := k.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			fmt.Printf("Background cleanup: failed to list namespaces: %v\n", err)
			return
		}

		// Current time for age comparison
		now := time.Now()
		ageThreshold := 5 * time.Minute

		// Filter to old test namespaces (created more than 5 minutes ago)
		var oldTestNamespaces []string
		for _, ns := range namespaces.Items {
			if len(ns.Name) >= 5 && ns.Name[:5] == "test-" {
				age := now.Sub(ns.CreationTimestamp.Time)
				if age > ageThreshold {
					oldTestNamespaces = append(oldTestNamespaces, ns.Name)
				}
			}
		}

		if len(oldTestNamespaces) == 0 {
			fmt.Printf("Background cleanup: no old test namespaces found\n")
			return
		}

		fmt.Printf("Background cleanup: deleting %d old test namespaces (>%v old) in background\n", len(oldTestNamespaces), ageThreshold)

		// Delete each old namespace
		for _, nsName := range oldTestNamespaces {
			err := k.clientset.CoreV1().Namespaces().Delete(ctx, nsName, metav1.DeleteOptions{})
			if err != nil {
				fmt.Printf("Background cleanup: failed to delete namespace %s: %v\n", nsName, err)
			}
		}

		fmt.Printf("Background cleanup: completed deletion of %d namespaces\n", len(oldTestNamespaces))
	}()
}

// Teardown destroys the Kind cluster
func (k *KindCluster) Teardown() error {
	if err := k.provider.Delete(k.Name, ""); err != nil {
		return fmt.Errorf("failed to delete kind cluster: %w", err)
	}
	return nil
}

// Namespace represents a Kubernetes namespace for test isolation
type Namespace struct {
	Name      string
	cluster   *KindCluster
	clientset *kubernetes.Clientset
}

// Delete removes the namespace from the cluster
func (n *Namespace) Delete() error {
	ctx := context.Background()
	err := n.clientset.CoreV1().Namespaces().Delete(ctx, n.Name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete namespace: %w", err)
	}
	return nil
}

// Clientset returns the Kubernetes clientset
func (n *Namespace) Clientset() *kubernetes.Clientset {
	return n.clientset
}

// getRestConfig returns the REST config for the Kind cluster
func (k *KindCluster) getRestConfig() (*rest.Config, error) {
	config, err := clientcmd.RESTConfigFromKubeConfig([]byte(k.Kubeconfig))
	if err != nil {
		return nil, fmt.Errorf("failed to create rest config: %w", err)
	}

	// Disable client-side rate limiting for parallel tests
	// Note: Setting QPS=0 and Burst=0 means "use defaults", not "disable"
	// Use FakeAlwaysRateLimiter to actually disable rate limiting
	config.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()

	return config, nil
}

// ShouldKeepCluster returns whether the cluster should be kept after tests
// based on the KEEP_CLUSTER environment variable.
// Values: "" (default) - keep cluster for faster subsequent runs, "false" - always cleanup
func ShouldKeepCluster() string {
	val := os.Getenv("KEEP_CLUSTER")
	if val == "" {
		return "true" // Default to keeping cluster for faster test iterations
	}
	return val
}

// PullDockerImage pulls a Docker image from a registry to the local Docker daemon.
// This is required for private registry images before loading into Kind.
func PullDockerImage(image string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "pull", image)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to pull image: %w\nOutput: %s", err, output)
	}

	return nil
}

// LoadDockerImage loads a Docker image from the host into the Kind cluster.
// This is necessary for images from private registries that require authentication,
// as Kind nodes cannot authenticate to pull images directly.
// The image must already be pulled locally (e.g., via `docker pull`).
func (k *KindCluster) LoadDockerImage(image string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "kind", "load", "docker-image", image, "--name", k.Name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to load image: %w\nOutput: %s", err, output)
	}

	return nil
}

// getHAProxyEnterpriseImage returns the HAProxy Enterprise image name based on
// the HAPROXY_VERSION environment variable.
func getHAProxyEnterpriseImage() string {
	version := os.Getenv("HAPROXY_VERSION")
	if version == "" {
		version = "3.2"
	}

	// Map community version to enterprise version format
	var tag string
	switch {
	case len(version) >= 3 && version[:3] == "3.0":
		tag = "3.0r1"
	case len(version) >= 3 && version[:3] == "3.1":
		tag = "3.1r1"
	case len(version) >= 3 && version[:3] == "3.2":
		tag = "3.2r1"
	default:
		tag = version
	}

	return fmt.Sprintf("hapee-registry.haproxy.com/haproxy-enterprise:%s", tag)
}
