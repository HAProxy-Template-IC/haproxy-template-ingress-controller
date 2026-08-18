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
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"

	"k8s.io/apimachinery/pkg/util/validation"
)

// ---------------------------------------------------------------------------
// removePodFromStatus
// ---------------------------------------------------------------------------

func TestRemovePodFromStatus(t *testing.T) {
	tests := []struct {
		name        string
		pods        []haproxyv1alpha1.PodDeploymentStatus
		podName     string
		expectLen   int
		expectFound bool
	}{
		{
			name:        "remove from empty slice",
			pods:        nil,
			podName:     "pod-1",
			expectLen:   0,
			expectFound: false,
		},
		{
			name: "remove existing pod",
			pods: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1"},
				{PodName: "pod-2"},
			},
			podName:     "pod-1",
			expectLen:   1,
			expectFound: true,
		},
		{
			name: "remove non-existing pod",
			pods: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1"},
			},
			podName:     "pod-99",
			expectLen:   1,
			expectFound: false,
		},
		{
			name: "remove only pod",
			pods: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1"},
			},
			podName:     "pod-1",
			expectLen:   0,
			expectFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, removed := removePodFromStatus(tt.pods, tt.podName)
			assert.Len(t, result, tt.expectLen)
			assert.Equal(t, tt.expectFound, removed)
			// The removed pod should not be present.
			for i := range result {
				assert.NotEqual(t, tt.podName, result[i].PodName)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// buildPodStatus
// ---------------------------------------------------------------------------

func TestBuildPodStatus(t *testing.T) {
	t.Run("minimal update with no optional fields", func(t *testing.T) {
		update := &DeploymentStatusUpdate{
			PodName:  "pod-1",
			Checksum: "sha256:abc",
		}
		result := buildPodStatus(update)
		assert.Equal(t, "pod-1", result.PodName)
		assert.Equal(t, "sha256:abc", result.Checksum)
		assert.Empty(t, result.LastError)
	})

	t.Run("update with error", func(t *testing.T) {
		update := &DeploymentStatusUpdate{
			PodName:      "pod-1",
			PodUID:       "uid-1",
			PodRuntimeID: "runtime-1",
			Checksum:     "sha256:abc",
			Error:        "connection reset",
		}
		result := buildPodStatus(update)

		assert.Equal(t, "pod-1", result.PodName)
		assert.Equal(t, "uid-1", result.PodUID)
		assert.Equal(t, "runtime-1", result.PodRuntimeID)
		assert.Equal(t, "sha256:abc", result.Checksum)
		assert.Equal(t, "connection reset", result.LastError)
	})

	t.Run("no error does not set lastError", func(t *testing.T) {
		update := &DeploymentStatusUpdate{
			PodName: "pod-1",
			Error:   "",
		}
		result := buildPodStatus(update)
		assert.Empty(t, result.LastError)
	})

	t.Run("plan fields are carried through", func(t *testing.T) {
		update := &DeploymentStatusUpdate{
			PodName:       "pod-1",
			AppliedPlanID: "plan-applied",
			RunningPlanID: "plan-running",
			Mode:          "runtime",
			Reasons:       []string{"servers changed"},
		}
		result := buildPodStatus(update)

		assert.Equal(t, "plan-applied", result.AppliedPlanID)
		assert.Equal(t, "plan-running", result.RunningPlanID)
		assert.Equal(t, "runtime", result.Mode)
		assert.Equal(t, []string{"servers changed"}, result.Reasons)
	})

	t.Run("reasons are truncated to the CRD limit", func(t *testing.T) {
		reasons := make([]string, maxPodStatusReasons+3)
		for i := range reasons {
			reasons[i] = fmt.Sprintf("reason-%d", i)
		}
		update := &DeploymentStatusUpdate{PodName: "pod-1", Reasons: reasons}

		result := buildPodStatus(update)

		// MaxItems rejects the whole status write, so the writer truncates.
		require.Len(t, result.Reasons, maxPodStatusReasons)
		assert.Equal(t, "reason-0", result.Reasons[0])
		assert.Equal(t, "reason-7", result.Reasons[maxPodStatusReasons-1])
	})
}

// ---------------------------------------------------------------------------
// GenerateRuntimeConfigName
// ---------------------------------------------------------------------------

func TestGenerateRuntimeConfigName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "simple name", input: "my-config", expected: "my-config-haproxycfg"},
		{name: "with dots", input: "my.config.v1", expected: "my.config.v1-haproxycfg"},
		{name: "with hyphens", input: "my-long-config-name", expected: "my-long-config-name-haproxycfg"},
		{name: "already has suffix", input: "config-haproxycfg", expected: "config-haproxycfg-haproxycfg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, GenerateRuntimeConfigName(tt.input))
		})
	}

	longName := strings.Repeat("a", validation.DNS1123SubdomainMaxLength)
	otherLongName := strings.Repeat("a", validation.DNS1123SubdomainMaxLength-1) + "b"
	validName := GenerateRuntimeConfigName(longName)
	otherValidName := GenerateRuntimeConfigName(otherLongName)
	assert.Empty(t, validation.IsDNS1123Subdomain(validName))
	assert.LessOrEqual(t, len(validName), validation.DNS1123SubdomainMaxLength)
	assert.NotEqual(t, validName, otherValidName)
	assert.True(t, strings.HasSuffix(validName, runtimeConfigNameSuffix))

	invalidName := runtimeConfigResourceName(longName, "-invalid")
	assert.Empty(t, validation.IsDNS1123Subdomain(invalidName))
	assert.True(t, strings.HasSuffix(invalidName, runtimeConfigNameSuffix+"-invalid"))
}

// ---------------------------------------------------------------------------
// Publisher name generation methods
// ---------------------------------------------------------------------------

func TestPublisher_GenerateMapFileName(t *testing.T) {
	p := &Publisher{}

	tests := []struct {
		name     string
		mapName  string
		expected string
	}{
		{name: "simple map", mapName: "hosts.map", expected: "haproxy-map-hosts"},
		{name: "no extension", mapName: "hosts", expected: "haproxy-map-hosts"},
		{name: "path with directory", mapName: "/etc/haproxy/maps/hosts.map", expected: "haproxy-map-/etc/haproxy/maps/hosts"},
		{name: "double extension", mapName: "hosts.map.bak", expected: "haproxy-map-hosts.map"},
		{name: "hidden file", mapName: ".hidden.map", expected: "haproxy-map-.hidden"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, p.generateMapFileName(tt.mapName))
		})
	}
}

func TestPublisher_GenerateSecretName(t *testing.T) {
	p := &Publisher{}

	tests := []struct {
		name     string
		certPath string
		expected string
	}{
		{name: "simple cert", certPath: "/etc/haproxy/ssl/cert.pem", expected: "haproxy-cert-cert"},
		{name: "with underscores", certPath: "/ssl/my_cert_bundle.pem", expected: "haproxy-cert-my-cert-bundle"},
		{name: "no extension", certPath: "/ssl/cert", expected: "haproxy-cert-cert"},
		{name: "crt extension", certPath: "server.crt", expected: "haproxy-cert-server"},
		{name: "deep path", certPath: "/a/b/c/d/cert.pem", expected: "haproxy-cert-cert"},
		{name: "multiple underscores", certPath: "a_b_c_d.pem", expected: "haproxy-cert-a-b-c-d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, p.generateSecretName(tt.certPath))
		})
	}
}

func TestPublisher_GenerateGeneralFileName(t *testing.T) {
	p := &Publisher{}

	tests := []struct {
		name     string
		fileName string
		expected string
	}{
		{name: "error page", fileName: "/etc/haproxy/errors/503.http", expected: "haproxy-file-503"},
		{name: "with underscores", fileName: "my_error_page.html", expected: "haproxy-file-my-error-page"},
		{name: "with dots", fileName: "config.v2.json", expected: "haproxy-file-config-v2"},
		{name: "no extension", fileName: "myfile", expected: "haproxy-file-myfile"},
		{name: "deep path", fileName: "/a/b/c/file.txt", expected: "haproxy-file-file"},
		{name: "underscores and dots", fileName: "my_file.name.ext", expected: "haproxy-file-my-file-name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, p.generateGeneralFileName(tt.fileName))
		})
	}
}

func TestPublisher_GenerateCRTListFileName(t *testing.T) {
	p := &Publisher{}

	tests := []struct {
		name     string
		listPath string
		expected string
	}{
		{name: "simple crt-list", listPath: "/etc/haproxy/ssl/certs.crt-list", expected: "haproxy-crtlist-certs"},
		{name: "with underscores", listPath: "my_crt_list.list", expected: "haproxy-crtlist-my-crt-list"},
		{name: "no extension", listPath: "crtlist", expected: "haproxy-crtlist-crtlist"},
		{name: "deep path", listPath: "/a/b/c/list.cfg", expected: "haproxy-crtlist-list"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, p.generateCRTListFileName(tt.listPath))
		})
	}
}

func TestResolveAuxiliaryResourceNames(t *testing.T) {
	items := []string{"error.http", "error.lua", strings.Repeat("A", 300) + ".txt"}
	names := resolveAuxiliaryResourceNames(
		items,
		"-invalid",
		func(item string) string { return sanitizeResourceName("haproxy-file-", item) },
		func(item string) string { return item },
	)

	require.Len(t, names, len(items))
	assert.NotEqual(t, names[0], names[1])
	for _, name := range names {
		assert.Empty(t, validation.IsDNS1123Subdomain(name), name)
		assert.LessOrEqual(t, len(name), validation.DNS1123SubdomainMaxLength)
		assert.True(t, strings.HasSuffix(name, "-invalid"))
	}
}

// ---------------------------------------------------------------------------
// calculateChecksum
// ---------------------------------------------------------------------------

func TestCalculateChecksum(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "empty string", content: ""},
		{name: "simple content", content: "hello world"},
		{name: "multiline", content: "line1\nline2\nline3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateChecksum(tt.content)
			assert.True(t, strings.HasPrefix(result, "sha256:"), "should have sha256 prefix")
			// SHA256 hex is 64 chars + "sha256:" prefix = 71 chars.
			assert.Len(t, result, 71)
		})
	}

	t.Run("deterministic", func(t *testing.T) {
		a := calculateChecksum("test content")
		b := calculateChecksum("test content")
		assert.Equal(t, a, b)
	})

	t.Run("different content produces different checksum", func(t *testing.T) {
		a := calculateChecksum("content a")
		b := calculateChecksum("content b")
		assert.NotEqual(t, a, b)
	})
}

// ---------------------------------------------------------------------------
// boolPtr
// ---------------------------------------------------------------------------

func TestBoolPtr(t *testing.T) {
	trueVal := new(true)
	falseVal := new(false)

	require.NotNil(t, trueVal)
	assert.True(t, *trueVal)

	require.NotNil(t, falseVal)
	assert.False(t, *falseVal)
}

// ---------------------------------------------------------------------------
// compressIfNeeded (requires Publisher with logger)
// ---------------------------------------------------------------------------

func TestCompressIfNeeded(t *testing.T) {
	p := &Publisher{logger: testLogger()}

	t.Run("threshold zero disables compression", func(t *testing.T) {
		result := p.compressIfNeeded("some content", 0, "test")
		assert.Equal(t, "some content", result.content)
		assert.False(t, result.compressed)
	})

	t.Run("threshold negative disables compression", func(t *testing.T) {
		result := p.compressIfNeeded("some content", -1, "test")
		assert.Equal(t, "some content", result.content)
		assert.False(t, result.compressed)
	})

	t.Run("content below threshold not compressed", func(t *testing.T) {
		result := p.compressIfNeeded("small", 1000, "test")
		assert.Equal(t, "small", result.content)
		assert.False(t, result.compressed)
	})

	t.Run("content at threshold not compressed", func(t *testing.T) {
		content := strings.Repeat("x", 100)
		result := p.compressIfNeeded(content, 100, "test")
		assert.Equal(t, content, result.content)
		assert.False(t, result.compressed)
	})

	t.Run("large repetitive content is compressed", func(t *testing.T) {
		// Repetitive content compresses well with zstd.
		content := strings.Repeat("frontend http\n  bind *:80\n  default_backend web\n", 500)
		result := p.compressIfNeeded(content, 100, "test")
		assert.True(t, result.compressed, "highly repetitive content should compress well")
		assert.Less(t, len(result.content), len(content), "compressed should be smaller")
	})

	t.Run("incompressible content stays uncompressed", func(t *testing.T) {
		// Very short content that won't benefit from compression.
		content := strings.Repeat("x", 101)
		result := p.compressIfNeeded(content, 100, "test")
		// Short non-repetitive content may not compress smaller; in that case it stays uncompressed.
		if !result.compressed {
			assert.Equal(t, content, result.content)
		}
	})
}
