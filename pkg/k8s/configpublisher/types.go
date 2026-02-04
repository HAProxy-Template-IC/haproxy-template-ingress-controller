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

package configpublisher

import (
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	listersv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/generated/listers/haproxytemplate/v1alpha1"

	"k8s.io/apimachinery/pkg/types"
)

// Listers holds the informer-backed listers for cached reads.
// These avoid direct API calls when checking if updates are needed.
type Listers struct {
	MapFiles     listersv1alpha1.HAProxyMapFileLister
	GeneralFiles listersv1alpha1.HAProxyGeneralFileLister
	CRTListFiles listersv1alpha1.HAProxyCRTListFileLister
	HAProxyCfgs  listersv1alpha1.HAProxyCfgLister
}

// AuxiliaryFiles contains all auxiliary files (maps, certificates, general files, crt-lists).
type AuxiliaryFiles struct {
	// MapFiles contains HAProxy map files.
	MapFiles []auxiliaryfiles.MapFile

	// SSLCertificates contains SSL/TLS certificate files.
	SSLCertificates []auxiliaryfiles.SSLCertificate

	// SSLCaFiles contains SSL CA certificate files for client/backend certificate verification.
	SSLCaFiles []auxiliaryfiles.SSLCaFile

	// GeneralFiles contains general-purpose files (error pages, etc.).
	GeneralFiles []auxiliaryfiles.GeneralFile

	// CRTListFiles contains crt-list files for SSL certificate lists.
	CRTListFiles []auxiliaryfiles.CRTListFile
}

// PublishRequest contains the information needed to publish HAProxy runtime configuration.
type PublishRequest struct {
	// TemplateConfigName is the name of the HAProxyTemplateConfig that generated this config.
	TemplateConfigName string

	// TemplateConfigNamespace is the namespace of the HAProxyTemplateConfig.
	TemplateConfigNamespace string

	// TemplateConfigUID is the UID of the HAProxyTemplateConfig (for ownerReferences).
	TemplateConfigUID types.UID

	// Config is the rendered HAProxy configuration content.
	Config string

	// ConfigPath is the file system path where the config is stored.
	// Default: /etc/haproxy/haproxy.cfg
	ConfigPath string

	// AuxiliaryFiles contains map files, SSL certificates, and general files.
	AuxiliaryFiles *AuxiliaryFiles

	// RenderedAt is the timestamp when the configuration was rendered.
	RenderedAt time.Time

	// ValidatedAt is the timestamp when the configuration was successfully validated.
	ValidatedAt time.Time

	// Checksum is the SHA-256 hash of the configuration content.
	Checksum string

	// NameSuffix appends to the generated resource name (e.g., "-invalid").
	// Used to publish separate resources for invalid configurations.
	// +optional
	NameSuffix string

	// ValidationError contains the error message if this configuration failed validation.
	// When set, this indicates the configuration is invalid and should not be deployed.
	// +optional
	ValidationError string

	// CompressionThreshold is the minimum size in bytes at which configs are compressed.
	// Configs smaller than this threshold are stored uncompressed.
	// Set to 0 or negative to disable compression.
	// Set to a positive value (e.g., 1048576 for 1 MiB) to enable compression.
	CompressionThreshold int64
}

// PublishResult contains the result of publishing configuration resources.
type PublishResult struct {
	// RuntimeConfigName is the name of the created/updated HAProxyRuntimeConfig.
	RuntimeConfigName string

	// RuntimeConfigNamespace is the namespace of the HAProxyRuntimeConfig.
	RuntimeConfigNamespace string

	// MapFileNames lists the names of created/updated HAProxyMapFile resources.
	MapFileNames []string

	// SecretNames lists the names of created/updated Secret resources for SSL certificates.
	SecretNames []string

	// GeneralFileNames lists the names of created/updated HAProxyGeneralFile resources.
	GeneralFileNames []string

	// CRTListFileNames lists the names of created/updated HAProxyCRTListFile resources.
	CRTListFileNames []string
}

// DeploymentStatusUpdate contains information about a configuration deployment to a pod.
type DeploymentStatusUpdate struct {
	// RuntimeConfigName is the name of the HAProxyRuntimeConfig to update.
	RuntimeConfigName string

	// RuntimeConfigNamespace is the namespace of the HAProxyRuntimeConfig.
	RuntimeConfigNamespace string

	// PodName is the name of the HAProxy pod that received the configuration.
	PodName string

	// DeployedAt is the timestamp when configuration was last changed on the pod.
	// Only set when operations > 0.
	DeployedAt time.Time

	// Checksum is the checksum of the configuration deployed to the pod.
	Checksum string

	// IsDriftCheck indicates whether this was a drift prevention check (GET-only)
	// or an actual sync operation (POST/PUT/DELETE).
	IsDriftCheck bool

	// LastReloadAt is the timestamp when HAProxy was last reloaded.
	// Only set when a reload was triggered.
	LastReloadAt *time.Time

	// LastReloadID is the reload identifier from HAProxy.
	// Only set when a reload was triggered.
	LastReloadID string

	// SyncDuration is how long the sync operation took.
	SyncDuration *time.Duration

	// VersionConflictRetries is the number of retries due to version conflicts.
	VersionConflictRetries int

	// FallbackUsed indicates whether raw config push was used.
	FallbackUsed bool

	// OperationSummary provides a breakdown of operations performed.
	OperationSummary *OperationSummary

	// Error contains the error message if sync failed.
	// Empty string indicates success.
	Error string
}

// OperationSummary provides statistics about sync operations.
type OperationSummary struct {
	TotalAPIOperations int
	BackendsAdded      int
	BackendsRemoved    int
	BackendsModified   int
	ServersAdded       int
	ServersRemoved     int
	ServersModified    int
	FrontendsAdded     int
	FrontendsRemoved   int
	FrontendsModified  int
}

// PodCleanupRequest contains information about a terminated pod to clean up.
type PodCleanupRequest struct {
	// PodName is the name of the terminated pod.
	PodName string

	// Namespace is the namespace where HAProxyCfg resources should be looked up.
	// This ensures namespace-scoped cleanup operations.
	Namespace string
}
