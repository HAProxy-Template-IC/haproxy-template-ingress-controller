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

//go:build acceptance

package acceptance

import (
	"os"
	"testing"

	"sigs.k8s.io/e2e-framework/pkg/types"
)

// TestAllAcceptanceParallel runs all acceptance tests in parallel using the e2e-framework's
// TestInParallel method. Each test runs in its own isolated namespace, sharing the same
// Kind cluster for efficiency.
//
// Usage:
//
//	go test -tags=acceptance -v -timeout 30m -run TestAllAcceptanceParallel ./tests/acceptance/...
//
// The -parallel flag can be used to control the maximum number of concurrent tests:
//
//	go test -tags=acceptance -v -timeout 30m -parallel 4 -run TestAllAcceptanceParallel ./tests/acceptance/...
//
// Benefits:
// - Faster overall test execution (tests run concurrently)
// - Shared Kind cluster reduces setup time
// - Each test has namespace isolation (no cross-test interference)
//
// Notes:
// - Each test creates and cleans up its own namespace
// - Cluster-scoped resources (ClusterRole, ClusterRoleBinding) use namespace-prefixed names
// - Tests are independent and should not rely on any shared state
func TestAllAcceptanceParallel(t *testing.T) {
	// Skip when running in CI sharding mode - individual tests run via matrix
	if os.Getenv("SKIP_PARALLEL_RUNNER") == "true" {
		t.Skip("Skipping parallel runner in CI sharding mode - tests run individually via matrix")
	}

	// Collect all feature builders
	features := []types.Feature{
		// Error scenarios tests
		buildInvalidHAProxyConfigFeature(),
		buildCredentialsMissingFeature(),
		buildControllerCrashRecoveryFeature(),
		buildRapidConfigUpdatesFeature(),
		buildGracefulShutdownFeature(),
		buildDataplaneUnreachableFeature(),
		buildLeadershipDuringReconciliationFeature(),
		buildWatchReconnectionFeature(),
		buildTransactionConflictFeature(),
		buildPartialDeploymentFailureFeature(),

		// Leader election tests
		buildLeaderElectionTwoReplicasFeature(),
		buildLeaderElectionFailoverFeature(),
		buildLeaderElectionDisabledModeFeature(),

		// HTTP store tests
		buildHTTPStoreValidUpdateFeature(),
		buildHTTPStoreInvalidUpdateFeature(),
		buildHTTPStoreFailoverFeature(),

		// Metrics tests
		buildMetricsFeature(),
	}

	// Run all features in parallel
	testEnv.TestInParallel(t, features...)
}
