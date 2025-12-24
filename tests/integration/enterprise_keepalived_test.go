//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/rekby/fixenv"
	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/client/enterprise"
)

// isKeepalivedNotInstalled checks if the error indicates Keepalived is not installed.
// The DataPlane API returns 501 when Keepalived is not installed/configured.
func isKeepalivedNotInstalled(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "501")
}

// TestKeepalivedVRRPInstances tests VRRP instance operations.
// VRRP is available in all HAProxy Enterprise versions, but Keepalived must be installed.
func TestKeepalivedVRRPInstances(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfKeepalivedNotSupported(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	keepalivedOps := enterprise.NewKeepalivedOperations(dataplaneClient)

	t.Run("list-vrrp-instances", func(t *testing.T) {
		instances, err := keepalivedOps.GetAllVRRPInstances(ctx)
		if isKeepalivedNotInstalled(err) {
			t.Skip("Keepalived is not installed on this system")
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// VRRP instances may be empty on fresh install, that's OK
		assert.NotNil(t, instances)
	})
}

// TestKeepalivedVRRPSyncGroups tests VRRP sync group operations.
// VRRP sync groups are available in all HAProxy Enterprise versions, but Keepalived must be installed.
func TestKeepalivedVRRPSyncGroups(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfKeepalivedNotSupported(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	keepalivedOps := enterprise.NewKeepalivedOperations(dataplaneClient)

	t.Run("list-sync-groups", func(t *testing.T) {
		groups, err := keepalivedOps.GetAllVRRPSyncGroups(ctx)
		if isKeepalivedNotInstalled(err) {
			t.Skip("Keepalived is not installed on this system")
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Sync groups may be empty on fresh install, that's OK
		assert.NotNil(t, groups)
	})
}

// TestKeepalivedVRRPScripts tests VRRP tracking script operations.
// VRRP scripts are available in all HAProxy Enterprise versions, but Keepalived must be installed.
func TestKeepalivedVRRPScripts(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfKeepalivedNotSupported(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	keepalivedOps := enterprise.NewKeepalivedOperations(dataplaneClient)

	t.Run("list-vrrp-scripts", func(t *testing.T) {
		scripts, err := keepalivedOps.GetAllVRRPScripts(ctx)
		if isKeepalivedNotInstalled(err) {
			t.Skip("Keepalived is not installed on this system")
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Scripts may be empty on fresh install, that's OK
		assert.NotNil(t, scripts)
	})
}

// TestKeepalivedTransactions tests the Keepalived-specific transaction system.
// Keepalived has its own transaction system separate from HAProxy configuration.
func TestKeepalivedTransactions(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfKeepalivedNotSupported(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	keepalivedOps := enterprise.NewKeepalivedOperations(dataplaneClient)

	t.Run("transaction-lifecycle", func(t *testing.T) {
		// Start a Keepalived transaction
		txID, err := keepalivedOps.StartTransaction(ctx)
		if isKeepalivedNotInstalled(err) {
			t.Skip("Keepalived is not installed on this system")
		}
		if err != nil {
			t.Fatalf("unexpected error starting transaction: %v", err)
		}
		assert.NotEmpty(t, txID)

		// Get the transaction to verify it exists
		tx, err := keepalivedOps.GetTransaction(ctx, txID)
		if err != nil {
			t.Fatalf("unexpected error getting transaction: %v", err)
		}
		assert.NotNil(t, tx)

		// Delete (abort) the transaction to clean up
		err = keepalivedOps.DeleteTransaction(ctx, txID)
		if err != nil {
			t.Fatalf("unexpected error deleting transaction: %v", err)
		}
	})
}
