//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rekby/fixenv"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/client"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/comparator"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/parser"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/parser/enterprise"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/parser/parserconfig"
)

const (
	// maxK8sNameLength is the maximum length for Kubernetes resource names (RFC 1123)
	maxK8sNameLength = 63
	// hashSuffixLength is the length of the hash suffix used to ensure uniqueness
	hashSuffixLength = 8
)

// generateSafeNamespaceName creates a Kubernetes-compliant namespace name that never exceeds 63 characters.
// It uses a combination of the test name (truncated if needed) and a unique hash suffix.
//
// Strategy:
// 1. Normalize test name (lowercase, replace "/" with "-")
// 2. If the name would exceed 63 chars with hash suffix, truncate intelligently
// 3. Add an 8-character hash suffix for uniqueness (derived from full test name + timestamp)
// 4. Ensure total length is always <= 63 characters
//
// Example outputs:
//   - "test-add-backend-a1b2c3d4" (short test name)
//   - "test-backend-add-http-response-rule-a1b2c3d4" (truncated long name)
func generateSafeNamespaceName(testName string) string {
	// Normalize test name: lowercase and replace "/" with "-"
	normalized := strings.ToLower(strings.ReplaceAll(testName, "/", "-"))

	// Generate unique hash from test name + timestamp for uniqueness
	// This ensures the same test run at different times gets different namespaces
	timestamp := fmt.Sprintf("%d", time.Now().UnixNano())
	hashInput := fmt.Sprintf("%s-%s", normalized, timestamp)
	hash := sha256.Sum256([]byte(hashInput))
	hashSuffix := hex.EncodeToString(hash[:])[:hashSuffixLength]

	maxBaseLength := maxK8sNameLength - 5 - 1 - hashSuffixLength

	// Truncate normalized name if needed
	baseName := normalized
	if len(baseName) > maxBaseLength {
		baseName = baseName[:maxBaseLength]
	}

	// Construct final name
	finalName := fmt.Sprintf("test-%s-%s", baseName, hashSuffix)

	// Sanity check: ensure we never exceed the limit
	if len(finalName) > maxK8sNameLength {
		panic(fmt.Sprintf("BUG: generated namespace name '%s' exceeds %d characters (length: %d)",
			finalName, maxK8sNameLength, len(finalName)))
	}

	return finalName
}

// SharedCluster provides a package-scoped Kind cluster shared across all tests
// This fixture runs only once per test package and is kept by default for faster test iterations
// The cluster is automatically reused if it already exists
// Set KEEP_CLUSTER=false to force cleanup after tests
func SharedCluster(env fixenv.Env) *KindCluster {
	return fixenv.CacheResult(env, func() (*fixenv.GenericResult[*KindCluster], error) {
		cluster, err := SetupKindCluster(&KindClusterConfig{
			Name: "haproxy-test",
		})
		if err != nil {
			return nil, fmt.Errorf("failed to setup kind cluster: %w", err)
		}

		// Return with conditional cleanup function
		return fixenv.NewGenericResultWithCleanup(cluster, func() {
			keepCluster := ShouldKeepCluster()
			if keepCluster == "true" {
				fmt.Printf("\n🔒 Keeping Kind cluster '%s' (KEEP_CLUSTER=true)\n", cluster.Name)
				fmt.Printf("🧹 To manually clean up: kind delete cluster --name=%s\n", cluster.Name)
				return
			}
			// Default: always clean up
			_ = cluster.Teardown()
		}), nil
	}, fixenv.CacheOptions{Scope: fixenv.ScopePackage})
}

// TestNamespace provides a test-scoped namespace (fresh for each test)
// Automatically depends on SharedCluster fixture
// Namespaces are kept by default for faster test iterations
// Set KEEP_CLUSTER=false to force cleanup after tests
func TestNamespace(env fixenv.Env) *Namespace {
	// Automatic dependency: request SharedCluster fixture
	cluster := SharedCluster(env)

	return fixenv.CacheResult(env, func() (*fixenv.GenericResult[*Namespace], error) {
		// Generate unique namespace name for this test
		// Uses generateSafeNamespaceName to ensure Kubernetes compliance (max 63 chars)
		name := generateSafeNamespaceName(env.T().Name())

		ns, err := cluster.CreateNamespace(name)
		if err != nil {
			return nil, fmt.Errorf("failed to create namespace: %w", err)
		}

		// Return with conditional cleanup function
		return fixenv.NewGenericResultWithCleanup(ns, func() {
			keepCluster := ShouldKeepCluster()
			if keepCluster == "true" {
				fmt.Printf("🔒 Keeping namespace '%s' (KEEP_CLUSTER=true)\n", ns.Name)
				return
			}
			// Default: always clean up
			_ = ns.Delete()
		}), nil
	})
}

// TestHAProxy provides a test-scoped HAProxy deployment
// Automatically depends on TestNamespace fixture (which depends on SharedCluster)
// HAProxy instances are kept by default for faster test iterations
// Set KEEP_CLUSTER=false to force cleanup after tests
func TestHAProxy(env fixenv.Env) *HAProxyInstance {
	// Automatic dependency chain: TestNamespace -> SharedCluster
	ns := TestNamespace(env)

	return fixenv.CacheResult(env, func() (*fixenv.GenericResult[*HAProxyInstance], error) {
		haproxy, err := DeployHAProxy(ns, DefaultHAProxyConfig())
		if err != nil {
			return nil, fmt.Errorf("failed to deploy haproxy: %w", err)
		}

		// Register cleanup to dump container logs on test failure
		// This runs after test completion, so logs include all activity
		// Type assert to *testing.T to access Failed() method
		if t, ok := env.T().(*testing.T); ok {
			t.Cleanup(func() {
				haproxy.DumpLogsOnFailure(t)
			})
		}

		return fixenv.NewGenericResultWithCleanup(haproxy, func() {
			keepCluster := ShouldKeepCluster()
			if keepCluster == "true" {
				fmt.Printf("🔒 Keeping HAProxy instance '%s' in namespace '%s' (KEEP_CLUSTER=true)\n", haproxy.Name, haproxy.Namespace)
				return
			}
			// Default: always clean up
			_ = haproxy.Delete()
		}), nil
	})
}

// TestDataplaneClient provides a configured Dataplane API client
// Automatically depends on TestHAProxy fixture
func TestDataplaneClient(env fixenv.Env) *client.DataplaneClient {
	// Automatic dependency chain: TestHAProxy -> TestNamespace -> SharedCluster
	haproxy := TestHAProxy(env)

	return fixenv.CacheResult(env, func() (*fixenv.GenericResult[*client.DataplaneClient], error) {
		endpoint := haproxy.GetDataplaneEndpoint()

		var dataplaneClient *client.DataplaneClient
		var lastErr error

		// Retry client creation up to 3 times with exponential backoff
		// This handles transient API failures during version detection
		for attempt := 1; attempt <= 3; attempt++ {
			dataplaneClient, lastErr = client.New(context.Background(), &client.Config{
				BaseURL:  endpoint.URL,
				Username: endpoint.Username,
				Password: endpoint.Password,
			})
			if lastErr == nil {
				return fixenv.NewGenericResult(dataplaneClient), nil
			}

			if attempt < 3 {
				backoff := time.Duration(attempt) * time.Second
				fmt.Printf("Dataplane client creation attempt %d failed: %v (retrying in %v)\n", attempt, lastErr, backoff)
				time.Sleep(backoff)
			}
		}

		return nil, fmt.Errorf("failed to create dataplane client after 3 attempts: %w", lastErr)
	})
}

// ConfigParser defines the interface for HAProxy configuration parsing.
// Both CE (parser.Parser) and EE (enterprise.Parser) parsers implement this interface.
type ConfigParser interface {
	ParseFromString(config string) (*parserconfig.StructuredConfig, error)
}

// TestParser provides a configuration parser instance.
// It automatically selects the appropriate parser based on whether the test HAProxy
// is Enterprise or Community edition.
func TestParser(env fixenv.Env) ConfigParser {
	// Depend on client to check for EE status
	dpClient := TestDataplaneClient(env)

	return fixenv.CacheResult(env, func() (*fixenv.GenericResult[ConfigParser], error) {
		var p ConfigParser
		var err error

		// Use EE parser when connected to HAProxy Enterprise
		if dpClient.Clientset().IsEnterprise() {
			fmt.Println("Using Enterprise Edition parser for test HAProxy")
			p, err = enterprise.NewParser()
			if err != nil {
				return nil, fmt.Errorf("failed to create EE parser: %w", err)
			}
		} else {
			p, err = parser.New()
			if err != nil {
				return nil, fmt.Errorf("failed to create parser: %w", err)
			}
		}
		return fixenv.NewGenericResult(p), nil
	})
}

// TestComparator provides a comparator instance
func TestComparator(env fixenv.Env) *comparator.Comparator {
	return fixenv.CacheResult(env, func() (*fixenv.GenericResult[*comparator.Comparator], error) {
		return fixenv.NewGenericResult(comparator.New()), nil
	})
}

// TestDataplaneHighLevelClient provides a high-level dataplane.Client
// This uses the public Sync API that other components will use
// Automatically depends on TestHAProxy fixture
func TestDataplaneHighLevelClient(env fixenv.Env) *dataplane.Client {
	// Automatic dependency chain: TestHAProxy -> TestNamespace -> SharedCluster
	haproxy := TestHAProxy(env)

	return fixenv.CacheResult(env, func() (*fixenv.GenericResult[*dataplane.Client], error) {
		endpoint := haproxy.GetDataplaneEndpoint()

		dpEndpoint := dataplane.Endpoint{
			URL:      endpoint.URL,
			Username: endpoint.Username,
			Password: endpoint.Password,
		}

		var dpClient *dataplane.Client
		var lastErr error

		// Retry client creation up to 3 times with exponential backoff
		// This handles transient API failures during version detection
		for attempt := 1; attempt <= 3; attempt++ {
			dpClient, lastErr = dataplane.NewClient(context.Background(), &dpEndpoint)
			if lastErr == nil {
				return fixenv.NewGenericResult(dpClient), nil
			}

			if attempt < 3 {
				backoff := time.Duration(attempt) * time.Second
				fmt.Printf("High-level dataplane client creation attempt %d failed: %v (retrying in %v)\n", attempt, lastErr, backoff)
				time.Sleep(backoff)
			}
		}

		return nil, fmt.Errorf("failed to create dataplane client after 3 attempts: %w", lastErr)
	})
}

// =============================================================================
// Enterprise Feature Skip Helpers
// =============================================================================
//
// These helpers skip tests when HAProxy Enterprise features are not available.
// Tests using these helpers will gracefully skip on community edition.
//
// Usage:
//
//	func TestWAFRulesets(t *testing.T) {
//	    env := fixenv.New(t)
//	    skipIfWAFNotSupported(t, env)
//	    // Test proceeds only on enterprise edition
//	}

// skipIfNotEnterprise skips the test if HAProxy Enterprise is not detected.
// This is the general-purpose skip for any enterprise-only test.
func skipIfNotEnterprise(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsWAF {
		t.Skipf("Skipping test: requires HAProxy Enterprise (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
}

// skipIfWAFNotSupported skips the test if WAF features are not available.
// WAF is available in all HAProxy Enterprise versions.
func skipIfWAFNotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsWAF {
		t.Skipf("Skipping test: WAF requires HAProxy Enterprise (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
}

// skipIfWAFGlobalNotSupported skips the test if WAF global config is not available.
// WAF global configuration requires HAProxy Enterprise v3.2+.
func skipIfWAFGlobalNotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsWAFGlobal {
		t.Skipf("Skipping test: WAF global config requires HAProxy Enterprise v3.2+ (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
}

// skipIfWAFProfilesNotSupported skips the test if WAF profiles are not available.
// WAF profiles require HAProxy Enterprise v3.2+.
func skipIfWAFProfilesNotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsWAFProfiles {
		t.Skipf("Skipping test: WAF profiles require HAProxy Enterprise v3.2+ (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
}

// skipIfKeepalivedNotSupported skips the test if Keepalived/VRRP features are not available.
// Keepalived is available in all HAProxy Enterprise versions.
func skipIfKeepalivedNotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsKeepalived {
		t.Skipf("Skipping test: Keepalived requires HAProxy Enterprise (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
}

// skipIfUDPLBNotSupported skips the test if UDP load balancing is not available.
// UDP load balancing is available in all HAProxy Enterprise versions.
func skipIfUDPLBNotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsUDPLoadBalancing {
		t.Skipf("Skipping test: UDP load balancing requires HAProxy Enterprise (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
}

// skipIfUDPLBACLsNotSupported skips the test if UDP LB ACLs are not available.
// UDP LB ACLs require HAProxy Enterprise v3.2+.
func skipIfUDPLBACLsNotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsUDPLBACLs {
		t.Skipf("Skipping test: UDP LB ACLs require HAProxy Enterprise v3.2+ (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
}

// skipIfBotManagementNotSupported skips the test if bot management is not available.
// Bot management is available in all HAProxy Enterprise versions.
func skipIfBotManagementNotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsBotManagement {
		t.Skipf("Skipping test: Bot management requires HAProxy Enterprise (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
}

// skipIfBotManagementSyncNotSupported skips sync tests if bot management data file is not available.
// Bot management sync tests require HAPEE_KEY environment variable to download the data file
// from haproxy.com. The data file is downloaded during HAProxy deployment when HAPEE_KEY is set.
func skipIfBotManagementSyncNotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsBotManagement {
		t.Skipf("Skipping test: Bot management requires HAProxy Enterprise (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
	if os.Getenv("HAPEE_KEY") == "" {
		t.Skip("Skipping test: Bot management sync tests require HAPEE_KEY environment variable " +
			"to download the data file from haproxy.com")
	}
}

// skipIfCaptchaNotSupported skips the test if captcha features are not available.
// Captcha is available in all HAProxy Enterprise versions but uses a separate module
// (hapee-lb-captcha.so) that is NOT included in standard HAPEE container images.
// The module is installed automatically when HAPEE_KEY is set (used to authenticate
// with HAProxy Enterprise apt repository).
func skipIfCaptchaNotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsBotManagement {
		t.Skipf("Skipping test: Captcha requires HAProxy Enterprise (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
	if os.Getenv("HAPEE_KEY") == "" {
		t.Skip("Skipping test: Captcha tests require HAPEE_KEY environment variable " +
			"to install the hapee-lb-captcha.so module from HAProxy Enterprise repository")
	}
}

// skipIfGitIntegrationNotSupported skips the test if Git integration is not available.
// Git integration is available in all HAProxy Enterprise versions.
func skipIfGitIntegrationNotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsGitIntegration {
		t.Skipf("Skipping test: Git integration requires HAProxy Enterprise (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
}

// skipIfAdvancedLoggingNotSupported skips the test if advanced logging is not available.
// Advanced logging is available in all HAProxy Enterprise versions.
func skipIfAdvancedLoggingNotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsAdvancedLogging {
		t.Skipf("Skipping test: Advanced logging requires HAProxy Enterprise (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
}

// skipIfDynamicUpdateNotSupported skips the test if dynamic updates are not available.
// Dynamic updates are available in all HAProxy Enterprise versions.
func skipIfDynamicUpdateNotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsDynamicUpdate {
		t.Skipf("Skipping test: Dynamic updates require HAProxy Enterprise (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
}

// skipIfALOHANotSupported skips the test if ALOHA features are not available.
// ALOHA is available in all HAProxy Enterprise versions.
func skipIfALOHANotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsALOHA {
		t.Skipf("Skipping test: ALOHA requires HAProxy Enterprise (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
}

// skipIfPingNotSupported skips the test if the Ping endpoint is not available.
// Ping endpoint requires HAProxy Enterprise v3.2+.
func skipIfPingNotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsPing {
		t.Skipf("Skipping test: Ping endpoint requires HAProxy Enterprise v3.2+ (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
}

// skipIfLogProfilesNotSupported skips the test if log profiles are not supported.
// Log profiles require DataPlane API v3.1+.
func skipIfLogProfilesNotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsLogProfiles {
		t.Skipf("Skipping test: log profiles require DataPlane API v3.1+ (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
}

// skipIfTracesNotSupported skips the test if traces section is not supported.
// Traces require DataPlane API v3.1+.
func skipIfTracesNotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsTraces {
		t.Skipf("Skipping test: traces section requires DataPlane API v3.1+ (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
}

// skipIfQUICInitialRulesNotSupported skips the test if QUIC initial rules are not supported.
// QUIC initial rules require DataPlane API v3.1+.
func skipIfQUICInitialRulesNotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsQUICInitialRules {
		t.Skipf("Skipping test: QUIC initial rules require DataPlane API v3.1+ (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
}

// skipIfAcmeProvidersNotSupported skips the test if ACME providers are not supported.
// ACME providers require DataPlane API v3.2+.
func skipIfAcmeProvidersNotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsAcmeProviders {
		t.Skipf("Skipping test: ACME providers require DataPlane API v3.2+ (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
}

// skipIfConfigMetadataNotSupported skips the test if config model metadata is not supported.
// Metadata fields on configuration models (ACL, Server, etc.) require DataPlane API v3.2+.
// On v3.0/v3.1, the Metadata field is not present in the API schema, so comments are lost
// during round-trip through the API, causing false positive updates in idempotency tests.
func skipIfConfigMetadataNotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsConfigMetadata {
		t.Skipf("Skipping test: config model metadata requires DataPlane API v3.2+ (detected version: %s)",
			dataplaneClient.DetectedVersion())
	}
}

// =============================================================================
// Transaction Helper
// =============================================================================

// StartTestTransaction is a convenience function that creates a transaction
// by first getting the current configuration version.
// This is useful for tests that need to make transactional operations.
func StartTestTransaction(ctx context.Context, c *client.DataplaneClient) (*client.Transaction, error) {
	version, err := c.GetVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get version: %w", err)
	}
	return c.CreateTransaction(ctx, version)
}
