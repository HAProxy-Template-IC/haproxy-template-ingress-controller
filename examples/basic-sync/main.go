// Basic example demonstrating the dataplane Client API
//
// This example shows how to:
//   - Create a dataplane Client
//   - Sync a simple HAProxy configuration
//   - Handle errors properly
//   - Inspect sync results
//
// Prerequisites:
//   - HAProxy running with Dataplane API enabled
//   - Dataplane API accessible at the configured endpoint
//
// Configuration:
//
//	Set these environment variables or modify the code:
//	- HAPROXY_URL: Dataplane API endpoint (default: http://localhost:5555/v2)
//	- HAPROXY_USER: Basic auth username (default: admin)
//	- HAPROXY_PASS: Basic auth password (default: admin)
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("Error: %v", err)
	}
}

func run() error {
	// Configure connection to HAProxy Dataplane API
	endpoint := dataplane.Endpoint{
		URL:      getEnv("HAPROXY_URL", "http://localhost:5555/v2"),
		Username: getEnv("HAPROXY_USER", "admin"),
		Password: getEnv("HAPROXY_PASS", "admin"),
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create client - this establishes connection and should be reused
	fmt.Println("Creating dataplane client...")
	client, err := dataplane.NewClient(ctx, &endpoint)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	defer client.Close()

	fmt.Printf("Connected to HAProxy at %s\n\n", endpoint.URL)

	// Define desired HAProxy configuration
	desiredConfig := `
global
    daemon
    maxconn 4096

defaults
    mode http
    timeout client 30s
    timeout server 30s
    timeout connect 5s
    timeout http-request 10s

frontend http-in
    bind *:80
    default_backend web-servers

backend web-servers
    balance roundrobin
    server web1 192.168.1.10:80 check inter 2s
    server web2 192.168.1.11:80 check inter 2s
`

	// Optional: Configure sync behavior
	opts := &dataplane.SyncOptions{
		MaxRetries:      3,               // Retry version conflicts up to 3 times
		Timeout:         2 * time.Minute, // Overall operation timeout
		ContinueOnError: false,           // Stop on first error
		FallbackToRaw:   true,            // Auto-fallback to raw config push if needed
	}

	// Sync the configuration
	fmt.Println("Syncing HAProxy configuration...")
	result, err := client.Sync(ctx, desiredConfig, nil, opts)
	if err != nil {
		return handleSyncError(err)
	}

	// Display sync results
	displaySyncResults(result)

	// Example: Preview changes without applying (dry run)
	if err := runDryRunExample(ctx, client, desiredConfig); err != nil {
		log.Printf("Dry run failed: %v", err)
	}

	fmt.Println("\nExample completed successfully!")
	return nil
}

// handleSyncError processes and returns sync errors with detailed information.
func handleSyncError(err error) error {
	var syncErr *dataplane.SyncError
	if errors.As(err, &syncErr) {
		log.Printf("Sync failed at stage '%s': %s\n", syncErr.Stage, syncErr.Message)
		if len(syncErr.Hints) > 0 {
			log.Println("\nTroubleshooting hints:")
			for _, hint := range syncErr.Hints {
				log.Printf("  - %s\n", hint)
			}
		}
	}
	return fmt.Errorf("sync failed: %w", err)
}

// displaySyncResults prints the results of a successful sync operation.
func displaySyncResults(result *dataplane.SyncResult) {
	fmt.Println("\nSync completed successfully!")
	fmt.Printf("Duration: %v\n", result.Duration)
	fmt.Printf("Operations applied: %d\n", len(result.AppliedOperations))

	if result.Retries > 0 {
		fmt.Printf("Retries (version conflicts): %d\n", result.Retries)
	}

	if result.UsedRawPush() {
		fmt.Println("⚠ Warning: Used raw config push (fine-grained sync not used)")
	}

	if result.ReloadTriggered {
		fmt.Printf("HAProxy reloaded: %s\n", result.ReloadID)
	} else {
		fmt.Println("No HAProxy reload required (runtime API used)")
	}

	// Display applied operations
	if len(result.AppliedOperations) > 0 {
		fmt.Println("\nApplied operations:")
		for i, op := range result.AppliedOperations {
			fmt.Printf("  %d. [%s] %s: %s\n", i+1, op.Type, op.Resource, op.Description)
		}
	}
}

// runDryRunExample demonstrates the dry-run functionality.
func runDryRunExample(ctx context.Context, client *dataplane.Client, desiredConfig string) error {
	fmt.Println("\n--- Dry Run Example ---")
	modifiedConfig := desiredConfig + "\n    server web3 192.168.1.12:80 check inter 2s\n"

	diff, err := client.DryRun(ctx, modifiedConfig)
	if err != nil {
		return err
	}

	if diff.HasChanges {
		fmt.Printf("Would apply %d operations:\n", len(diff.PlannedOperations))
		for i, op := range diff.PlannedOperations {
			fmt.Printf("  %d. [%s] %s: %s\n", i+1, op.Type, op.Resource, op.Description)
		}
	} else {
		fmt.Println("No changes needed")
	}

	return nil
}

// getEnv retrieves an environment variable with a fallback default value.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
