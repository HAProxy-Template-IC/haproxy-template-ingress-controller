package client

import "fmt"

// ClientError represents errors that occur during Kubernetes client operations.
type ClientError struct {
	Operation string
	Cause     error
}

func (e *ClientError) Error() string {
	return fmt.Sprintf("k8s client error during %s: %v", e.Operation, e.Cause)
}

func (e *ClientError) Unwrap() error {
	return e.Cause
}

// NamespaceDiscoveryError represents errors that occur during namespace discovery.
type NamespaceDiscoveryError struct {
	Path  string
	Cause error
}

func (e *NamespaceDiscoveryError) Error() string {
	return fmt.Sprintf("failed to discover namespace from %s: %v", e.Path, e.Cause)
}

func (e *NamespaceDiscoveryError) Unwrap() error {
	return e.Cause
}
