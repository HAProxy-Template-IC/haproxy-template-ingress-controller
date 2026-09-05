// Copyright 2026 Philipp Hossner
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

package renderer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

func TestDecodeIncrementalBindingsReturnsSortedDetachedBindings(t *testing.T) {
	encoded := []byte("{\"routes\":{\"enabled\":true,\"nested\":{\"first\":1,\"second\":2}},\"services\":{}}")
	watched := map[string]config.WatchedResource{
		"services": {},
		"routes":   {},
	}

	bindings, err := decodeIncrementalBindings("backend", encoded, watched)
	require.NoError(t, err)
	require.Equal(t, []incrementalBinding{
		{component: "backend", source: "routes", props: []byte("{\"enabled\":true,\"nested\":{\"first\":1,\"second\":2}}")},
		{component: "backend", source: "services", props: []byte("{}")},
	}, bindings)

	copy(encoded, "poison")
	assert.Equal(t, "{\"enabled\":true,\"nested\":{\"first\":1,\"second\":2}}", string(bindings[0].props))
	bindings[0].props[0] = '['
	assert.Equal(t, "{}", string(bindings[1].props))
}

func TestDecodeIncrementalBindingsAcceptsEmptyObject(t *testing.T) {
	bindings, err := decodeIncrementalBindings(
		"backend",
		[]byte("{}"),
		map[string]config.WatchedResource{"routes": {}},
	)
	require.NoError(t, err)
	assert.Empty(t, bindings)
}

func TestDecodeIncrementalBindingsRejectsInvalidData(t *testing.T) {
	tests := map[string]struct {
		encoded string
		want    string
	}{
		"empty": {
			want: "decoding incremental bindings",
		},
		"malformed": {
			encoded: "{\"routes\":",
			want:    "decoding incremental bindings",
		},
		"top-level null": {
			encoded: "null",
			want:    "must be a JSON object",
		},
		"top-level array": {
			encoded: "[]",
			want:    "must be a JSON object",
		},
		"trailing object": {
			encoded: "{} {}",
			want:    "must contain one JSON object",
		},
		"malformed trailing data": {
			encoded: "{} invalid",
			want:    "decoding trailing incremental bindings data",
		},
		"trailing newline": {
			encoded: "{}\n",
			want:    "canonical JSON",
		},
		"leading whitespace": {
			encoded: " {}",
			want:    "canonical JSON",
		},
		"unordered aliases": {
			encoded: "{\"services\":{},\"routes\":{}}",
			want:    "canonical JSON",
		},
		"unordered props": {
			encoded: "{\"routes\":{\"second\":2,\"first\":1}}",
			want:    "canonical JSON",
		},
		"duplicate alias": {
			encoded: "{\"routes\":{},\"routes\":{}}",
			want:    "canonical JSON",
		},
		"duplicate nested prop": {
			encoded: "{\"routes\":{\"key\":1,\"key\":2}}",
			want:    "canonical JSON",
		},
		"unknown alias": {
			encoded: "{\"unknown\":{}}",
			want:    "alias \"unknown\" is not a watched resource",
		},
		"null props": {
			encoded: "{\"routes\":null}",
			want:    "alias \"routes\" props must be a JSON object",
		},
		"array props": {
			encoded: "{\"routes\":[]}",
			want:    "alias \"routes\" props must be a JSON object",
		},
		"scalar props": {
			encoded: "{\"routes\":true}",
			want:    "alias \"routes\" props must be a JSON object",
		},
		"derived render mode prop": {
			encoded: `{"routes":{"renderMode":"admission"}}`,
			want:    "cannot supply derived renderMode",
		},
	}
	watched := map[string]config.WatchedResource{
		"routes":   {},
		"services": {},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := decodeIncrementalBindings("backend", []byte(test.encoded), watched)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func TestStaticIncrementalBindingUsesDetachedEmptyProps(t *testing.T) {
	first := staticIncrementalBinding("backend", "routes")
	second := staticIncrementalBinding("backend", "routes")
	assert.Equal(t, incrementalBinding{component: "backend", source: "routes", props: []byte("{}")}, first)

	first.props[0] = '['
	assert.Equal(t, "{}", string(second.props))
}
