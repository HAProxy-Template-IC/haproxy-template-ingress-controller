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
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"path/filepath"
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
// The certificate is resolved per TLS handshake through getCertificate. When
// ServerConfig.CertDir is set, that callback reloads tls.crt/tls.key from disk
// on change, so a rotated certificate (cert-manager renewal written to a
// mounted Secret) is served without restarting. Otherwise getCertificate
// returns a fixed certificate parsed once from CertPEM/KeyPEM. Either way the
// certificate is validated eagerly in NewServer so a malformed cert surfaces
// there rather than at the first handshake.
type Server struct {
	config     ServerConfig
	validators map[string]ValidationFunc
	mu         sync.RWMutex
	// onUnregisteredGVK is seeded from ServerConfig and swappable via
	// SetOnUnregisteredGVK; guarded by mu alongside validators.
	onUnregisteredGVK func(gvk string)
	// boundAddr is the listener's actual address, resolved after net.Listen so
	// a Port of 0 (tests) can be discovered instead of guessed. Guarded by mu.
	boundAddr      string
	httpServer     *http.Server
	getCertificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)
	generation     *validatorGeneration
	closed         bool

	// listening is closed once the TLS listener has been bound to the
	// configured port. Callers that need to know the server is actually
	// accepting connections (e.g., an iteration sequencer that wants the
	// controller's readiness probe to wait for admission to be reachable)
	// can read from Listening() — until then connection attempts fail with
	// "connection refused" because Go's net.Listen hasn't returned yet.
	listening chan struct{}
}

type validatorGeneration struct {
	validators        map[string]ValidationFunc
	onUnregisteredGVK func(gvk string)
	onRetired         func()
	inFlight          sync.WaitGroup
	retireOnce        sync.Once
}

func newValidatorGeneration(
	validators map[string]ValidationFunc,
	onUnregisteredGVK func(gvk string),
	onRetired func(),
) *validatorGeneration {
	return &validatorGeneration{
		validators:        validators,
		onUnregisteredGVK: onUnregisteredGVK,
		onRetired:         onRetired,
	}
}

func (g *validatorGeneration) retire() {
	if g == nil {
		return
	}
	g.retireOnce.Do(func() {
		g.inFlight.Wait()
		if g.onRetired != nil {
			g.onRetired()
		}
	})
}

// NewServer creates a new webhook server with the given configuration.
//
// The server will not start until Start() is called. The certificate is
// loaded eagerly — from CertDir (reloading, on change) or from CertPEM/KeyPEM
// (fixed) — so configuration errors surface here rather than at the first TLS
// handshake.
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
	// Above the 90s client-go transport default, so the API server closes first.
	if config.IdleTimeout == 0 {
		config.IdleTimeout = 120 * time.Second
	}

	getCertificate, err := newGetCertificate(config)
	if err != nil {
		return nil, err
	}

	generation := newValidatorGeneration(make(map[string]ValidationFunc), config.OnUnregisteredGVK, nil)
	return &Server{
		config:            *config,
		validators:        generation.validators,
		onUnregisteredGVK: config.OnUnregisteredGVK,
		getCertificate:    getCertificate,
		generation:        generation,
		listening:         make(chan struct{}),
	}, nil
}

// newGetCertificate builds the tls.Config GetCertificate callback: a reloading
// file source when CertDir is set, otherwise a fixed certificate parsed once
// from CertPEM/KeyPEM.
func newGetCertificate(config *ServerConfig) (func(*tls.ClientHelloInfo) (*tls.Certificate, error), error) {
	if config.CertDir != "" {
		reloader, err := newCertReloader(
			filepath.Join(config.CertDir, "tls.crt"),
			filepath.Join(config.CertDir, "tls.key"),
		)
		if err != nil {
			return nil, fmt.Errorf("loading webhook certificate from %s: %w", config.CertDir, err)
		}
		return reloader.GetCertificate, nil
	}

	cert, err := tls.X509KeyPair(config.CertPEM, config.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("loading initial TLS certificate: %w", err)
	}
	return func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &cert, nil }, nil
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

// Addr returns the address the listener actually bound, or "" before it has.
// Callers that configure Port 0 — tests, which must not fight over a fixed
// port — read the kernel-assigned port from here once Listening() has closed.
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.boundAddr
}

// SetOnUnregisteredGVK REPLACES the unregistered-GVK reporter.
//
// A server that outlives the wiring which built its validator table (the
// controller keeps one listener bound across config reinitializations, so an
// admission request never meets a closed port) must be able to re-point this
// callback at the current wiring's metrics recorder. It is read under the same
// lock as the validator table, so it swaps atomically with respect to in-flight
// requests.
func (s *Server) SetOnUnregisteredGVK(fn func(gvk string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onUnregisteredGVK = fn
	s.generation.onUnregisteredGVK = fn
}

// SetValidators atomically replaces the validator table.
func (s *Server) SetValidators(validators map[string]ValidationFunc) {
	s.mu.RLock()
	onUnregisteredGVK := s.onUnregisteredGVK
	s.mu.RUnlock()
	_ = s.ReplaceValidatorGeneration(validators, onUnregisteredGVK, nil)
}

// ReplaceValidatorGeneration installs one complete table and retires the old
// table after all requests that acquired it have returned.
func (s *Server) ReplaceValidatorGeneration(
	validators map[string]ValidationFunc,
	onUnregisteredGVK func(gvk string),
	onRetired func(),
) error {
	replacement := make(map[string]ValidationFunc, len(validators))
	maps.Copy(replacement, validators)
	next := newValidatorGeneration(replacement, onUnregisteredGVK, onRetired)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("webhook server is closed")
	}
	previous := s.generation
	s.generation = next
	s.validators = next.validators
	s.onUnregisteredGVK = next.onUnregisteredGVK
	s.mu.Unlock()

	previous.retire()
	return nil
}

func (s *Server) retireValidatorGeneration() {
	empty := newValidatorGeneration(make(map[string]ValidationFunc), nil, nil)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	previous := s.generation
	s.generation = empty
	s.validators = empty.validators
	s.onUnregisteredGVK = nil
	s.mu.Unlock()

	previous.retire()
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
	defer s.retireValidatorGeneration()

	mux := http.NewServeMux()
	mux.HandleFunc(s.config.Path, s.handleValidation)
	mux.HandleFunc("/healthz", s.handleHealthz)

	addr := fmt.Sprintf("%s:%d", s.config.BindAddress, s.config.Port)
	tlsConfig := &tls.Config{
		GetCertificate: s.getCertificate,
		MinVersion:     tls.VersionTLS12,
	}

	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      mux,
		TLSConfig:    tlsConfig,
		ReadTimeout:  s.config.ReadTimeout,
		WriteTimeout: s.config.WriteTimeout,
		IdleTimeout:  s.config.IdleTimeout,
	}

	// Bind synchronously so callers can observe success before any
	// admission request is routed at us.
	tcpListener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	tlsListener := tls.NewListener(tcpListener, tlsConfig)
	s.mu.Lock()
	s.boundAddr = tcpListener.Addr().String()
	s.mu.Unlock()
	close(s.listening)

	serveDone := make(chan error, 1)
	go func() {
		err := s.httpServer.Serve(tlsListener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveDone <- err
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		shutdownErr := s.httpServer.Shutdown(shutdownCtx)
		cancel()
		if shutdownErr != nil {
			shutdownErr = errors.Join(shutdownErr, s.httpServer.Close())
		}
		return errors.Join(shutdownErr, <-serveDone)
	case err := <-serveDone:
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
	generation := s.generation
	generation.inFlight.Add(1)
	validator, exists := generation.validators[gvk]
	onUnregistered := generation.onUnregisteredGVK
	s.mu.RUnlock()
	defer generation.inFlight.Done()

	if !exists {
		if onUnregistered != nil {
			onUnregistered(gvk)
		}
		return deniedResponse(
			fmt.Sprintf("no validator registered for %s; retry after controller initialization", gvk),
			http.StatusServiceUnavailable,
		)
	}

	// DELETE requests may carry only OldObject. The controller's structural
	// gate decides whether the operation has enough object data to validate.
	var obj *unstructured.Unstructured
	if len(request.Object.Raw) > 0 {
		obj = &unstructured.Unstructured{}
		if err := json.Unmarshal(request.Object.Raw, obj); err != nil {
			return deniedResponse(fmt.Sprintf("parsing object: %v", err), http.StatusBadRequest)
		}
	}

	// Parse old object (if present - for UPDATE/DELETE operations)
	var oldObj *unstructured.Unstructured
	if len(request.OldObject.Raw) > 0 {
		oldObj = &unstructured.Unstructured{}
		if err := json.Unmarshal(request.OldObject.Raw, oldObj); err != nil {
			return deniedResponse(fmt.Sprintf("parsing old object: %v", err), http.StatusBadRequest)
		}
	}

	metadataObject := obj
	if metadataObject == nil {
		metadataObject = oldObj
	}
	namespace, name := s.extractMetadata(metadataObject)

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
	allowed, reason, warnings, err := validator(ctx)

	if err != nil {
		return deniedResponse(fmt.Sprintf("validation error: %v", err), http.StatusInternalServerError)
	}

	if !allowed {
		// Validation failed; warnings still surface so the user sees both
		// the denial reason and any non-fatal diagnostics that ran before
		// the denial path was taken.
		resp := deniedResponse(reason, http.StatusForbidden)
		resp.Warnings = warnings
		return resp
	}

	// Validation passed
	return &admissionv1.AdmissionResponse{
		Allowed:  true,
		Warnings: warnings,
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
