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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rekby/fixenv"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
	"gitlab.com/haproxy-haptic/haptic/tests/kindutil"
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

// AgentImage builds the pod image once per run and loads it into the cluster.
// Package-scoped: every test's pod runs the same binary, and the build is the
// slowest step of the suite.
func AgentImage(env fixenv.Env) string {
	cluster := SharedCluster(env)

	return fixenv.CacheResult(env, func() (*fixenv.GenericResult[string], error) {
		tag, err := buildAndLoadAgentImage(cluster)
		if err != nil {
			return nil, err
		}
		fmt.Printf("✓ Agent image '%s' loaded into Kind cluster\n", tag)
		return fixenv.NewGenericResultWithCleanup(tag, func() {
			kindutil.RemoveImage(tag)
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

// TestHAProxy provides a test-scoped HAProxy pod: the HAProxy container plus
// its agent, the topology the chart deploys.
// Automatically depends on TestNamespace fixture (which depends on SharedCluster)
// HAProxy instances are kept by default for faster test iterations
// Set KEEP_CLUSTER=false to force cleanup after tests
func TestHAProxy(env fixenv.Env) *HAProxyInstance {
	// Automatic dependency chain: TestNamespace -> SharedCluster
	ns := TestNamespace(env)
	image := AgentImage(env)

	return fixenv.CacheResult(env, func() (*fixenv.GenericResult[*HAProxyInstance], error) {
		haproxy, err := DeployHAProxy(ns, DefaultHAProxyConfig(image))
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

// TestAgentClient provides the controller's end of the wire contract, pointed
// at the test pod's agent through the forwarded port.
// Automatically depends on TestHAProxy fixture
func TestAgentClient(env fixenv.Env) *client.Client {
	// Automatic dependency chain: TestHAProxy -> TestNamespace -> SharedCluster
	haproxy := TestHAProxy(env)

	return fixenv.CacheResult(env, func() (*fixenv.GenericResult[*client.Client], error) {
		agentClient, err := client.New(&client.Config{
			BaseURL:            haproxy.AgentURL(),
			Username:           haproxy.AgentUser,
			Password:           haproxy.AgentPass,
			Timeout:            30 * time.Second,
			PerPodApplyTimeout: 2 * time.Minute,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create agent client: %w", err)
		}
		return fixenv.NewGenericResultWithCleanup(agentClient, agentClient.Close), nil
	})
}

// skipBelowHAProxy skips a test whose subject needs a later HAProxy release
// than the one under test. The version comes from HAPROXY_VERSION, which also
// selects the image, so the gate and the pod can never disagree.
func skipBelowHAProxy(t *testing.T, bound string) {
	t.Helper()
	if !haproxyAtLeast(bound) {
		t.Skipf("HAProxy %s is under test; this needs %s or later", HAProxyVersion(), bound)
	}
}

// haproxyAtLeast compares the version under test with a "major.minor" bound.
func haproxyAtLeast(bound string) bool {
	haveMajor, haveMinor := majorMinor(HAProxyVersion())
	wantMajor, wantMinor := majorMinor(bound)
	if haveMajor != wantMajor {
		return haveMajor > wantMajor
	}
	return haveMinor >= wantMinor
}

func majorMinor(version string) (major, minor int) {
	fields := strings.SplitN(strings.TrimPrefix(version, "v"), ".", 3)
	major, _ = strconv.Atoi(leadingDigits(fields[0]))
	if len(fields) > 1 {
		minor, _ = strconv.Atoi(leadingDigits(fields[1]))
	}
	return major, minor
}

func leadingDigits(s string) string {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return s[:i]
		}
	}
	return s
}
