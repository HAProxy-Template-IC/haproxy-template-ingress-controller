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

package webhook

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

var (
	scheme = runtime.NewScheme()
	codecs = serializer.NewCodecFactory(scheme)
)

func init() {
	// Register AdmissionReview types
	_ = admissionv1.AddToScheme(scheme)
}

// Server is an HTTPS webhook server that validates Kubernetes resources.
//
// The server handles AdmissionReview requests from the Kubernetes API server
// and calls registered validation functions to determine whether resources
// should be admitted.
//
// The server is thread-safe and can handle multiple concurrent requests.
type Server struct {
	config     ServerConfig
	validators map[string]ValidationFunc
	mu         sync.RWMutex
	httpServer *http.Server
}

// NewServer creates a new webhook server with the given configuration.
//
// The server will not start until Start() is called.
func NewServer(config *ServerConfig) *Server {
	// Apply defaults
	if config.Port == 0 {
		config.Port = 9443
	}
	if config.BindAddress == "" {
		config.BindAddress = "0.0.0.0"
	}
	if config.Path == "" {
		config.Path = "/validate"
	}
	if config.ReadTimeout == 0 {
		config.ReadTimeout = 10 * time.Second
	}
	if config.WriteTimeout == 0 {
		config.WriteTimeout = 10 * time.Second
	}

	return &Server{
		config:     *config,
		validators: make(map[string]ValidationFunc),
	}
}

// RegisterValidator registers a validation function for a specific resource type.
//
// The gvk parameter should be in the format "version.Kind" (e.g., "v1.Ingress").
// For resources with a group, use "group/version.Kind" (e.g., "networking.k8s.io/v1.Ingress").
//
// If a validator is already registered for this gvk, it will be replaced.
//
// This method is thread-safe.
func (s *Server) RegisterValidator(gvk string, fn ValidationFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.validators[gvk] = fn
}

// Start starts the HTTPS webhook server.
//
// The server will listen on the configured port and handle AdmissionReview requests.
// The server will gracefully shut down when the context is cancelled.
//
// This method blocks until the server is shut down.
func (s *Server) Start(ctx context.Context) error {
	// Create TLS certificate
	cert, err := tls.X509KeyPair(s.config.CertPEM, s.config.KeyPEM)
	if err != nil {
		return fmt.Errorf("loading TLS certificate: %w", err)
	}

	// Create HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc(s.config.Path, s.handleValidation)
	mux.HandleFunc("/healthz", s.handleHealthz)

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", s.config.BindAddress, s.config.Port),
		Handler: mux,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
		ReadTimeout:  s.config.ReadTimeout,
		WriteTimeout: s.config.WriteTimeout,
	}

	// Start server in goroutine
	errChan := make(chan error, 1)
	go func() {
		if err := s.httpServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	// Wait for context cancellation or server error
	select {
	case <-ctx.Done():
		// Graceful shutdown
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return s.httpServer.Shutdown(shutdownCtx)
	case err := <-errChan:
		return err
	}
}

// handleValidation handles AdmissionReview requests.
func (s *Server) handleValidation(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading request: %v", err), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Decode AdmissionReview request
	review := &admissionv1.AdmissionReview{}
	deserializer := codecs.UniversalDeserializer()
	if _, _, err := deserializer.Decode(body, nil, review); err != nil {
		http.Error(w, fmt.Sprintf("decoding request: %v", err), http.StatusBadRequest)
		return
	}

	// Validate the request
	response := s.validate(review.Request)

	// Create AdmissionReview response
	review.Response = response
	review.Response.UID = review.Request.UID

	// Encode response
	responseBytes, err := json.Marshal(review)
	if err != nil {
		http.Error(w, fmt.Sprintf("encoding response: %v", err), http.StatusInternalServerError)
		return
	}

	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(responseBytes)
}

// validate validates an AdmissionRequest.
func (s *Server) validate(request *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	// Get validator for this resource type
	gvk := s.getGVK(request)

	s.mu.RLock()
	validator, exists := s.validators[gvk]
	s.mu.RUnlock()

	if !exists {
		// No validator registered, allow by default
		return &admissionv1.AdmissionResponse{
			Allowed: true,
		}
	}

	// Parse new object as unstructured (same type as stores use)
	// This ensures consistent data types throughout reconciliation and webhook paths
	obj := &unstructured.Unstructured{}
	if err := json.Unmarshal(request.Object.Raw, obj); err != nil {
		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: fmt.Sprintf("parsing object: %v", err),
				Code:    http.StatusBadRequest,
			},
		}
	}

	// Parse old object (if present - for UPDATE/DELETE operations)
	var oldObj *unstructured.Unstructured
	if len(request.OldObject.Raw) > 0 {
		oldObj = &unstructured.Unstructured{}
		if err := json.Unmarshal(request.OldObject.Raw, oldObj); err != nil {
			return &admissionv1.AdmissionResponse{
				Allowed: false,
				Result: &metav1.Status{
					Message: fmt.Sprintf("parsing old object: %v", err),
					Code:    http.StatusBadRequest,
				},
			}
		}
	}

	// Extract namespace and name from object metadata
	namespace, name := s.extractMetadata(obj)

	// Build validation context
	ctx := &ValidationContext{
		Object:    obj,
		OldObject: oldObj,
		Operation: string(request.Operation),
		Namespace: namespace,
		Name:      name,
		UID:       string(request.UID),
		UserInfo:  request.UserInfo,
	}

	// Call validator with full context
	allowed, reason, err := validator(ctx)

	if err != nil {
		// Validation error (internal server error)
		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: fmt.Sprintf("validation error: %v", err),
				Code:    http.StatusInternalServerError,
			},
		}
	}

	if !allowed {
		// Validation failed
		return &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: reason,
				Code:    http.StatusForbidden,
			},
		}
	}

	// Validation passed
	return &admissionv1.AdmissionResponse{
		Allowed: true,
	}
}

// extractMetadata extracts namespace and name from a resource object.
//
// Returns empty strings if metadata is not found.
func (s *Server) extractMetadata(obj *unstructured.Unstructured) (namespace, name string) {
	if obj == nil {
		return "", ""
	}

	// Use unstructured API to extract metadata
	namespace = obj.GetNamespace()
	name = obj.GetName()

	return namespace, name
}

// getGVK returns the GVK string for an AdmissionRequest.
//
// Format: "group/version.Kind" or "version.Kind" for core types.
func (s *Server) getGVK(request *admissionv1.AdmissionRequest) string {
	if request.Kind.Group == "" {
		return fmt.Sprintf("%s.%s", request.Kind.Version, request.Kind.Kind)
	}
	return fmt.Sprintf("%s/%s.%s", request.Kind.Group, request.Kind.Version, request.Kind.Kind)
}

// handleHealthz handles health check requests.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
