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

package typebootstrap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// TestOfflineGVKResolver_EmptyByDefault pins the contract: a fresh
// resolver has no entries. Population is the caller's job (typically
// walking a --schema-dir and emitting one Register per CRD plural).
// The previous behaviour pre-loaded a hardcoded Gateway entry; that
// was removed when we deleted the parallel embedded-schemas code path
// in favour of a single offline source — see ADR-0010.
func TestOfflineGVKResolver_EmptyByDefault(t *testing.T) {
	r := NewOfflineGVKResolver()

	_, err := r.Resolve("gateway.networking.k8s.io/v1", "gateways")
	require.Error(t, err,
		"NewOfflineGVKResolver must return an empty resolver; the caller registers entries from --schema-dir")
}

// TestOfflineGVKResolver_UnknownReturnsHelpfulError pins the
// degradation path. Resources without a matching entry surface an
// error message that points at --schema-dir, so a chart author hitting
// this for the first time knows exactly where to add the missing CRD
// or OpenAPI v3 schema.
func TestOfflineGVKResolver_UnknownReturnsHelpfulError(t *testing.T) {
	r := NewOfflineGVKResolver()

	_, err := r.Resolve("custom.example.com/v1", "widgets")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--schema-dir",
		"error must direct chart authors to the schema-dir flag")
	assert.Contains(t, err.Error(), "HAPTIC_SCHEMA_DIR",
		"error must also mention the env-var form so CI configs are findable")
}

// TestOfflineGVKResolver_RegisterPopulates verifies that callers can
// register their own entries — the only way to populate the resolver
// now that the constructor returns empty. validate.go does exactly
// this by iterating DirFetcher.PluralsFor().
func TestOfflineGVKResolver_RegisterPopulates(t *testing.T) {
	r := NewOfflineGVKResolver().Register(
		"example.com/v1", "widgets",
		schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"})

	gvk, err := r.Resolve("example.com/v1", "widgets")
	require.NoError(t, err)
	assert.Equal(t, "Widget", gvk.Kind)
}
