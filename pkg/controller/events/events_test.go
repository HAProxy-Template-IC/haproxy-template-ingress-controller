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

package events

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// TestLifecycleEvents tests lifecycle.go event types.
func TestLifecycleEvents(t *testing.T) {
	t.Run("ControllerStartedEvent", func(t *testing.T) {
		event := NewControllerStartedEvent("config-v1", "secret-v2")
		require.NotNil(t, event)
		assert.Equal(t, "config-v1", event.ConfigVersion)
		assert.Equal(t, "secret-v2", event.SecretVersion)
		assert.Equal(t, EventTypeControllerStarted, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("ControllerShutdownEvent", func(t *testing.T) {
		event := NewControllerShutdownEvent("graceful shutdown")
		require.NotNil(t, event)
		assert.Equal(t, "graceful shutdown", event.Reason)
		assert.Equal(t, EventTypeControllerShutdown, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})
}

// TestConfigEvents tests config.go event types.
func TestConfigEvents(t *testing.T) {
	t.Run("ConfigParsedEvent", func(t *testing.T) {
		config := map[string]string{"key": "value"}
		templateConfig := map[string]string{"template": "config"}
		event := NewConfigParsedEvent(config, templateConfig, "v1", "secret-v1")
		require.NotNil(t, event)
		assert.Equal(t, config, event.Config)
		assert.Equal(t, templateConfig, event.TemplateConfig)
		assert.Equal(t, "v1", event.Version)
		assert.Equal(t, "secret-v1", event.SecretVersion)
		assert.Equal(t, EventTypeConfigParsed, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("ConfigValidationRequest", func(t *testing.T) {
		config := map[string]string{"key": "value"}
		event := NewConfigValidationRequest(config, "v1")
		require.NotNil(t, event)
		assert.Equal(t, config, event.Config)
		assert.Equal(t, "v1", event.Version)
		assert.Equal(t, EventTypeConfigValidationRequest, event.EventType())
		assert.NotEmpty(t, event.RequestID())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("ConfigValidationResponse", func(t *testing.T) {
		errors := []string{"error1", "error2"}
		event := NewConfigValidationResponse("req-123", "basic", false, errors)
		require.NotNil(t, event)
		assert.Equal(t, "basic", event.ValidatorName)
		assert.False(t, event.Valid)
		assert.Equal(t, errors, event.Errors)
		assert.Equal(t, EventTypeConfigValidationResponse, event.EventType())
		assert.Equal(t, "req-123", event.RequestID())
		assert.Equal(t, "basic", event.Responder())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("ConfigValidationResponse_DefensiveCopy", func(t *testing.T) {
		errors := []string{"error1", "error2"}
		event := NewConfigValidationResponse("req-123", "basic", false, errors)

		// Modify original slice
		errors[0] = "modified"

		// Event should have the original value
		assert.Equal(t, "error1", event.Errors[0])
	})

	t.Run("ConfigValidationResponse_EmptyErrors", func(t *testing.T) {
		event := NewConfigValidationResponse("req-123", "basic", true, nil)
		require.NotNil(t, event)
		assert.True(t, event.Valid)
		assert.Nil(t, event.Errors)
	})

	t.Run("ConfigValidatedEvent", func(t *testing.T) {
		config := map[string]string{"key": "value"}
		templateConfig := map[string]string{"template": "config"}
		event := NewConfigValidatedEvent(config, templateConfig, "v1", "secret-v1")
		require.NotNil(t, event)
		assert.Equal(t, config, event.Config)
		assert.Equal(t, templateConfig, event.TemplateConfig)
		assert.Equal(t, "v1", event.Version)
		assert.Equal(t, "secret-v1", event.SecretVersion)
		assert.Equal(t, EventTypeConfigValidated, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("ConfigInvalidEvent", func(t *testing.T) {
		validationErrors := map[string][]string{
			"basic":    {"error1"},
			"template": {"error2", "error3"},
		}
		event := NewConfigInvalidEvent("v1", nil, validationErrors)
		require.NotNil(t, event)
		assert.Equal(t, "v1", event.Version)
		assert.Nil(t, event.TemplateConfig)
		assert.Equal(t, validationErrors, event.ValidationErrors)
		assert.Equal(t, EventTypeConfigInvalid, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("ConfigInvalidEvent_DefensiveCopy", func(t *testing.T) {
		validationErrors := map[string][]string{
			"basic": {"error1"},
		}
		event := NewConfigInvalidEvent("v1", nil, validationErrors)

		// Modify original map
		validationErrors["basic"][0] = "modified"
		validationErrors["new"] = []string{"new error"}

		// Event should have the original values
		assert.Equal(t, "error1", event.ValidationErrors["basic"][0])
		assert.NotContains(t, event.ValidationErrors, "new")
	})

	t.Run("ConfigResourceChangedEvent", func(t *testing.T) {
		resource := map[string]string{"name": "my-config"}
		event := NewConfigResourceChangedEvent(resource)
		require.NotNil(t, event)
		assert.Equal(t, resource, event.Resource)
		assert.Equal(t, EventTypeConfigResourceChanged, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})
}

// TestResourceEvents tests resource.go event types.
func TestResourceEvents(t *testing.T) {
	t.Run("ResourceIndexUpdatedEvent", func(t *testing.T) {
		changeStats := types.ChangeStats{
			Created:       2,
			Modified:      1,
			Deleted:       0,
			IsInitialSync: false,
		}
		event := NewResourceIndexUpdatedEvent("ingresses", changeStats)
		require.NotNil(t, event)
		assert.Equal(t, "ingresses", event.ResourceTypeName)
		assert.Equal(t, changeStats, event.ChangeStats)
		assert.Equal(t, EventTypeResourceIndexUpdated, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("ResourceSyncCompleteEvent", func(t *testing.T) {
		event := NewResourceSyncCompleteEvent("services", 10)
		require.NotNil(t, event)
		assert.Equal(t, "services", event.ResourceTypeName)
		assert.Equal(t, 10, event.InitialCount)
		assert.Equal(t, EventTypeResourceSyncComplete, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("IndexSynchronizedEvent", func(t *testing.T) {
		resourceCounts := map[string]int{
			"ingresses": 5,
			"services":  10,
		}
		event := NewIndexSynchronizedEvent(resourceCounts)
		require.NotNil(t, event)
		assert.Equal(t, resourceCounts, event.ResourceCounts)
		assert.Equal(t, EventTypeIndexSynchronized, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("IndexSynchronizedEvent_DefensiveCopy", func(t *testing.T) {
		resourceCounts := map[string]int{
			"ingresses": 5,
		}
		event := NewIndexSynchronizedEvent(resourceCounts)

		// Modify original map
		resourceCounts["ingresses"] = 100
		resourceCounts["new"] = 50

		// Event should have the original values
		assert.Equal(t, 5, event.ResourceCounts["ingresses"])
		assert.NotContains(t, event.ResourceCounts, "new")
	})
}

// TestLeaderEvents tests leader.go event types.
func TestLeaderEvents(t *testing.T) {
	t.Run("LeaderElectionStartedEvent", func(t *testing.T) {
		event := NewLeaderElectionStartedEvent("pod-1", "my-lease", "default")
		require.NotNil(t, event)
		assert.Equal(t, "pod-1", event.Identity)
		assert.Equal(t, "my-lease", event.LeaseName)
		assert.Equal(t, "default", event.LeaseNamespace)
		assert.Equal(t, EventTypeLeaderElectionStarted, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("BecameLeaderEvent", func(t *testing.T) {
		event := NewBecameLeaderEvent("pod-1")
		require.NotNil(t, event)
		assert.Equal(t, "pod-1", event.Identity)
		assert.Equal(t, EventTypeBecameLeader, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("LostLeadershipEvent", func(t *testing.T) {
		event := NewLostLeadershipEvent("pod-1", "graceful_shutdown")
		require.NotNil(t, event)
		assert.Equal(t, "pod-1", event.Identity)
		assert.Equal(t, "graceful_shutdown", event.Reason)
		assert.Equal(t, EventTypeLostLeadership, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("NewLeaderObservedEvent", func(t *testing.T) {
		event := NewNewLeaderObservedEvent("pod-2", false)
		require.NotNil(t, event)
		assert.Equal(t, "pod-2", event.NewLeaderIdentity)
		assert.False(t, event.IsSelf)
		assert.Equal(t, EventTypeNewLeaderObserved, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})
}

// TestReconciliationEvents tests reconciliation.go event types.
func TestReconciliationEvents(t *testing.T) {
	t.Run("ReconciliationTriggeredEvent", func(t *testing.T) {
		event := NewReconciliationTriggeredEvent("config_change", true)
		require.NotNil(t, event)
		assert.Equal(t, "config_change", event.Reason)
		assert.Equal(t, EventTypeReconciliationTriggered, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("ReconciliationTriggeredEvent_WithNewCorrelation", func(t *testing.T) {
		event := NewReconciliationTriggeredEvent("config_change", true, WithNewCorrelation())
		require.NotNil(t, event)
		assert.NotEmpty(t, event.CorrelationID())
		assert.NotEmpty(t, event.EventID())
	})

	t.Run("ReconciliationStartedEvent", func(t *testing.T) {
		event := NewReconciliationStartedEvent("config_change")
		require.NotNil(t, event)
		assert.Equal(t, "config_change", event.Trigger)
		assert.Equal(t, EventTypeReconciliationStarted, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("ReconciliationStartedEvent_WithCorrelation", func(t *testing.T) {
		event := NewReconciliationStartedEvent("config_change",
			WithCorrelation("corr-123", "cause-456"))
		require.NotNil(t, event)
		assert.Equal(t, "corr-123", event.CorrelationID())
		assert.Equal(t, "cause-456", event.CausationID())
	})

	t.Run("ReconciliationCompletedEvent", func(t *testing.T) {
		event := NewReconciliationCompletedEvent(100)
		require.NotNil(t, event)
		assert.Equal(t, int64(100), event.DurationMs)
		assert.Equal(t, EventTypeReconciliationCompleted, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("ReconciliationFailedEvent", func(t *testing.T) {
		event := NewReconciliationFailedEvent("template error", "render")
		require.NotNil(t, event)
		assert.Equal(t, "template error", event.Error)
		assert.Equal(t, "render", event.Phase)
		assert.Equal(t, EventTypeReconciliationFailed, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})
}

// TestCredentialsEvents tests credentials.go event types.
func TestCredentialsEvents(t *testing.T) {
	t.Run("SecretResourceChangedEvent", func(t *testing.T) {
		resource := map[string]string{"name": "my-secret"}
		event := NewSecretResourceChangedEvent(resource)
		require.NotNil(t, event)
		assert.Equal(t, resource, event.Resource)
		assert.Equal(t, EventTypeSecretResourceChanged, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("CredentialsUpdatedEvent", func(t *testing.T) {
		credentials := map[string]string{"user": "admin"}
		event := NewCredentialsUpdatedEvent(credentials, "v1")
		require.NotNil(t, event)
		assert.Equal(t, credentials, event.Credentials)
		assert.Equal(t, "v1", event.SecretVersion)
		assert.Equal(t, EventTypeCredentialsUpdated, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("CredentialsInvalidEvent", func(t *testing.T) {
		event := NewCredentialsInvalidEvent("v1", "missing field")
		require.NotNil(t, event)
		assert.Equal(t, "v1", event.SecretVersion)
		assert.Equal(t, "missing field", event.Error)
		assert.Equal(t, EventTypeCredentialsInvalid, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})
}

// TestStorageEvents tests storage.go event types.
func TestStorageEvents(t *testing.T) {
	t.Run("StorageSyncStartedEvent", func(t *testing.T) {
		endpoints := []interface{}{"endpoint1", "endpoint2"}
		event := NewStorageSyncStartedEvent("pre-config", endpoints)
		require.NotNil(t, event)
		assert.Equal(t, "pre-config", event.Phase)
		assert.Len(t, event.Endpoints, 2)
		assert.Equal(t, EventTypeStorageSyncStarted, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("StorageSyncStartedEvent_DefensiveCopy", func(t *testing.T) {
		endpoints := []interface{}{"endpoint1"}
		event := NewStorageSyncStartedEvent("config", endpoints)

		// Modify original slice
		endpoints[0] = "modified"

		// Event should have original value
		assert.Equal(t, "endpoint1", event.Endpoints[0])
	})

	t.Run("StorageSyncCompletedEvent", func(t *testing.T) {
		stats := map[string]int{"files": 5}
		event := NewStorageSyncCompletedEvent("config", stats, 100)
		require.NotNil(t, event)
		assert.Equal(t, "config", event.Phase)
		assert.Equal(t, stats, event.Stats)
		assert.Equal(t, int64(100), event.DurationMs)
		assert.Equal(t, EventTypeStorageSyncCompleted, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("StorageSyncFailedEvent", func(t *testing.T) {
		event := NewStorageSyncFailedEvent("post-config", "connection refused")
		require.NotNil(t, event)
		assert.Equal(t, "post-config", event.Phase)
		assert.Equal(t, "connection refused", event.Error)
		assert.Equal(t, EventTypeStorageSyncFailed, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})
}

// TestCertificateEvents tests certificate.go event types.
func TestCertificateEvents(t *testing.T) {
	t.Run("CertResourceChangedEvent", func(t *testing.T) {
		resource := map[string]string{"name": "my-cert"}
		event := NewCertResourceChangedEvent(resource)
		require.NotNil(t, event)
		assert.Equal(t, resource, event.Resource)
		assert.Equal(t, EventTypeCertResourceChanged, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("CertParsedEvent", func(t *testing.T) {
		certPEM := []byte("cert-content")
		keyPEM := []byte("key-content")
		event := NewCertParsedEvent(certPEM, keyPEM, "v1")
		require.NotNil(t, event)
		assert.Equal(t, certPEM, event.CertPEM)
		assert.Equal(t, keyPEM, event.KeyPEM)
		assert.Equal(t, "v1", event.Version)
		assert.Equal(t, EventTypeCertParsed, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("CertParsedEvent_DefensiveCopy", func(t *testing.T) {
		certPEM := []byte("cert-content")
		keyPEM := []byte("key-content")
		event := NewCertParsedEvent(certPEM, keyPEM, "v1")

		// Modify original slices
		certPEM[0] = 'X'
		keyPEM[0] = 'Y'

		// Event should have original values
		assert.Equal(t, byte('c'), event.CertPEM[0])
		assert.Equal(t, byte('k'), event.KeyPEM[0])
	})
}

// TestHTTPEvents tests http.go event types.
func TestHTTPEvents(t *testing.T) {
	t.Run("HTTPResourceUpdatedEvent", func(t *testing.T) {
		event := NewHTTPResourceUpdatedEvent("https://example.com/resource", "abc123", 1024)
		require.NotNil(t, event)
		assert.Equal(t, "https://example.com/resource", event.URL)
		assert.Equal(t, "abc123", event.ContentChecksum)
		assert.Equal(t, 1024, event.ContentSize)
		assert.Equal(t, EventTypeHTTPResourceUpdated, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("HTTPResourceAcceptedEvent", func(t *testing.T) {
		event := NewHTTPResourceAcceptedEvent("https://example.com/resource", "abc123", 1024)
		require.NotNil(t, event)
		assert.Equal(t, "https://example.com/resource", event.URL)
		assert.Equal(t, "abc123", event.ContentChecksum)
		assert.Equal(t, 1024, event.ContentSize)
		assert.Equal(t, EventTypeHTTPResourceAccepted, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("HTTPResourceRejectedEvent", func(t *testing.T) {
		event := NewHTTPResourceRejectedEvent("https://example.com/resource", "abc123", "invalid config")
		require.NotNil(t, event)
		assert.Equal(t, "https://example.com/resource", event.URL)
		assert.Equal(t, "abc123", event.ContentChecksum)
		assert.Equal(t, "invalid config", event.Reason)
		assert.Equal(t, EventTypeHTTPResourceRejected, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})
}

// TestTemplateEvents tests template.go event types.
func TestTemplateEvents(t *testing.T) {
	t.Run("TemplateRenderedEvent", func(t *testing.T) {
		auxFiles := map[string]string{"file1": "content1"}
		event := NewTemplateRenderedEvent(
			"haproxy config",
			auxFiles,
			5,
			100,
			"resource_change",
			true, // coalescible
		)
		require.NotNil(t, event)
		assert.Equal(t, "haproxy config", event.HAProxyConfig)
		assert.Equal(t, auxFiles, event.AuxiliaryFiles)
		assert.Equal(t, 5, event.AuxiliaryFileCount)
		assert.Equal(t, int64(100), event.DurationMs)
		assert.Equal(t, len("haproxy config"), event.ConfigBytes)
		assert.Equal(t, EventTypeTemplateRendered, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("TemplateRenderedEvent_WithCorrelation", func(t *testing.T) {
		event := NewTemplateRenderedEvent("cfg", nil, 0, 0, "", true,
			WithCorrelation("corr-123", "cause-456"))
		require.NotNil(t, event)
		assert.Equal(t, "corr-123", event.CorrelationID())
		assert.Equal(t, "cause-456", event.CausationID())
		assert.NotEmpty(t, event.EventID())
	})

	t.Run("TemplateRenderFailedEvent", func(t *testing.T) {
		event := NewTemplateRenderFailedEvent("haproxy.cfg", "syntax error", "stack trace line 1\nstack trace line 2")
		require.NotNil(t, event)
		assert.Equal(t, "haproxy.cfg", event.TemplateName)
		assert.Equal(t, "syntax error", event.Error)
		assert.Contains(t, event.StackTrace, "stack trace")
		assert.Equal(t, EventTypeTemplateRenderFailed, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("TemplateRenderFailedEvent_WithCorrelation", func(t *testing.T) {
		event := NewTemplateRenderFailedEvent("template", "error", "stack",
			WithNewCorrelation())
		require.NotNil(t, event)
		assert.NotEmpty(t, event.CorrelationID())
		assert.NotEmpty(t, event.EventID())
	})
}

// TestValidationEvents tests validation.go event types.
func TestValidationEvents(t *testing.T) {
	t.Run("ValidationStartedEvent", func(t *testing.T) {
		event := NewValidationStartedEvent()
		require.NotNil(t, event)
		assert.Equal(t, EventTypeValidationStarted, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("ValidationStartedEvent_WithCorrelation", func(t *testing.T) {
		event := NewValidationStartedEvent(WithCorrelation("corr-123", "cause-456"))
		require.NotNil(t, event)
		assert.Equal(t, "corr-123", event.CorrelationID())
		assert.Equal(t, "cause-456", event.CausationID())
	})

	t.Run("ValidationCompletedEvent", func(t *testing.T) {
		warnings := []string{"warning1", "warning2"}
		event := NewValidationCompletedEvent(warnings, 50, "config_change", true)
		require.NotNil(t, event)
		assert.Equal(t, warnings, event.Warnings)
		assert.Equal(t, int64(50), event.DurationMs)
		assert.Equal(t, "config_change", event.TriggerReason)
		assert.Equal(t, EventTypeValidationCompleted, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("ValidationCompletedEvent_DefensiveCopy", func(t *testing.T) {
		warnings := []string{"warning1"}
		event := NewValidationCompletedEvent(warnings, 50, "", true)

		// Modify original
		warnings[0] = "modified"

		// Event should have original value
		assert.Equal(t, "warning1", event.Warnings[0])
	})

	t.Run("ValidationCompletedEvent_EmptyWarnings", func(t *testing.T) {
		event := NewValidationCompletedEvent(nil, 50, "", true)
		require.NotNil(t, event)
		assert.Nil(t, event.Warnings)
	})

	t.Run("ValidationFailedEvent", func(t *testing.T) {
		errors := []string{"error1", "error2"}
		event := NewValidationFailedEvent(errors, 100, "drift_prevention")
		require.NotNil(t, event)
		assert.Equal(t, errors, event.Errors)
		assert.Equal(t, int64(100), event.DurationMs)
		assert.Equal(t, "drift_prevention", event.TriggerReason)
		assert.Equal(t, EventTypeValidationFailed, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("ValidationFailedEvent_DefensiveCopy", func(t *testing.T) {
		errors := []string{"error1"}
		event := NewValidationFailedEvent(errors, 100, "")

		// Modify original
		errors[0] = "modified"

		// Event should have original value
		assert.Equal(t, "error1", event.Errors[0])
	})

	t.Run("ValidationFailedEvent_EmptyErrors", func(t *testing.T) {
		event := NewValidationFailedEvent(nil, 100, "")
		require.NotNil(t, event)
		assert.Nil(t, event.Errors)
	})

	t.Run("ValidationTestsStartedEvent", func(t *testing.T) {
		event := NewValidationTestsStartedEvent(10)
		require.NotNil(t, event)
		assert.Equal(t, 10, event.TestCount)
		assert.Equal(t, EventTypeValidationTestsStarted, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("ValidationTestsCompletedEvent", func(t *testing.T) {
		event := NewValidationTestsCompletedEvent(10, 8, 2, 500)
		require.NotNil(t, event)
		assert.Equal(t, 10, event.TotalTests)
		assert.Equal(t, 8, event.PassedTests)
		assert.Equal(t, 2, event.FailedTests)
		assert.Equal(t, int64(500), event.DurationMs)
		assert.Equal(t, EventTypeValidationTestsCompleted, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("ValidationTestsFailedEvent", func(t *testing.T) {
		failedTests := []string{"test1", "test2"}
		event := NewValidationTestsFailedEvent(failedTests)
		require.NotNil(t, event)
		assert.Equal(t, failedTests, event.FailedTests)
		assert.Equal(t, EventTypeValidationTestsFailed, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("ValidationTestsFailedEvent_DefensiveCopy", func(t *testing.T) {
		failedTests := []string{"test1"}
		event := NewValidationTestsFailedEvent(failedTests)

		// Modify original
		failedTests[0] = "modified"

		// Event should have original value
		assert.Equal(t, "test1", event.FailedTests[0])
	})

	t.Run("ValidationTestsFailedEvent_EmptyTests", func(t *testing.T) {
		event := NewValidationTestsFailedEvent(nil)
		require.NotNil(t, event)
		assert.Nil(t, event.FailedTests)
	})
}

// TestDeploymentEvents tests deployment.go event types.
func TestDeploymentEvents(t *testing.T) {
	t.Run("DeploymentStartedEvent", func(t *testing.T) {
		endpoints := []interface{}{"endpoint1", "endpoint2"}
		event := NewDeploymentStartedEvent(endpoints)
		require.NotNil(t, event)
		assert.Len(t, event.Endpoints, 2)
		assert.Equal(t, "endpoint1", event.Endpoints[0])
		assert.Equal(t, EventTypeDeploymentStarted, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("DeploymentStartedEvent_DefensiveCopy", func(t *testing.T) {
		endpoints := []interface{}{"endpoint1"}
		event := NewDeploymentStartedEvent(endpoints)

		// Modify original
		endpoints[0] = "modified"

		// Event should have original value
		assert.Equal(t, "endpoint1", event.Endpoints[0])
	})

	t.Run("DeploymentStartedEvent_EmptyEndpoints", func(t *testing.T) {
		event := NewDeploymentStartedEvent(nil)
		require.NotNil(t, event)
		assert.Nil(t, event.Endpoints)
	})

	t.Run("DeploymentStartedEvent_WithCorrelation", func(t *testing.T) {
		event := NewDeploymentStartedEvent(nil, WithCorrelation("corr-123", "cause-456"))
		require.NotNil(t, event)
		assert.Equal(t, "corr-123", event.CorrelationID())
		assert.Equal(t, "cause-456", event.CausationID())
	})

	t.Run("InstanceDeployedEvent", func(t *testing.T) {
		event := NewInstanceDeployedEvent("endpoint1", 100, true)
		require.NotNil(t, event)
		assert.Equal(t, "endpoint1", event.Endpoint)
		assert.Equal(t, int64(100), event.DurationMs)
		assert.True(t, event.ReloadRequired)
		assert.Equal(t, EventTypeInstanceDeployed, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("InstanceDeployedEvent_WithCorrelation", func(t *testing.T) {
		event := NewInstanceDeployedEvent("ep", 50, false, WithNewCorrelation())
		require.NotNil(t, event)
		assert.NotEmpty(t, event.CorrelationID())
	})

	t.Run("InstanceDeploymentFailedEvent", func(t *testing.T) {
		event := NewInstanceDeploymentFailedEvent("endpoint1", "connection refused", true)
		require.NotNil(t, event)
		assert.Equal(t, "endpoint1", event.Endpoint)
		assert.Equal(t, "connection refused", event.Error)
		assert.True(t, event.Retryable)
		assert.Equal(t, EventTypeInstanceDeploymentFailed, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("InstanceDeploymentFailedEvent_WithCorrelation", func(t *testing.T) {
		event := NewInstanceDeploymentFailedEvent("ep", "error", false,
			WithCorrelation("corr", "cause"))
		require.NotNil(t, event)
		assert.Equal(t, "corr", event.CorrelationID())
	})

	t.Run("DeploymentCompletedEvent", func(t *testing.T) {
		event := NewDeploymentCompletedEvent(DeploymentResult{
			Total:              10,
			Succeeded:          8,
			Failed:             2,
			DurationMs:         500,
			ReloadsTriggered:   3,
			TotalAPIOperations: 25,
		})
		require.NotNil(t, event)
		assert.Equal(t, 10, event.Total)
		assert.Equal(t, 8, event.Succeeded)
		assert.Equal(t, 2, event.Failed)
		assert.Equal(t, int64(500), event.DurationMs)
		assert.Equal(t, 3, event.ReloadsTriggered)
		assert.Equal(t, 25, event.TotalAPIOperations)
		assert.Equal(t, EventTypeDeploymentCompleted, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("DeploymentCompletedEvent_WithCorrelation", func(t *testing.T) {
		event := NewDeploymentCompletedEvent(DeploymentResult{
			Total:              5,
			Succeeded:          5,
			DurationMs:         100,
			ReloadsTriggered:   5,
			TotalAPIOperations: 15,
		}, WithNewCorrelation())
		require.NotNil(t, event)
		assert.NotEmpty(t, event.CorrelationID())
	})

	t.Run("DeploymentScheduledEvent", func(t *testing.T) {
		endpoints := []interface{}{"ep1", "ep2"}
		auxFiles := map[string]string{"file": "content"}
		event := NewDeploymentScheduledEvent(
			"haproxy config",
			auxFiles,
			endpoints,
			"my-config",
			"default",
			"config_validation",
			true, // coalescible
		)
		require.NotNil(t, event)
		assert.Equal(t, "haproxy config", event.Config)
		assert.Equal(t, auxFiles, event.AuxiliaryFiles)
		assert.Len(t, event.Endpoints, 2)
		assert.Equal(t, "my-config", event.RuntimeConfigName)
		assert.Equal(t, "default", event.RuntimeConfigNamespace)
		assert.Equal(t, "config_validation", event.Reason)
		assert.Equal(t, EventTypeDeploymentScheduled, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("DeploymentScheduledEvent_DefensiveCopy", func(t *testing.T) {
		endpoints := []interface{}{"ep1"}
		event := NewDeploymentScheduledEvent("cfg", nil, endpoints, "n", "ns", "r", true)

		// Modify original
		endpoints[0] = "modified"

		// Event should have original value
		assert.Equal(t, "ep1", event.Endpoints[0])
	})

	t.Run("DeploymentScheduledEvent_WithCorrelation", func(t *testing.T) {
		event := NewDeploymentScheduledEvent("cfg", nil, nil, "n", "ns", "r", true,
			WithCorrelation("corr", "cause"))
		require.NotNil(t, event)
		assert.Equal(t, "corr", event.CorrelationID())
		assert.Equal(t, "cause", event.CausationID())
	})

	t.Run("DriftPreventionTriggeredEvent", func(t *testing.T) {
		event := NewDriftPreventionTriggeredEvent(30 * time.Minute)
		require.NotNil(t, event)
		assert.Equal(t, 30*time.Minute, event.TimeSinceLastDeployment)
		assert.Equal(t, EventTypeDriftPreventionTriggered, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})
}

// TestDiscoveryEvents tests discovery.go event types.
func TestDiscoveryEvents(t *testing.T) {
	t.Run("HAProxyPodsDiscoveredEvent", func(t *testing.T) {
		endpoints := []interface{}{"ep1", "ep2", "ep3"}
		event := NewHAProxyPodsDiscoveredEvent(endpoints, 3)
		require.NotNil(t, event)
		assert.Len(t, event.Endpoints, 3)
		assert.Equal(t, 3, event.Count)
		assert.Equal(t, EventTypeHAProxyPodsDiscovered, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("HAProxyPodsDiscoveredEvent_DefensiveCopy", func(t *testing.T) {
		endpoints := []interface{}{"ep1"}
		event := NewHAProxyPodsDiscoveredEvent(endpoints, 1)

		// Modify original
		endpoints[0] = "modified"

		// Event should have original value
		assert.Equal(t, "ep1", event.Endpoints[0])
	})

	t.Run("HAProxyPodsDiscoveredEvent_EmptyEndpoints", func(t *testing.T) {
		event := NewHAProxyPodsDiscoveredEvent(nil, 0)
		require.NotNil(t, event)
		assert.Nil(t, event.Endpoints)
		assert.Equal(t, 0, event.Count)
	})

	t.Run("HAProxyPodAddedEvent", func(t *testing.T) {
		event := NewHAProxyPodAddedEvent("endpoint1")
		require.NotNil(t, event)
		assert.Equal(t, "endpoint1", event.Endpoint)
		assert.Equal(t, EventTypeHAProxyPodAdded, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("HAProxyPodRemovedEvent", func(t *testing.T) {
		event := NewHAProxyPodRemovedEvent("endpoint1")
		require.NotNil(t, event)
		assert.Equal(t, "endpoint1", event.Endpoint)
		assert.Equal(t, EventTypeHAProxyPodRemoved, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("HAProxyPodTerminatedEvent", func(t *testing.T) {
		event := NewHAProxyPodTerminatedEvent("pod-1", "default")
		require.NotNil(t, event)
		assert.Equal(t, "pod-1", event.PodName)
		assert.Equal(t, "default", event.PodNamespace)
		assert.Equal(t, EventTypeHAProxyPodTerminated, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})
}

// TestPublishingEvents tests publishing.go event types.
func TestPublishingEvents(t *testing.T) {
	t.Run("ConfigPublishedEvent", func(t *testing.T) {
		event := NewConfigPublishedEvent("my-config", "default", 5, 2)
		require.NotNil(t, event)
		assert.Equal(t, "my-config", event.RuntimeConfigName)
		assert.Equal(t, "default", event.RuntimeConfigNamespace)
		assert.Equal(t, 5, event.MapFileCount)
		assert.Equal(t, 2, event.SecretCount)
		assert.Equal(t, EventTypeConfigPublished, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("ConfigPublishFailedEvent", func(t *testing.T) {
		err := assert.AnError
		event := NewConfigPublishFailedEvent(err)
		require.NotNil(t, event)
		assert.Equal(t, err.Error(), event.Error)
		assert.Equal(t, EventTypeConfigPublishFailed, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("ConfigPublishFailedEvent_NilError", func(t *testing.T) {
		event := NewConfigPublishFailedEvent(nil)
		require.NotNil(t, event)
		assert.Empty(t, event.Error)
	})

	t.Run("ConfigAppliedToPodEvent", func(t *testing.T) {
		syncMetadata := &SyncMetadata{
			ReloadTriggered:        true,
			ReloadID:               "reload-123",
			SyncDuration:           100 * time.Millisecond,
			VersionConflictRetries: 1,
			FallbackUsed:           false,
			OperationCounts: OperationCounts{
				TotalAPIOperations: 10,
				BackendsAdded:      2,
				ServersAdded:       5,
			},
		}
		event := NewConfigAppliedToPodEvent(
			"my-config",
			"default",
			"pod-1",
			"haproxy",
			"checksum-abc",
			false,
			syncMetadata,
		)
		require.NotNil(t, event)
		assert.Equal(t, "my-config", event.RuntimeConfigName)
		assert.Equal(t, "default", event.RuntimeConfigNamespace)
		assert.Equal(t, "pod-1", event.PodName)
		assert.Equal(t, "haproxy", event.PodNamespace)
		assert.Equal(t, "checksum-abc", event.Checksum)
		assert.False(t, event.IsDriftCheck)
		assert.NotNil(t, event.SyncMetadata)
		assert.True(t, event.SyncMetadata.ReloadTriggered)
		assert.Equal(t, "reload-123", event.SyncMetadata.ReloadID)
		assert.Equal(t, 10, event.SyncMetadata.OperationCounts.TotalAPIOperations)
		assert.Equal(t, EventTypeConfigAppliedToPod, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("ConfigAppliedToPodEvent_DriftCheck", func(t *testing.T) {
		event := NewConfigAppliedToPodEvent(
			"my-config", "default", "pod-1", "haproxy", "checksum-abc", true, nil)
		require.NotNil(t, event)
		assert.True(t, event.IsDriftCheck)
		assert.Nil(t, event.SyncMetadata)
	})
}

// TestWebhookEvents tests webhook.go event types.
func TestWebhookEvents(t *testing.T) {
	t.Run("WebhookValidationRequest", func(t *testing.T) {
		obj := map[string]interface{}{"metadata": map[string]interface{}{"name": "test"}}
		event := NewWebhookValidationRequest(
			"networking.k8s.io/v1.Ingress",
			"default",
			"my-ingress",
			obj,
			"CREATE",
		)
		require.NotNil(t, event)
		assert.NotEmpty(t, event.ID)
		assert.Equal(t, "networking.k8s.io/v1.Ingress", event.GVK)
		assert.Equal(t, "default", event.Namespace)
		assert.Equal(t, "my-ingress", event.Name)
		assert.Equal(t, obj, event.Object)
		assert.Equal(t, "CREATE", event.Operation)
		assert.Equal(t, EventTypeWebhookValidationRequestSG, event.EventType())
		assert.Equal(t, event.ID, event.RequestID())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("WebhookValidationRequest_UniqueIDs", func(t *testing.T) {
		event1 := NewWebhookValidationRequest("gvk", "ns", "n", nil, "CREATE")
		event2 := NewWebhookValidationRequest("gvk", "ns", "n", nil, "CREATE")
		assert.NotEqual(t, event1.ID, event2.ID)
	})

	t.Run("WebhookValidationResponse_Allowed", func(t *testing.T) {
		event := NewWebhookValidationResponse("req-123", "dryrun", true, "")
		require.NotNil(t, event)
		assert.Equal(t, "req-123", event.RequestID())
		assert.Equal(t, "dryrun", event.ValidatorID)
		assert.Equal(t, "dryrun", event.Responder())
		assert.True(t, event.Allowed)
		assert.Empty(t, event.Reason)
		assert.Equal(t, EventTypeWebhookValidationResponseSG, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("WebhookValidationResponse_Denied", func(t *testing.T) {
		event := NewWebhookValidationResponse("req-123", "basic", false, "invalid configuration")
		require.NotNil(t, event)
		assert.Equal(t, "req-123", event.RequestID())
		assert.Equal(t, "basic", event.ValidatorID)
		assert.False(t, event.Allowed)
		assert.Equal(t, "invalid configuration", event.Reason)
	})
}

// TestWebhookObservabilityEvents tests webhookobservability.go event types.
func TestWebhookObservabilityEvents(t *testing.T) {
	t.Run("WebhookValidationRequestEvent", func(t *testing.T) {
		event := NewWebhookValidationRequestEvent(
			"uid-123",
			"Ingress",
			"my-ingress",
			"default",
			"CREATE",
		)
		require.NotNil(t, event)
		assert.Equal(t, "uid-123", event.RequestUID)
		assert.Equal(t, "Ingress", event.Kind)
		assert.Equal(t, "my-ingress", event.Name)
		assert.Equal(t, "default", event.Namespace)
		assert.Equal(t, "CREATE", event.Operation)
		assert.Equal(t, EventTypeWebhookValidationRequest, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("WebhookValidationAllowedEvent", func(t *testing.T) {
		event := NewWebhookValidationAllowedEvent("uid-123", "Ingress", "my-ingress", "default")
		require.NotNil(t, event)
		assert.Equal(t, "uid-123", event.RequestUID)
		assert.Equal(t, "Ingress", event.Kind)
		assert.Equal(t, "my-ingress", event.Name)
		assert.Equal(t, "default", event.Namespace)
		assert.Equal(t, EventTypeWebhookValidationAllowed, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("WebhookValidationDeniedEvent", func(t *testing.T) {
		event := NewWebhookValidationDeniedEvent(
			"uid-123",
			"Ingress",
			"my-ingress",
			"default",
			"invalid configuration",
		)
		require.NotNil(t, event)
		assert.Equal(t, "uid-123", event.RequestUID)
		assert.Equal(t, "Ingress", event.Kind)
		assert.Equal(t, "my-ingress", event.Name)
		assert.Equal(t, "default", event.Namespace)
		assert.Equal(t, "invalid configuration", event.Reason)
		assert.Equal(t, EventTypeWebhookValidationDenied, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})

	t.Run("WebhookValidationErrorEvent", func(t *testing.T) {
		event := NewWebhookValidationErrorEvent("uid-123", "Ingress", "timeout")
		require.NotNil(t, event)
		assert.Equal(t, "uid-123", event.RequestUID)
		assert.Equal(t, "Ingress", event.Kind)
		assert.Equal(t, "timeout", event.Error)
		assert.Equal(t, EventTypeWebhookValidationError, event.EventType())
		assert.False(t, event.Timestamp().IsZero())
	})
}

// TestCorrelation tests correlation.go functionality.
func TestCorrelation(t *testing.T) {
	t.Run("NewCorrelation_NoOptions", func(t *testing.T) {
		c := NewCorrelation()
		assert.NotEmpty(t, c.EventID())
		assert.Empty(t, c.CorrelationID())
		assert.Empty(t, c.CausationID())
	})

	t.Run("NewCorrelation_WithNewCorrelation", func(t *testing.T) {
		c := NewCorrelation(WithNewCorrelation())
		assert.NotEmpty(t, c.EventID())
		assert.NotEmpty(t, c.CorrelationID())
		assert.Empty(t, c.CausationID())
	})

	t.Run("NewCorrelation_WithCorrelation", func(t *testing.T) {
		c := NewCorrelation(WithCorrelation("corr-123", "cause-456"))
		assert.NotEmpty(t, c.EventID())
		assert.Equal(t, "corr-123", c.CorrelationID())
		assert.Equal(t, "cause-456", c.CausationID())
	})

	t.Run("NewCorrelation_MultipleOptions", func(t *testing.T) {
		// WithCorrelation should override WithNewCorrelation
		c := NewCorrelation(WithNewCorrelation(), WithCorrelation("corr-123", "cause-456"))
		assert.NotEmpty(t, c.EventID())
		assert.Equal(t, "corr-123", c.CorrelationID())
		assert.Equal(t, "cause-456", c.CausationID())
	})

	t.Run("NewCorrelation_UniqueEventIDs", func(t *testing.T) {
		c1 := NewCorrelation()
		c2 := NewCorrelation()
		assert.NotEqual(t, c1.EventID(), c2.EventID())
	})

	t.Run("PropagateCorrelation_FromCorrelatedEvent", func(t *testing.T) {
		// Create a source event with correlation
		sourceEvent := NewReconciliationTriggeredEvent("test", true, WithNewCorrelation())
		sourceCorrelationID := sourceEvent.CorrelationID()
		sourceEventID := sourceEvent.EventID()

		// Propagate correlation to a new event
		newEvent := NewReconciliationStartedEvent("test",
			PropagateCorrelation(sourceEvent))

		// New event should have same correlation ID
		assert.Equal(t, sourceCorrelationID, newEvent.CorrelationID())
		// New event's causation ID should be source's event ID
		assert.Equal(t, sourceEventID, newEvent.CausationID())
		// New event should have its own unique event ID
		assert.NotEqual(t, sourceEventID, newEvent.EventID())
	})

	t.Run("PropagateCorrelation_FromNonCorrelatedEvent", func(t *testing.T) {
		// Create a non-correlated source (just an interface{})
		source := "not a correlated event"

		opt := PropagateCorrelation(source)

		// Option should be empty
		assert.Empty(t, opt.correlationID)
		assert.Empty(t, opt.causationID)

		// When used, should not set any correlation
		c := NewCorrelation(opt)
		assert.Empty(t, c.CorrelationID())
		assert.Empty(t, c.CausationID())
	})

	t.Run("PropagateCorrelation_ChainPropagation", func(t *testing.T) {
		// Simulate a chain: Event1 -> Event2 -> Event3
		event1 := NewReconciliationTriggeredEvent("start", true, WithNewCorrelation())

		event2 := NewReconciliationStartedEvent("middle",
			PropagateCorrelation(event1))

		event3 := NewReconciliationCompletedEvent(100,
			PropagateCorrelation(event2))

		// All events should share the same correlation ID
		assert.Equal(t, event1.CorrelationID(), event2.CorrelationID())
		assert.Equal(t, event2.CorrelationID(), event3.CorrelationID())

		// Each event should have unique event ID
		assert.NotEqual(t, event1.EventID(), event2.EventID())
		assert.NotEqual(t, event2.EventID(), event3.EventID())

		// Causation chain: event1 -> event2 -> event3
		assert.Equal(t, event1.EventID(), event2.CausationID())
		assert.Equal(t, event2.EventID(), event3.CausationID())
	})

	t.Run("WithCorrelation_EmptyValues", func(t *testing.T) {
		opt := WithCorrelation("", "")
		c := NewCorrelation(opt)
		// Empty values should not be set
		assert.Empty(t, c.CorrelationID())
		assert.Empty(t, c.CausationID())
	})
}

// TestTimestampNotZero is a comprehensive test ensuring all events have non-zero timestamps.
func TestTimestampNotZero(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name  string
		event interface {
			Timestamp() time.Time
		}
	}{
		{"ControllerStarted", NewControllerStartedEvent("v1", "v2")},
		{"ControllerShutdown", NewControllerShutdownEvent("reason")},
		{"ConfigParsed", NewConfigParsedEvent(nil, nil, "v1", "v2")},
		{"ConfigValidationRequest", NewConfigValidationRequest(nil, "v1")},
		{"ConfigValidationResponse", NewConfigValidationResponse("req", "validator", true, nil)},
		{"ConfigValidated", NewConfigValidatedEvent(nil, nil, "v1", "v2")},
		{"ConfigInvalid", NewConfigInvalidEvent("v1", nil, nil)},
		{"ConfigResourceChanged", NewConfigResourceChangedEvent(nil)},
		{"ResourceIndexUpdated", NewResourceIndexUpdatedEvent("type", types.ChangeStats{})},
		{"ResourceSyncComplete", NewResourceSyncCompleteEvent("type", 0)},
		{"IndexSynchronized", NewIndexSynchronizedEvent(nil)},
		{"LeaderElectionStarted", NewLeaderElectionStartedEvent("id", "lease", "ns")},
		{"BecameLeader", NewBecameLeaderEvent("id")},
		{"LostLeadership", NewLostLeadershipEvent("id", "reason")},
		{"NewLeaderObserved", NewNewLeaderObservedEvent("id", false)},
		{"ReconciliationTriggered", NewReconciliationTriggeredEvent("reason", true)},
		{"ReconciliationStarted", NewReconciliationStartedEvent("trigger")},
		{"ReconciliationCompleted", NewReconciliationCompletedEvent(0)},
		{"ReconciliationFailed", NewReconciliationFailedEvent("error", "phase")},
		{"SecretResourceChanged", NewSecretResourceChangedEvent(nil)},
		{"CredentialsUpdated", NewCredentialsUpdatedEvent(nil, "v1")},
		{"CredentialsInvalid", NewCredentialsInvalidEvent("v1", "error")},
		{"StorageSyncStarted", NewStorageSyncStartedEvent("phase", nil)},
		{"StorageSyncCompleted", NewStorageSyncCompletedEvent("phase", nil, 0)},
		{"StorageSyncFailed", NewStorageSyncFailedEvent("phase", "error")},
		{"CertResourceChanged", NewCertResourceChangedEvent(nil)},
		{"CertParsed", NewCertParsedEvent(nil, nil, "v1")},
		{"HTTPResourceUpdated", NewHTTPResourceUpdatedEvent("url", "checksum", 0)},
		{"HTTPResourceAccepted", NewHTTPResourceAcceptedEvent("url", "checksum", 0)},
		{"HTTPResourceRejected", NewHTTPResourceRejectedEvent("url", "checksum", "error")},
		// Template events
		{"TemplateRendered", NewTemplateRenderedEvent("cfg", nil, 0, 0, "", true)},
		{"TemplateRenderFailed", NewTemplateRenderFailedEvent("name", "error", "stack")},
		// Validation events
		{"ValidationStarted", NewValidationStartedEvent()},
		{"ValidationCompleted", NewValidationCompletedEvent(nil, 0, "", true)},
		{"ValidationFailed", NewValidationFailedEvent(nil, 0, "")},
		{"ValidationTestsStarted", NewValidationTestsStartedEvent(0)},
		{"ValidationTestsCompleted", NewValidationTestsCompletedEvent(0, 0, 0, 0)},
		{"ValidationTestsFailed", NewValidationTestsFailedEvent(nil)},
		// Deployment events
		{"DeploymentStarted", NewDeploymentStartedEvent(nil)},
		{"InstanceDeployed", NewInstanceDeployedEvent(nil, 0, false)},
		{"InstanceDeploymentFailed", NewInstanceDeploymentFailedEvent(nil, "error", false)},
		{"DeploymentCompleted", NewDeploymentCompletedEvent(DeploymentResult{})},
		{"DeploymentScheduled", NewDeploymentScheduledEvent("cfg", nil, nil, "n", "ns", "r", true)},
		{"DriftPreventionTriggered", NewDriftPreventionTriggeredEvent(0)},
		// Discovery events
		{"HAProxyPodsDiscovered", NewHAProxyPodsDiscoveredEvent(nil, 0)},
		{"HAProxyPodAdded", NewHAProxyPodAddedEvent(nil)},
		{"HAProxyPodRemoved", NewHAProxyPodRemovedEvent(nil)},
		{"HAProxyPodTerminated", NewHAProxyPodTerminatedEvent("name", "ns")},
		// Publishing events
		{"ConfigPublished", NewConfigPublishedEvent("n", "ns", 0, 0)},
		{"ConfigPublishFailed", NewConfigPublishFailedEvent(nil)},
		{"ConfigAppliedToPod", NewConfigAppliedToPodEvent("n", "ns", "pod", "ns", "checksum", false, nil)},
		// Webhook events
		{"WebhookValidationRequest", NewWebhookValidationRequest("gvk", "ns", "n", nil, "CREATE")},
		{"WebhookValidationResponse", NewWebhookValidationResponse("req", "validator", true, "")},
		// Webhook observability events
		{"WebhookValidationRequestEvent", NewWebhookValidationRequestEvent("uid", "kind", "n", "ns", "op")},
		{"WebhookValidationAllowedEvent", NewWebhookValidationAllowedEvent("uid", "kind", "n", "ns")},
		{"WebhookValidationDeniedEvent", NewWebhookValidationDeniedEvent("uid", "kind", "n", "ns", "reason")},
		{"WebhookValidationErrorEvent", NewWebhookValidationErrorEvent("uid", "kind", "error")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := tt.event.Timestamp()
			assert.False(t, ts.IsZero(), "Timestamp should not be zero")
			assert.True(t, !ts.Before(now), "Timestamp should be >= test start time")
		})
	}
}
