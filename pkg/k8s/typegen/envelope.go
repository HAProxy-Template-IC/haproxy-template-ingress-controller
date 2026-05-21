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

package typegen

import "reflect"

// Field name constants — extracted so goconst stops flagging the
// repeated string literals across envelope.go and its test file.
// The Go-side identifiers (capitalised); JSON tags use the lower
// case form inline at the StructField site.
const (
	envFieldName        = "Name"
	envFieldNamespace   = "Namespace"
	envFieldLabels      = "Labels"
	envFieldAnnotations = "Annotations"
)

// EnvelopeType returns a minimal reflect.Type carrying the universal
// Kubernetes object shape every resource shares: apiVersion, kind,
// and a Metadata sub-struct with the four metadata fields chart
// templates touch most often. Spec / Status are NOT included.
//
// # Role
//
// The envelope is the canonical "minimal K8s object" reference
// shape. Two consumers:
//
//   - typebootstrap.injectObjectMetaIfMissing uses it as the
//     reference shape when a CRD's openAPIV3Schema declares
//     `metadata: {type: object}` with no properties (the apiserver
//     auto-validates ObjectMeta in that case, so CRD authors
//     routinely leave it bare). The bootstrap path then synthesises
//     a Metadata sub-struct matching the envelope's shape so
//     chart code like `gw.Metadata.Name` compiles even when the
//     CRD schema didn't spell it out.
//
//   - Tests use it as a stable reference for assertions about
//     the generated-type shape contract.
//
// # Not a fail-open fallback
//
// Earlier revisions of this package returned EnvelopeType from
// typebootstrap.Bootstrap whenever schema acquisition failed for
// a watched resource — the idea being that "Metadata-only" typed
// access would keep chart code limping along. That fallback was
// removed because templates touching Spec/Status hit engine
// compile errors against the envelope on every render, with no
// automatic recovery, producing a "controller alive but render
// broken forever" zombie state. Bootstrap is now fail-closed: a
// schema fetch failure surfaces as a hard iteration-startup
// error, so operators see the problem in pod status and fix the
// underlying RBAC / CRD / apiserver issue.
//
// # Why these fields specifically
//
//   - apiVersion / kind: every K8s object has them. Templates use
//     them for status patches and rendered-resource emission.
//
//   - metadata.name, metadata.namespace: the two universal identity
//     fields. Used by every chart macro that emits per-resource
//     names, keys, or status patches.
//
//   - metadata.labels, metadata.annotations: the two universal
//     selector / extension surfaces. Used by annotation-driven
//     libraries (haproxytech.yaml, haproxy-ingress.yaml) and by
//     label-selector-aware emitters.
//
// Generation, creationTimestamp, ownerReferences etc. are
// deliberately absent — they're not universal across the chart's
// access patterns, and adding them is a backwards-compatible
// extension if a future caller proves the need.
func EnvelopeType() reflect.Type {
	metaType := reflect.StructOf([]reflect.StructField{
		{Name: envFieldName, Type: reflect.TypeOf(""), Tag: `json:"name"`},
		{Name: envFieldNamespace, Type: reflect.TypeOf(""), Tag: `json:"namespace"`},
		{Name: envFieldLabels, Type: reflect.TypeOf(map[string]string{}), Tag: `json:"labels"`},
		{Name: envFieldAnnotations, Type: reflect.TypeOf(map[string]string{}), Tag: `json:"annotations"`},
	})
	return reflect.StructOf([]reflect.StructField{
		{Name: "ApiVersion", Type: reflect.TypeOf(""), Tag: `json:"apiVersion"`},
		{Name: "Kind", Type: reflect.TypeOf(""), Tag: `json:"kind"`},
		{Name: "Metadata", Type: metaType, Tag: `json:"metadata"`},
	})
}
