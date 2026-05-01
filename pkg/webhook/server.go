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
	"net"
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
//
// The server reads CertPEM/KeyPEM at construction time and validates them
// eagerly so a malformed cert surfaces in NewServer rather than at the
// first TLS handshake. The cert lifetime equals the server lifetime —
// rotation is the controller's job: when the underlying Secret changes,
// the controller's iteration-restart path constructs a new Server with
// the fresh PEM bytes (same pattern used for CRD/credentials changes).
type Server struct {
	config     ServerConfig
	validators map[string]ValidationFunc
	mu         sync.RWMutex
	httpServer *http.Server
	cert       tls.Certificate

	// listening is closed once the TLS listener has been bound to the
	// configured port. Callers that need to know the server is actually
	// accepting connections (e.g., an iteration sequencer that wants the
	// controller's readiness probe to wait for admission to be reachable)
	// can read from Listening() — until then connection attempts fail with
	// "connection refused" because Go's net.Listen hasn't returned yet.
	listening chan struct{}
}

// NewServer creates a new webhook server with the given configuration.
//
// The server will not start until Start() is called. The CertPEM/KeyPEM in
// config are parsed eagerly so configuration errors surface here rather
// than at the first TLS handshake.
func NewServer(config *ServerConfig) (*Server, error) {
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

	cert, err := tls.X509KeyPair(config.CertPEM, config.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("loading initial TLS certificate: %w", err)
	}

	return &Server{
		config:     *config,
		validators: make(map[string]ValidationFunc),
		cert:       cert,
		listening:  make(chan struct{}),
	}, nil
}

// Listening returns a channel that is closed once the TLS listener has
// been bound to the configured port. Until this channel is closed,
// admission requests sent to the server's address fail with "connection
// refused". The controller uses this signal so its Pod readiness probe
// only flips healthy after the webhook is actually reachable.
func (s *Server) Listening() <-chan struct{} {
	return s.listening
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
// The server binds to the configured port synchronously, closes the
// channel returned by Listening() to signal readiness, and then serves
// in a background goroutine. The method blocks until the server is shut
// down (context cancellation) or the serve loop returns an error.
//
// Splitting bind from serve matters because the Pod readiness probe
// must not flip healthy until admission is reachable — otherwise the
// API server starts routing AdmissionReview requests at the controller
// before net.Listen has returned, and every request bounces with
// "connection refused" until the listener finally binds. Callers that
// need to gate on the bind read Listening().
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc(s.config.Path, s.handleValidation)
	mux.HandleFunc("/healthz", s.handleHealthz)

	addr := fmt.Sprintf("%s:%d", s.config.BindAddress, s.config.Port)
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{s.cert},
		MinVersion:   tls.VersionTLS12,
	}

	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      mux,
		TLSConfig:    tlsConfig,
		ReadTimeout:  s.config.ReadTimeout,
		WriteTimeout: s.config.WriteTimeout,
	}

	// Bind synchronously so callers can observe success before any
	// admission request is routed at us.
	tcpListener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	tlsListener := tls.NewListener(tcpListener, tlsConfig)
	close(s.listening)

	// Serve in a goroutine so Start() can still block on context
	// cancellation for graceful shutdown handling.
	errChan := make(chan error, 1)
	go func() {
		if err := s.httpServer.Serve(tlsListener); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	select {
	case <-ctx.Done():
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
		return deniedResponse(fmt.Sprintf("parsing object: %v", err), http.StatusBadRequest)
	}

	// Parse old object (if present - for UPDATE/DELETE operations)
	var oldObj *unstructured.Unstructured
	if len(request.OldObject.Raw) > 0 {
		oldObj = &unstructured.Unstructured{}
		if err := json.Unmarshal(request.OldObject.Raw, oldObj); err != nil {
			return deniedResponse(fmt.Sprintf("parsing old object: %v", err), http.StatusBadRequest)
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
		return deniedResponse(fmt.Sprintf("validation error: %v", err), http.StatusInternalServerError)
	}

	if !allowed {
		// Validation failed
		return deniedResponse(reason, http.StatusForbidden)
	}

	// Validation passed
	return &admissionv1.AdmissionResponse{
		Allowed: true,
	}
}

// deniedResponse builds an AdmissionResponse with Allowed=false carrying a
// metav1.Status with the supplied message and HTTP-style code, used for the
// four reject paths in validate (parse failures, validator errors, denials).
func deniedResponse(message string, code int32) *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{
		Allowed: false,
		Result: &metav1.Status{
			Message: message,
			Code:    code,
		},
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
