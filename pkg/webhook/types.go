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

// Package webhook provides a pure library for Kubernetes admission webhooks.
//
// This package implements an HTTPS webhook server with flexible validation handlers,
// without dependencies on other project packages. It can be used in any Kubernetes
// controller project.
//
// The package provides:
//   - Generic webhook server with configurable validation
//   - AdmissionReview v1 request/response handling
//   - ValidationContext with full admission request details
//   - Thread-safe concurrent request handling
//
// External dependencies required (not provided by this library):
//   - TLS certificates (from cert-manager, Kubernetes Secret, or Helm)
//   - ValidatingWebhookConfiguration (via Helm chart or kubectl apply)
//
// Example usage:
//
//	// Load certificates from external source (Kubernetes Secret)
//	secret, err := client.CoreV1().Secrets("default").Get(ctx, "webhook-certs", metav1.GetOptions{})
//	certPEM := secret.Data["tls.crt"]
//	keyPEM := secret.Data["tls.key"]
//
//	// Create webhook server
//	server := webhook.NewServer(&webhook.ServerConfig{
//	    Port:     9443,
//	    CertPEM:  certPEM,
//	    KeyPEM:   keyPEM,
//	})
//
//	// Register validator with full context
//	server.RegisterValidator("networking.k8s.io/v1.Ingress", func(ctx *webhook.ValidationContext) (bool, string, error) {
//	    // Validation logic with access to operation type and old/new objects
//	    if ctx.Operation == "UPDATE" && ctx.OldObject != nil {
//	        // Implement immutability checks
//	    }
//	    return true, "", nil
//	})
//
//	// Start server
//	server.Start(ctx)
package webhook

import (
	"time"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// ValidationContext provides the complete context for validating a Kubernetes resource.
//
// This includes the resource object, operation type, and related metadata from
// the AdmissionRequest. This allows validators to make informed decisions based
// on the full context of the admission request.
type ValidationContext struct {
	// Object is the resource object being validated (new version).
	// For CREATE: the object being created
	// For UPDATE: the new version of the object
	// For DELETE: the object being deleted
	// Stored as unstructured.Unstructured (same type as resource stores use).
	Object *unstructured.Unstructured

	// OldObject is the existing version of the resource (for UPDATE/DELETE operations).
	// For CREATE: nil
	// For UPDATE: the current version in the cluster
	// For DELETE: the object being deleted (same as Object)
	// Stored as unstructured.Unstructured (same type as resource stores use).
	OldObject *unstructured.Unstructured

	// Operation indicates the admission operation type.
	// Values: "CREATE", "UPDATE", "DELETE", "CONNECT"
	Operation string

	// Namespace is the namespace of the resource (empty for cluster-scoped resources).
	Namespace string

	// Name is the name of the resource.
	// May be empty for CREATE operations using generateName.
	Name string

	// UID is a unique identifier for this admission request.
	// Can be used for correlation and logging.
	UID string

	// UserInfo contains information about the user making the request.
	// Includes username, UID, groups, and extra fields.
	// Can be used for authorization decisions.
	UserInfo authenticationv1.UserInfo
}

// ValidationFunc is called to validate a Kubernetes resource admission request.
//
// Parameters:
//   - ctx: The validation context with full admission request information
//
// Returns:
//   - allowed: Whether the resource should be admitted
//   - reason: Human-readable reason for denial (empty if allowed)
//   - warnings: Soft warnings surfaced via AdmissionResponse.Warnings.
//     Returned for both allowed and denied responses so kubectl prints
//     them as "Warning:" lines without blocking admission. Each entry
//     should fit in 256 chars; over that, the API server truncates.
//   - err: Error during validation (500 response if non-nil)
//
// The function receives complete context including both old and new objects,
// operation type, and metadata. This allows validators to implement sophisticated
// validation logic based on the admission operation.
//
// Example:
//
//	func validateIngress(ctx *webhook.ValidationContext) (bool, string, []string, error) {
//	    // Access new object (already unstructured.Unstructured)
//	    if ctx.Object == nil {
//	        return false, "", nil, errors.New("object is nil")
//	    }
//
//	    // For UPDATE operations, compare with old object
//	    if ctx.Operation == "UPDATE" && ctx.OldObject != nil {
//	        // Both ctx.Object and ctx.OldObject are *unstructured.Unstructured
//	        // Validate the change...
//	    }
//
//	    spec, found, err := unstructured.NestedMap(ctx.Object.Object, "spec")
//	    if err != nil || !found {
//	        return false, "spec is required", nil, nil
//	    }
//
//	    return true, "", nil, nil
//	}
type ValidationFunc func(ctx *ValidationContext) (allowed bool, reason string, warnings []string, err error)

// ServerConfig configures the webhook HTTPS server.
type ServerConfig struct {
	// Port is the HTTPS port to listen on.
	// Default: 9443
	Port int

	// BindAddress is the address to bind to.
	// Default: "0.0.0.0"
	BindAddress string

	// CertPEM is the PEM-encoded server certificate.
	// Required.
	CertPEM []byte

	// KeyPEM is the PEM-encoded private key.
	// Required.
	KeyPEM []byte

	// Path is the URL path for the webhook endpoint.
	// Default: "/validate"
	Path string

	// ReadTimeout is the maximum duration for reading the entire request.
	// Default: 10s
	ReadTimeout time.Duration

	// WriteTimeout is the maximum duration before timing out writes of the response.
	// Default: 10s
	WriteTimeout time.Duration
}

// WebhookRule specifies which resources a webhook should intercept.
type WebhookRule struct {
	// APIGroups that this rule matches.
	// Example: ["networking.k8s.io"]
	APIGroups []string

	// APIVersions that this rule matches.
	// Example: ["v1"]
	APIVersions []string

	// Resources that this rule matches (plural, lowercase).
	// Example: ["ingresses"]
	Resources []string

	// Operations that this rule matches.
	// Default: ["CREATE", "UPDATE"]
	Operations []admissionv1.OperationType

	// Scope restricts the rule to cluster or namespace-scoped resources.
	// Default: "*" (all scopes)
	Scope *admissionv1.ScopeType
}
