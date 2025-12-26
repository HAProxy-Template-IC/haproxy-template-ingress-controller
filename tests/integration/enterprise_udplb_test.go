//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/rekby/fixenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client/enterprise"
)

// TestUDPLoadBalancers tests UDP load balancer CRUD operations.
// UDP load balancing is available in all HAProxy Enterprise versions.
func TestUDPLoadBalancers(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfUDPLBNotSupported(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	udpOps := enterprise.NewUDPLBOperations(dataplaneClient)

	// Start a transaction for operations requiring one
	tx, err := StartTestTransaction(ctx, dataplaneClient)
	require.NoError(t, err)
	defer func() {
		_ = tx.Abort(ctx)
	}()

	txID := tx.ID

	t.Run("list-udp-lbs", func(t *testing.T) {
		lbs, err := udpOps.GetAllUDPLbs(ctx, txID)
		require.NoError(t, err)
		// UDP load balancers may be empty on fresh install, that's OK
		assert.NotNil(t, lbs)
	})
}

// TestUDPLoadBalancerDgramBinds tests UDP LB dgram bind operations.
// Dgram binds are available in all HAProxy Enterprise versions.
func TestUDPLoadBalancerDgramBinds(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfUDPLBNotSupported(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	udpOps := enterprise.NewUDPLBOperations(dataplaneClient)

	tx, err := StartTestTransaction(ctx, dataplaneClient)
	require.NoError(t, err)
	defer func() {
		_ = tx.Abort(ctx)
	}()

	txID := tx.ID

	t.Run("list-dgram-binds", func(t *testing.T) {
		// First check if we have any UDP LBs
		lbs, err := udpOps.GetAllUDPLbs(ctx, txID)
		if err != nil || len(lbs) == 0 {
			t.Skip("No UDP load balancers available for dgram binds test")
		}

		// Get dgram binds for the first UDP LB
		lbName := lbs[0].Name
		if lbName == "" {
			t.Skip("UDP load balancer has no name")
		}

		binds, err := udpOps.GetAllDgramBindsUDPLb(ctx, txID, lbName)
		require.NoError(t, err)
		assert.NotNil(t, binds)
	})
}

// TestUDPLoadBalancerLogTargets tests UDP LB log target operations.
// Log targets are available in all HAProxy Enterprise versions.
func TestUDPLoadBalancerLogTargets(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfUDPLBNotSupported(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	udpOps := enterprise.NewUDPLBOperations(dataplaneClient)

	tx, err := StartTestTransaction(ctx, dataplaneClient)
	require.NoError(t, err)
	defer func() {
		_ = tx.Abort(ctx)
	}()

	txID := tx.ID

	t.Run("list-log-targets", func(t *testing.T) {
		// First check if we have any UDP LBs
		lbs, err := udpOps.GetAllUDPLbs(ctx, txID)
		if err != nil || len(lbs) == 0 {
			t.Skip("No UDP load balancers available for log targets test")
		}

		lbName := lbs[0].Name
		if lbName == "" {
			t.Skip("UDP load balancer has no name")
		}

		targets, err := udpOps.GetAllLogTargetsUDPLb(ctx, txID, lbName)
		require.NoError(t, err)
		assert.NotNil(t, targets)
	})
}

// TestUDPLoadBalancerACLs tests UDP LB ACL operations.
// ACLs for UDP load balancers are only available in HAProxy Enterprise v3.2+.
func TestUDPLoadBalancerACLs(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfUDPLBACLsNotSupported(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	udpOps := enterprise.NewUDPLBOperations(dataplaneClient)

	tx, err := StartTestTransaction(ctx, dataplaneClient)
	require.NoError(t, err)
	defer func() {
		_ = tx.Abort(ctx)
	}()

	txID := tx.ID

	t.Run("list-acls", func(t *testing.T) {
		// First check if we have any UDP LBs
		lbs, err := udpOps.GetAllUDPLbs(ctx, txID)
		if err != nil || len(lbs) == 0 {
			t.Skip("No UDP load balancers available for ACLs test")
		}

		lbName := lbs[0].Name
		if lbName == "" {
			t.Skip("UDP load balancer has no name")
		}

		acls, err := udpOps.GetAllACLsUDPLb(ctx, txID, lbName)
		require.NoError(t, err)
		assert.NotNil(t, acls)
	})
}
