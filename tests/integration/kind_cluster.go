//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/flowcontrol"

	"gitlab.com/haproxy-haptic/haptic/tests/kindutil"
	"gitlab.com/haproxy-haptic/haptic/tests/process"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// KindClusterConfig holds configuration for creating a Kind cluster.
type KindClusterConfig struct {
	Name string
	// Image is the Kind node image to use (e.g., "kindest/node:v1.32.0")
	// If empty, uses the image from KIND_NODE_IMAGE env var or defaults to kindest/node:v1.32.0
	Image string
}

// KindCluster represents a Kind (Kubernetes in Docker) cluster for testing.
type KindCluster struct {
	Name       string
	Kubeconfig string
	owned      *kindutil.Cluster
	clientset  *kubernetes.Clientset
}

// SetupKindCluster creates or reuses a Kind cluster for integration testing.
func SetupKindCluster(cfg *KindClusterConfig) (*KindCluster, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	exists, err := kindutil.ClusterExists(ctx, process.Executor{}, cfg.Name, kindutil.DockerEnvironment(os.Getenv))
	if err != nil {
		return nil, err
	}
	var owned *kindutil.Cluster
	if exists {
		owned, _, err = kindutil.ResumeCluster(ctx, process.Executor{}, cfg.Name, kindutil.DockerEnvironment(os.Getenv))
	} else {
		owned, err = createKindCluster(ctx, cfg)
	}
	if err != nil {
		return nil, err
	}

	// Get kubeconfig
	kubeconfig, err := kindutil.ClusterKubeconfig(ctx, process.Executor{}, cfg.Name, kindutil.DockerEnvironment(os.Getenv))
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
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

	kindCluster := &KindCluster{
		Name:       cfg.Name,
		Kubeconfig: kubeconfig,
		owned:      owned,
		clientset:  clientset,
	}

	// Wait for API server to be fully ready
	// This ensures the API server is accepting connections before tests proceed
	fmt.Printf("⏳ Waiting for API server to become ready...\n")
	if err := waitForAPIServer(ctx, clientset, 2*time.Minute); err != nil {
		return nil, fmt.Errorf("API server failed to become ready: %w", err)
	}
	fmt.Printf("✓ API server is ready\n")

	// Trigger background cleanup of old test namespaces
	// This runs asynchronously and doesn't block test execution
	kindCluster.CleanupOldTestNamespacesAsync()

	return kindCluster, nil
}

func createKindCluster(ctx context.Context, cfg *KindClusterConfig) (*kindutil.Cluster, error) {
	artifacts, err := os.MkdirTemp("", "haptic-integration-")
	if err != nil {
		return nil, err
	}
	owned, err := kindutil.NewCluster(process.Executor{}, &kindutil.ClusterOptions{Name: cfg.Name, Kubeconfig: filepath.Join(artifacts, "kubeconfig"), Artifacts: artifacts, Environment: kindutil.DockerEnvironment(os.Getenv), ReadyTimeout: 5 * time.Minute})
	if err != nil {
		return nil, err
	}
	image := cfg.Image
	if image == "" {
		image = os.Getenv("KIND_NODE_IMAGE")
	}
	if image == "" {
		image = "kindest/node:v1.32.0"
	}
	if err := owned.Create(ctx, image); err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		return nil, errors.Join(err, owned.Close(cleanup))
	}
	return owned, nil
}

func waitForAPIServer(ctx context.Context, clientset *kubernetes.Clientset, timeout time.Duration) error {
	return testutil.Poll(ctx, testutil.WaitConfig{Timeout: timeout, InitialInterval: 2 * time.Second, MaxInterval: 2 * time.Second, Multiplier: 1}, "API server readiness", func(ctx context.Context) (testutil.PollResult, error) {
		_, err := clientset.Discovery().RESTClient().Get().AbsPath("/version").DoRaw(ctx)
		if err != nil {
			return testutil.PollPending, err
		}
		return testutil.PollSucceeded, nil
	})
}

// CreateNamespace creates a new namespace in the cluster.
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

	// Wait for the "default" ServiceAccount before handing the namespace to
	// tests. The SA is populated asynchronously by kube-controller-manager;
	// creating a pod before it exists fails with "serviceaccount \"default\"
	// not found". Fast CI machines mask the race, but a busy control plane
	// (or one recovering from a lost leader-election lease) widens it to
	// whole seconds.
	if err := k.waitForDefaultServiceAccount(ctx, created.Name); err != nil {
		return nil, fmt.Errorf("waiting for default service account in %s: %w", created.Name, err)
	}

	return &Namespace{
		Name:      created.Name,
		cluster:   k,
		clientset: k.clientset,
	}, nil
}

// waitForDefaultServiceAccount polls until kube-controller-manager has
// created the "default" ServiceAccount in the namespace. The 60s budget is
// generous on purpose: it must cover a controller-manager restart (lost
// leader-election lease under load), after which the SA controller needs to
// re-sync every namespace.
func (k *KindCluster) waitForDefaultServiceAccount(ctx context.Context, namespace string) error {
	return testutil.Poll(ctx, testutil.WaitConfig{Timeout: 60 * time.Second, InitialInterval: 250 * time.Millisecond, MaxInterval: 250 * time.Millisecond, Multiplier: 1}, "default service account", func(ctx context.Context) (testutil.PollResult, error) {
		_, err := k.clientset.CoreV1().ServiceAccounts(namespace).Get(ctx, "default", metav1.GetOptions{})
		if err != nil {
			return testutil.PollPending, err
		}
		return testutil.PollSucceeded, nil
	})
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
		for i := range namespaces.Items {
			ns := &namespaces.Items[i]
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

// Teardown destroys the Kind cluster.
func (k *KindCluster) Teardown() error {
	if k.owned == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return k.owned.Close(ctx)
}

// Namespace represents a Kubernetes namespace for test isolation.
type Namespace struct {
	Name      string
	cluster   *KindCluster
	clientset *kubernetes.Clientset
}

// Delete removes the namespace from the cluster.
func (n *Namespace) Delete() error {
	ctx := context.Background()
	err := n.clientset.CoreV1().Namespaces().Delete(ctx, n.Name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete namespace: %w", err)
	}
	return nil
}

// getRestConfig returns the REST config for the Kind cluster.
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
// Values: "" (default) - keep cluster for faster subsequent runs, "false" - always cleanup.
func ShouldKeepCluster() string {
	val := os.Getenv("KEEP_CLUSTER")
	if val == "" {
		return "true" // Default to keeping cluster for faster test iterations
	}
	return val
}

// LoadDockerImage loads a locally built image into the Kind cluster, which has
// no registry to pull it from.
func (k *KindCluster) LoadDockerImage(image string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if k.owned != nil {
		return k.owned.LoadImages(ctx, image)
	}
	result, err := (process.Executor{}).Run(ctx, &process.Command{Name: "kind", Args: []string{"load", "docker-image", image, "--name", k.Name}, Env: kindutil.DockerEnvironment(os.Getenv)})
	if err != nil {
		return fmt.Errorf("failed to load image: %w\nOutput: %s", err, result.Combined)
	}

	return nil
}
