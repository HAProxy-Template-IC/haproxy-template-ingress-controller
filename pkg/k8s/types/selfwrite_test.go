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

package types

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var routesGR = schema.GroupResource{Group: "gateway.networking.k8s.io", Resource: "httproutes"}

func TestSelfWriteRegistry_RecordAndMatch(t *testing.T) {
	r := NewSelfWriteRegistry(0)
	r.Record(routesGR, "default", "route", "1001")

	assert.True(t, r.IsSelfWrite(routesGR, "default", "route", "1001"))
	assert.False(t, r.IsSelfWrite(routesGR, "default", "route", "1002"), "a newer version is somebody else's write")
	assert.False(t, r.IsSelfWrite(routesGR, "other", "route", "1001"), "namespace is part of the identity")
	assert.False(t, r.IsSelfWrite(schema.GroupResource{Group: "", Resource: "services"}, "default", "route", "1001"))
	assert.True(t, r.IsSelfWrite(routesGR, "default", "route", "1001"), "entries are not consumed by a lookup")
}

func TestSelfWriteRegistry_VersionAgnostic(t *testing.T) {
	// A write through v1beta1 and a watch on v1 see the same object and
	// resourceVersion; the key must not carry the API version.
	r := NewSelfWriteRegistry(0)
	r.Record(schema.GroupVersionResource{Group: "g", Version: "v1beta1", Resource: "things"}.GroupResource(), "ns", "n", "7")
	assert.True(t, r.IsSelfWrite(schema.GroupVersionResource{Group: "g", Version: "v1", Resource: "things"}.GroupResource(), "ns", "n", "7"))
}

func TestSelfWriteRegistry_NilAndEmptySafe(t *testing.T) {
	var r *SelfWriteRegistry
	r.Record(routesGR, "default", "route", "1")
	assert.False(t, r.IsSelfWrite(routesGR, "default", "route", "1"))

	r = NewSelfWriteRegistry(0)
	r.Record(routesGR, "default", "route", "")
	assert.False(t, r.IsSelfWrite(routesGR, "default", "route", ""), "an empty resourceVersion identifies nothing")
}

func TestSelfWriteRegistry_EvictsOldestAtLimit(t *testing.T) {
	r := NewSelfWriteRegistry(3)
	for i := 1; i <= 4; i++ {
		r.Record(routesGR, "default", "route", fmt.Sprint(i))
	}
	assert.False(t, r.IsSelfWrite(routesGR, "default", "route", "1"), "oldest entry is evicted first")
	for i := 2; i <= 4; i++ {
		assert.True(t, r.IsSelfWrite(routesGR, "default", "route", fmt.Sprint(i)))
	}
	r.Record(routesGR, "default", "route", "4") // duplicate: no eviction
	assert.True(t, r.IsSelfWrite(routesGR, "default", "route", "2"))
}
