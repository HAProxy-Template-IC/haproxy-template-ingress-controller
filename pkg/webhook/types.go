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
//   - err: Error during validation (500 response if non-nil)
//
// The function receives complete context including both old and new objects,
// operation type, and metadata. This allows validators to implement sophisticated
// validation logic based on the admission operation.
//
// Example:
//
//	func validateIngress(ctx *webhook.ValidationContext) (bool, string, error) {
//	    // Access new object (already unstructured.Unstructured)
//	    if ctx.Object == nil {
//	        return false, "", fmt.Errorf("object is nil")
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
//	        return false, "spec is required", nil
//	    }
//
//	    return true, "", nil
//	}
type ValidationFunc func(ctx *ValidationContext) (allowed bool, reason string, err error)

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

// CertConfig configures certificate generation and rotation.
type CertConfig struct {
	// Namespace where the webhook service runs.
	// Required for DNS names in certificate.
	Namespace string

	// ServiceName is the name of the Kubernetes Service exposing the webhook.
	// Required for DNS names in certificate.
	ServiceName string

	// CommonName for the generated certificates.
	// Default: "<service>.<namespace>.svc"
	CommonName string

	// Organization for the CA certificate.
	// Default: "haproxy-template-ic"
	Organization string

	// ValidityDuration is how long certificates are valid.
	// Default: 365 days
	ValidityDuration time.Duration

	// RotationThreshold triggers rotation when certificate expires within this duration.
	// Default: 30 days
	RotationThreshold time.Duration
}

// Certificates holds a complete certificate chain for the webhook.
type Certificates struct {
	// CACert is the PEM-encoded CA certificate.
	// This is injected into the ValidatingWebhookConfiguration.
	CACert []byte

	// CAKey is the PEM-encoded CA private key.
	// Kept secret, used to sign server certificates.
	CAKey []byte

	// ServerCert is the PEM-encoded server certificate.
	// Used by the webhook HTTPS server.
	ServerCert []byte

	// ServerKey is the PEM-encoded server private key.
	// Used by the webhook HTTPS server.
	ServerKey []byte

	// ValidUntil is when the server certificate expires.
	ValidUntil time.Time

	// GeneratedAt is when these certificates were created.
	GeneratedAt time.Time
}

// WebhookConfigSpec specifies how to configure the ValidatingWebhookConfiguration.
type WebhookConfigSpec struct {
	// Name of the ValidatingWebhookConfiguration resource.
	// Required.
	Name string

	// Namespace where the webhook service runs.
	// Required for webhook client config.
	Namespace string

	// ServiceName is the name of the Service exposing the webhook.
	// Required for webhook client config.
	ServiceName string

	// Path is the URL path on the webhook server.
	// Default: "/validate"
	Path string

	// CABundle is the PEM-encoded CA certificate to trust.
	// Required. Obtained from certificate manager.
	CABundle []byte

	// Rules specify which resources to validate.
	// Each rule maps to a webhook in the configuration.
	Rules []WebhookRule

	// FailurePolicy determines what happens if the webhook fails.
	// Default: Fail (reject requests if webhook unavailable)
	FailurePolicy *admissionv1.FailurePolicyType

	// MatchPolicy determines how rules are matched.
	// Default: Equivalent (match semantically equivalent requests)
	MatchPolicy *admissionv1.MatchPolicyType

	// SideEffects indicates whether the webhook has side effects.
	// Default: None
	SideEffects *admissionv1.SideEffectClass

	// TimeoutSeconds is the maximum time to wait for a response.
	// Default: 10
	TimeoutSeconds *int32
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

// ValidationResult represents the outcome of a validation request.
type ValidationResult struct {
	// Allowed indicates whether the request should be admitted.
	Allowed bool

	// Reason provides a human-readable explanation for denial.
	// Empty if Allowed is true.
	Reason string

	// Warnings are non-blocking messages shown to the user.
	Warnings []string
}
