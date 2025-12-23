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

package debug

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haptic/pkg/core/config"
	"haptic/pkg/dataplane"
	"haptic/pkg/dataplane/auxiliaryfiles"
)

// mockStateProvider implements StateProvider for testing.
type mockStateProvider struct {
	config           *config.Config
	configVersion    string
	configErr        error
	credentials      *config.Credentials
	credentialsVer   string
	credentialsErr   error
	renderedConfig   string
	renderedTime     time.Time
	renderedErr      error
	auxFiles         *dataplane.AuxiliaryFiles
	auxFilesTime     time.Time
	auxFilesErr      error
	resourceCounts   map[string]int
	resourceCountErr error
	pipelineStatus   *PipelineStatus
	pipelineErr      error
	validatedConfig  *ValidatedConfigInfo
	validatedErr     error
	errorSummary     *ErrorSummary
	errorSummaryErr  error
}

func (m *mockStateProvider) GetConfig() (*config.Config, string, error) {
	return m.config, m.configVersion, m.configErr
}

func (m *mockStateProvider) GetCredentials() (*config.Credentials, string, error) {
	return m.credentials, m.credentialsVer, m.credentialsErr
}

func (m *mockStateProvider) GetRenderedConfig() (string, time.Time, error) {
	return m.renderedConfig, m.renderedTime, m.renderedErr
}

func (m *mockStateProvider) GetAuxiliaryFiles() (*dataplane.AuxiliaryFiles, time.Time, error) {
	return m.auxFiles, m.auxFilesTime, m.auxFilesErr
}

func (m *mockStateProvider) GetResourceCounts() (map[string]int, error) {
	return m.resourceCounts, m.resourceCountErr
}

func (m *mockStateProvider) GetResourcesByType(_ string) ([]interface{}, error) {
	return nil, nil
}

func (m *mockStateProvider) GetPipelineStatus() (*PipelineStatus, error) {
	return m.pipelineStatus, m.pipelineErr
}

func (m *mockStateProvider) GetValidatedConfig() (*ValidatedConfigInfo, error) {
	return m.validatedConfig, m.validatedErr
}

func (m *mockStateProvider) GetErrors() (*ErrorSummary, error) {
	return m.errorSummary, m.errorSummaryErr
}

func TestConfigVar_Get_Success(t *testing.T) {
	testConfig := &config.Config{
		WatchedResources: map[string]config.WatchedResource{
			"services": {APIVersion: "v1", Resources: "services"},
		},
	}

	provider := &mockStateProvider{
		config:        testConfig,
		configVersion: "v123",
	}

	configVar := &ConfigVar{provider: provider}

	result, err := configVar.Get()

	require.NoError(t, err)
	data, ok := result.(map[string]interface{})
	require.True(t, ok)

	assert.Equal(t, testConfig, data["config"])
	assert.Equal(t, "v123", data["version"])
	assert.NotNil(t, data["updated"])
}

func TestConfigVar_Get_Error(t *testing.T) {
	provider := &mockStateProvider{
		configErr: errors.New("config not loaded yet"),
	}

	configVar := &ConfigVar{provider: provider}

	result, err := configVar.Get()

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "config not loaded")
}

func TestCredentialsVar_Get_Success(t *testing.T) {
	provider := &mockStateProvider{
		credentials: &config.Credentials{
			DataplaneUsername: "admin",
			DataplanePassword: "secret123",
		},
		credentialsVer: "v456",
	}

	credVar := &CredentialsVar{provider: provider}

	result, err := credVar.Get()

	require.NoError(t, err)
	data, ok := result.(map[string]interface{})
	require.True(t, ok)

	assert.Equal(t, "v456", data["version"])
	assert.True(t, data["has_dataplane_creds"].(bool))
	assert.NotNil(t, data["updated"])
	// Verify password is NOT exposed
	assert.NotContains(t, data, "password")
	assert.NotContains(t, data, "secret123")
}

func TestCredentialsVar_Get_NoCredentials(t *testing.T) {
	provider := &mockStateProvider{
		credentials:    &config.Credentials{},
		credentialsVer: "v456",
	}

	credVar := &CredentialsVar{provider: provider}

	result, err := credVar.Get()

	require.NoError(t, err)
	data := result.(map[string]interface{})
	assert.False(t, data["has_dataplane_creds"].(bool))
}

func TestCredentialsVar_Get_Error(t *testing.T) {
	provider := &mockStateProvider{
		credentialsErr: errors.New("credentials not loaded"),
	}

	credVar := &CredentialsVar{provider: provider}

	result, err := credVar.Get()

	require.Error(t, err)
	assert.Nil(t, result)
}

func TestRenderedVar_Get_Success(t *testing.T) {
	testConfig := "global\n  maxconn 2000\n"
	testTime := time.Now()

	provider := &mockStateProvider{
		renderedConfig: testConfig,
		renderedTime:   testTime,
	}

	renderedVar := &RenderedVar{provider: provider}

	result, err := renderedVar.Get()

	require.NoError(t, err)
	data := result.(map[string]interface{})

	assert.Equal(t, testConfig, data["config"])
	assert.Equal(t, testTime, data["timestamp"])
	assert.Equal(t, len(testConfig), data["size"])
}

func TestRenderedVar_Get_Error(t *testing.T) {
	provider := &mockStateProvider{
		renderedErr: errors.New("no config rendered yet"),
	}

	renderedVar := &RenderedVar{provider: provider}

	result, err := renderedVar.Get()

	require.Error(t, err)
	assert.Nil(t, result)
}

func TestAuxFilesVar_Get_Success(t *testing.T) {
	testAuxFiles := &dataplane.AuxiliaryFiles{
		SSLCertificates: []auxiliaryfiles.SSLCertificate{
			{Path: "/etc/haproxy/certs/cert.pem", Content: "cert-content"},
		},
		MapFiles: []auxiliaryfiles.MapFile{
			{Path: "/etc/haproxy/maps/hosts.map", Content: "host1 backend1"},
		},
		GeneralFiles: []auxiliaryfiles.GeneralFile{
			{Filename: "500.http", Content: "error page"},
		},
	}
	testTime := time.Now()

	provider := &mockStateProvider{
		auxFiles:     testAuxFiles,
		auxFilesTime: testTime,
	}

	auxVar := &AuxFilesVar{provider: provider}

	result, err := auxVar.Get()

	require.NoError(t, err)
	data := result.(map[string]interface{})

	assert.Equal(t, testAuxFiles, data["files"])
	assert.Equal(t, testTime, data["timestamp"])

	summary := data["summary"].(map[string]int)
	assert.Equal(t, 1, summary["ssl_count"])
	assert.Equal(t, 1, summary["map_count"])
	assert.Equal(t, 1, summary["general_count"])
}

func TestAuxFilesVar_Get_Error(t *testing.T) {
	provider := &mockStateProvider{
		auxFilesErr: errors.New("no aux files"),
	}

	auxVar := &AuxFilesVar{provider: provider}

	result, err := auxVar.Get()

	require.Error(t, err)
	assert.Nil(t, result)
}

func TestResourcesVar_Get_Success(t *testing.T) {
	provider := &mockStateProvider{
		resourceCounts: map[string]int{
			"ingresses":    5,
			"services":     12,
			"haproxy-pods": 2,
		},
	}

	resourcesVar := &ResourcesVar{provider: provider}

	result, err := resourcesVar.Get()

	require.NoError(t, err)
	counts := result.(map[string]int)

	assert.Equal(t, 5, counts["ingresses"])
	assert.Equal(t, 12, counts["services"])
	assert.Equal(t, 2, counts["haproxy-pods"])
}

func TestResourcesVar_Get_Error(t *testing.T) {
	provider := &mockStateProvider{
		resourceCountErr: errors.New("resource watcher not ready"),
	}

	resourcesVar := &ResourcesVar{provider: provider}

	result, err := resourcesVar.Get()

	require.Error(t, err)
	assert.Nil(t, result)
}

func TestPipelineVar_Get_Success(t *testing.T) {
	testStatus := &PipelineStatus{
		LastTrigger: &TriggerStatus{
			Timestamp: time.Now(),
			Reason:    "config_change",
		},
		Rendering: &RenderingStatus{
			Status:      "succeeded",
			ConfigBytes: 1234,
		},
		Validation: &ValidationStatus{
			Status: "succeeded",
		},
		Deployment: &DeploymentStatus{
			Status:             "succeeded",
			EndpointsTotal:     2,
			EndpointsSucceeded: 2,
		},
	}

	provider := &mockStateProvider{
		pipelineStatus: testStatus,
	}

	pipelineVar := &PipelineVar{provider: provider}

	result, err := pipelineVar.Get()

	require.NoError(t, err)
	status := result.(*PipelineStatus)

	assert.Equal(t, "config_change", status.LastTrigger.Reason)
	assert.Equal(t, "succeeded", status.Rendering.Status)
}

func TestPipelineVar_Get_Error(t *testing.T) {
	provider := &mockStateProvider{
		pipelineErr: errors.New("no pipeline run yet"),
	}

	pipelineVar := &PipelineVar{provider: provider}

	result, err := pipelineVar.Get()

	require.Error(t, err)
	assert.Nil(t, result)
}

func TestValidatedVar_Get_Success(t *testing.T) {
	testValidated := &ValidatedConfigInfo{
		Config:               "global\n  daemon\n",
		Timestamp:            time.Now(),
		ConfigBytes:          16,
		ValidationDurationMs: 150,
	}

	provider := &mockStateProvider{
		validatedConfig: testValidated,
	}

	validatedVar := &ValidatedVar{provider: provider}

	result, err := validatedVar.Get()

	require.NoError(t, err)
	info := result.(*ValidatedConfigInfo)

	assert.Equal(t, 16, info.ConfigBytes)
	assert.Equal(t, int64(150), info.ValidationDurationMs)
}

func TestValidatedVar_Get_Error(t *testing.T) {
	provider := &mockStateProvider{
		validatedErr: errors.New("no config validated yet"),
	}

	validatedVar := &ValidatedVar{provider: provider}

	result, err := validatedVar.Get()

	require.Error(t, err)
	assert.Nil(t, result)
}

func TestErrorsVar_Get_Success(t *testing.T) {
	testErrors := &ErrorSummary{
		HAProxyValidationError: &ErrorInfo{
			Timestamp: time.Now(),
			Errors:    []string{"[ALERT] parsing error"},
		},
		LastErrorTimestamp: time.Now(),
	}

	provider := &mockStateProvider{
		errorSummary: testErrors,
	}

	errorsVar := &ErrorsVar{provider: provider}

	result, err := errorsVar.Get()

	require.NoError(t, err)
	summary := result.(*ErrorSummary)

	require.NotNil(t, summary.HAProxyValidationError)
	assert.Len(t, summary.HAProxyValidationError.Errors, 1)
}

func TestErrorsVar_Get_Error(t *testing.T) {
	provider := &mockStateProvider{
		errorSummaryErr: errors.New("internal error"),
	}

	errorsVar := &ErrorsVar{provider: provider}

	result, err := errorsVar.Get()

	require.Error(t, err)
	assert.Nil(t, result)
}

func TestFullStateVar_Get_Success(t *testing.T) {
	testConfig := &config.Config{}
	testRendered := "global\n"
	testAuxFiles := &dataplane.AuxiliaryFiles{}

	provider := &mockStateProvider{
		config:         testConfig,
		configVersion:  "v1",
		renderedConfig: testRendered,
		renderedTime:   time.Now(),
		auxFiles:       testAuxFiles,
		auxFilesTime:   time.Now(),
		resourceCounts: map[string]int{"services": 5},
	}

	fullStateVar := &FullStateVar{
		provider:    provider,
		eventBuffer: nil,
	}

	result, err := fullStateVar.Get()

	require.NoError(t, err)
	data := result.(map[string]interface{})

	assert.NotNil(t, data["config"])
	assert.NotNil(t, data["rendered"])
	assert.NotNil(t, data["auxfiles"])
	assert.NotNil(t, data["resources"])
	assert.NotNil(t, data["snapshot_time"])
	assert.NotNil(t, data["recent_events"])
}

func TestFullStateVar_Get_PartialState(t *testing.T) {
	// Test that FullStateVar doesn't fail even if some state is unavailable
	provider := &mockStateProvider{
		configErr:        errors.New("not loaded"),
		renderedErr:      errors.New("not rendered"),
		auxFilesErr:      errors.New("not available"),
		resourceCountErr: errors.New("not ready"),
	}

	fullStateVar := &FullStateVar{
		provider:    provider,
		eventBuffer: nil,
	}

	result, err := fullStateVar.Get()

	// Should succeed even with errors - best effort approach
	require.NoError(t, err)
	data := result.(map[string]interface{})

	// Config should contain nil config due to error
	configData := data["config"].(map[string]interface{})
	assert.Nil(t, configData["config"])
}
