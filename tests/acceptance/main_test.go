//go:build acceptance

package acceptance

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/support/kind"
	kindcluster "sigs.k8s.io/kind/pkg/cluster"
	kindcmd "sigs.k8s.io/kind/pkg/cmd"

	corev1 "k8s.io/api/core/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	haproxyv1alpha1 "haproxy-template-ic/pkg/apis/haproxytemplate/v1alpha1"
	"haproxy-template-ic/tests/kindutil"
)

func init() {
	// Register HAProxyTemplateConfig CRD scheme with the global client-go scheme
	// This allows the e2e-framework to understand our custom resources
	if err := haproxyv1alpha1.AddToScheme(clientgoscheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register haproxy scheme: %v", err))
	}
}

const (
	// TestKubeconfigPath is the isolated kubeconfig file for acceptance tests.
	// This prevents tests from accidentally modifying the user's default kubeconfig.
	TestKubeconfigPath = "/tmp/haproxy-test-kubeconfig"
)

// TestMain is the entry point for acceptance tests.
// It sets up the test environment with a kind cluster and ensures
// all Setup/Finish actions are properly executed.
//
// When running in CI with sharding (detected via SKIP_PARALLEL_RUNNER=true),
// the cluster is pre-created by helm/kind-action and this function only
// configures the test environment to use the existing cluster.
func TestMain(m *testing.M) {
	fmt.Println("DEBUG: TestMain is running!")

	// Create test environment with parallel execution enabled
	testEnv = env.NewParallel()
	fmt.Println("DEBUG: testEnv created:", testEnv != nil)

	// Check if running in CI sharding mode (cluster pre-created by helm/kind-action)
	if os.Getenv("SKIP_PARALLEL_RUNNER") == "true" {
		fmt.Println("DEBUG: CI sharding mode detected - using pre-created cluster")
		setupForCISharding()
	} else {
		fmt.Println("DEBUG: Local mode - creating dedicated cluster")
		setupForLocalDevelopment()
	}

	// Run tests
	os.Exit(testEnv.Run(m))
}

// setupForCISharding configures the test environment to use an existing cluster
// created by helm/kind-action in CI. The cluster, image, and CRD are already set up.
func setupForCISharding() {
	// In CI mode, KUBECONFIG is already set by helm/kind-action
	// Just validate the cluster is accessible
	testEnv.Setup(
		func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			// Use the kubeconfig from environment (set by helm/kind-action)
			kubeconfigPath := os.Getenv("KUBECONFIG")
			if kubeconfigPath == "" {
				kubeconfigPath = os.Getenv("HOME") + "/.kube/config"
			}
			fmt.Printf("DEBUG: Using kubeconfig from CI: %s\n", kubeconfigPath)

			cfg.WithKubeconfigFile(kubeconfigPath)

			// Validate cluster is accessible
			client, err := cfg.NewClient()
			if err != nil {
				return ctx, fmt.Errorf("failed to create client: %w", err)
			}

			var nodeList corev1.NodeList
			if err := client.Resources().List(ctx, &nodeList); err != nil {
				return ctx, fmt.Errorf("SAFETY CHECK FAILED: Cannot list nodes: %w", err)
			}
			if len(nodeList.Items) == 0 {
				return ctx, fmt.Errorf("SAFETY CHECK FAILED: Cluster has no nodes")
			}
			fmt.Printf("DEBUG: Cluster accessible with %d nodes\n", len(nodeList.Items))

			return ctx, nil
		},
	)

	// No cleanup needed in CI - the workflow handles cluster deletion
	testEnv.Finish(
		func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			fmt.Println("DEBUG: CI mode - skipping cluster cleanup (handled by workflow)")
			return ctx, nil
		},
	)
}

// setupForLocalDevelopment creates a dedicated Kind cluster for local testing.
// This is the original behavior for running `make test-acceptance` locally.
// It also handles Docker-in-Docker environments (e.g., GitLab CI).
func setupForLocalDevelopment() {
	// SAFETY: Isolate kubeconfig to prevent production cluster access
	if err := os.Setenv("KUBECONFIG", TestKubeconfigPath); err != nil {
		fmt.Printf("FATAL: Failed to set KUBECONFIG: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("DEBUG: Using isolated kubeconfig: %s\n", TestKubeconfigPath)

	kindClusterName := "haproxy-test"
	kindNodeImage := getKindNodeImage()

	// Check if running in Docker-in-Docker environment
	if kindutil.IsDockerInDocker() {
		fmt.Println("DEBUG: Detected Docker-in-Docker environment, using kind library directly")
		setupForDind(kindClusterName, kindNodeImage)
	} else {
		fmt.Println("DEBUG: Local environment, using e2e-framework kind provider")
		setupForLocal(kindClusterName, kindNodeImage)
	}
}

// setupForDind configures the test environment for Docker-in-Docker.
// It uses the kind library directly to create a cluster with DinD-compatible config.
func setupForDind(kindClusterName, kindNodeImage string) {
	provider := kindcluster.NewProvider(
		kindcluster.ProviderWithLogger(kindcmd.NewLogger()),
	)

	testEnv.Setup(
		func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			// Check if cluster already exists
			clusters, err := provider.List()
			if err != nil {
				return ctx, fmt.Errorf("failed to list clusters: %w", err)
			}

			clusterExists := false
			for _, c := range clusters {
				if c == kindClusterName {
					clusterExists = true
					fmt.Printf("DEBUG: Reusing existing Kind cluster '%s'\n", kindClusterName)
					break
				}
			}

			if !clusterExists {
				fmt.Printf("DEBUG: Creating new Kind cluster '%s' with DinD config\n", kindClusterName)

				createOpts := []kindcluster.CreateOption{
					kindcluster.CreateWithWaitForReady(5 * time.Minute),
					kindcluster.CreateWithNodeImage(kindNodeImage),
					kindcluster.CreateWithRawConfig([]byte(kindutil.DindKindConfig)),
				}

				if err := provider.Create(kindClusterName, createOpts...); err != nil {
					return ctx, fmt.Errorf("failed to create kind cluster: %w", err)
				}
			}

			// Get kubeconfig
			kubeconfig, err := provider.KubeConfig(kindClusterName, false)
			if err != nil {
				return ctx, fmt.Errorf("failed to get kubeconfig: %w", err)
			}

			// Patch kubeconfig for DinD (replace localhost with docker hostname)
			kubeconfig = kindutil.PatchKubeconfigForDind(kubeconfig)
			fmt.Printf("DEBUG: Patched kubeconfig for DinD (server: https://%s:...)\n", kindutil.GetDindHostname())

			// Write kubeconfig to file
			if err := os.WriteFile(TestKubeconfigPath, []byte(kubeconfig), 0600); err != nil {
				return ctx, fmt.Errorf("failed to write kubeconfig: %w", err)
			}

			cfg.WithKubeconfigFile(TestKubeconfigPath)

			// Validate cluster is accessible
			client, err := cfg.NewClient()
			if err != nil {
				return ctx, fmt.Errorf("failed to create client: %w", err)
			}

			var nodeList corev1.NodeList
			if err := client.Resources().List(ctx, &nodeList); err != nil {
				return ctx, fmt.Errorf("SAFETY CHECK FAILED: Cannot list nodes: %w", err)
			}
			if len(nodeList.Items) == 0 {
				return ctx, fmt.Errorf("SAFETY CHECK FAILED: Cluster has no nodes")
			}
			fmt.Printf("DEBUG: Cluster accessible with %d nodes\n", len(nodeList.Items))

			// Load controller image
			fmt.Println("DEBUG: Loading controller image into kind cluster...")
			cmd := exec.CommandContext(ctx, "kind", "load", "docker-image", "haproxy-template-ic:test", "--name", kindClusterName)
			if output, err := cmd.CombinedOutput(); err != nil {
				return ctx, fmt.Errorf("failed to load controller image into kind cluster: %w\nOutput: %s", err, string(output))
			}
			fmt.Println("DEBUG: Controller image loaded successfully")

			// Install HAProxyTemplateConfig CRD
			fmt.Println("DEBUG: Installing HAProxyTemplateConfig CRD...")
			crdPath := "../../charts/haproxy-template-ic/crds/haproxy-template-ic.gitlab.io_haproxytemplateconfigs.yaml"
			cmd = exec.CommandContext(ctx, "kubectl", "apply", "--kubeconfig", TestKubeconfigPath, "-f", crdPath)
			if output, err := cmd.CombinedOutput(); err != nil {
				return ctx, fmt.Errorf("failed to install HAProxyTemplateConfig CRD: %w\nOutput: %s", err, string(output))
			}
			fmt.Println("DEBUG: HAProxyTemplateConfig CRD installed successfully")

			// Wait for CRD to be established
			cmd = exec.CommandContext(ctx, "kubectl", "wait", "--kubeconfig", TestKubeconfigPath,
				"--for=condition=Established",
				"crd/haproxytemplateconfigs.haproxy-template-ic.gitlab.io",
				"--timeout=60s")
			if output, err := cmd.CombinedOutput(); err != nil {
				return ctx, fmt.Errorf("failed to wait for CRD to be established: %w\nOutput: %s", err, string(output))
			}

			return ctx, nil
		},
	)

	testEnv.Finish(
		func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			if err := provider.Delete(kindClusterName, ""); err != nil {
				fmt.Printf("Warning: failed to destroy kind cluster: %v\n", err)
			}
			return ctx, nil
		},
		func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			if err := os.Remove(TestKubeconfigPath); err != nil && !os.IsNotExist(err) {
				fmt.Printf("Warning: failed to remove test kubeconfig %s: %v\n", TestKubeconfigPath, err)
			} else {
				fmt.Printf("DEBUG: Removed test kubeconfig: %s\n", TestKubeconfigPath)
			}
			return ctx, nil
		},
	)
}

// setupForLocal configures the test environment for local development.
// It uses the e2e-framework's kind provider.
func setupForLocal(kindClusterName, kindNodeImage string) {
	kindCluster := kind.NewProvider().
		WithName(kindClusterName).
		WithOpts(kind.WithImage(kindNodeImage))

	testEnv.Setup(
		func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			// Create cluster
			kubeconfigPath, err := kindCluster.Create(ctx)
			if err != nil {
				return ctx, fmt.Errorf("failed to create kind cluster: %w", err)
			}

			// Update kubeconfig in context
			cfg.WithKubeconfigFile(kubeconfigPath)

			// SAFETY: Verify context switched to kind cluster
			client, err := cfg.NewClient()
			if err != nil {
				return ctx, fmt.Errorf("failed to create client: %w", err)
			}

			// Validate cluster has nodes
			var nodeList corev1.NodeList
			if err := client.Resources().List(ctx, &nodeList); err != nil {
				return ctx, fmt.Errorf("SAFETY CHECK FAILED: Cannot list nodes: %w", err)
			}
			if len(nodeList.Items) == 0 {
				return ctx, fmt.Errorf("SAFETY CHECK FAILED: Cluster has no nodes (unexpected for fresh kind cluster)")
			}

			fmt.Println("DEBUG: Loading controller image into kind cluster...")
			cmd := exec.CommandContext(ctx, "kind", "load", "docker-image", "haproxy-template-ic:test", "--name", kindClusterName)
			if output, err := cmd.CombinedOutput(); err != nil {
				return ctx, fmt.Errorf("failed to load controller image into kind cluster: %w\nOutput: %s", err, string(output))
			}
			fmt.Println("DEBUG: Controller image loaded successfully")

			// Install HAProxyTemplateConfig CRD
			fmt.Println("DEBUG: Installing HAProxyTemplateConfig CRD...")
			crdPath := "../../charts/haproxy-template-ic/crds/haproxy-template-ic.gitlab.io_haproxytemplateconfigs.yaml"
			cmd = exec.CommandContext(ctx, "kubectl", "apply", "--kubeconfig", kubeconfigPath, "-f", crdPath)
			if output, err := cmd.CombinedOutput(); err != nil {
				return ctx, fmt.Errorf("failed to install HAProxyTemplateConfig CRD: %w\nOutput: %s", err, string(output))
			}
			fmt.Println("DEBUG: HAProxyTemplateConfig CRD installed successfully")

			// Wait for CRD to be established
			cmd = exec.CommandContext(ctx, "kubectl", "wait", "--kubeconfig", kubeconfigPath,
				"--for=condition=Established",
				"crd/haproxytemplateconfigs.haproxy-template-ic.gitlab.io",
				"--timeout=60s")
			if output, err := cmd.CombinedOutput(); err != nil {
				return ctx, fmt.Errorf("failed to wait for CRD to be established: %w\nOutput: %s", err, string(output))
			}

			return ctx, nil
		},
	)

	// Finish: Cleanup resources
	testEnv.Finish(
		func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			if err := kindCluster.Destroy(ctx); err != nil {
				fmt.Printf("Warning: failed to destroy kind cluster: %v\n", err)
			}
			return ctx, nil
		},
		func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			if err := os.Remove(TestKubeconfigPath); err != nil && !os.IsNotExist(err) {
				fmt.Printf("Warning: failed to remove test kubeconfig %s: %v\n", TestKubeconfigPath, err)
			} else {
				fmt.Printf("DEBUG: Removed test kubeconfig: %s\n", TestKubeconfigPath)
			}
			return ctx, nil
		},
	)
}

// getKindNodeImage returns the Kind node image to use for acceptance tests.
// It checks the KIND_NODE_IMAGE environment variable and falls back to a default
// known-working version (v1.32.0) if not set.
//
// The default v1.32.0 is used instead of v1.32.1 because v1.32.1 has a bug
// with containerd snapshotter detection that causes image loading to fail.
// See: https://github.com/kubernetes-sigs/kind/issues/3871
func getKindNodeImage() string {
	if image := os.Getenv("KIND_NODE_IMAGE"); image != "" {
		return image
	}
	return "kindest/node:v1.32.0"
}
