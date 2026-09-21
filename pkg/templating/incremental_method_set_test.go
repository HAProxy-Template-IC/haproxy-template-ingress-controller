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

package templating

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIncrementalSerializationCustomMethodSets(t *testing.T) {
	type plainString string
	type plainRecord struct{ Value string }
	tests := []struct {
		name   string
		value  any
		custom bool
	}{
		{"scalar", "value", false},
		{"named scalar", plainString("value"), false},
		{"map", map[string]any{"value": "text"}, false},
		{"slice", []string{"value"}, false},
		{"record", plainRecord{Value: "text"}, false},
		{"record pointer", &plainRecord{Value: "text"}, false},
		{"value marshaler", incrementalResourceKeyMarshaler("value"), true},
		{"pointer marshaler on a value", incrementalResourceKeyPointerMarshaler("value"), true},
		{"nil pointer marshaler", (*incrementalResourceKeyPointerMarshaler)(nil), true},
		{"value stringer", incrementalResourceKeyStringer("value"), true},
		{"pointer stringer on a value", incrementalResourceKeyPointerStringer("value"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.custom, incrementalSerializationUsesCustomMethod(reflect.TypeOf(tt.value)))
		})
	}
}
