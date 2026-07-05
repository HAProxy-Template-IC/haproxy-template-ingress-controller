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

package migratecheck

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

// legacyClassAnnotation is the deprecated way of declaring an Ingress's
// class before spec.ingressClassName existed. Source controllers still
// honour it, so migrate-check reads it as the effective class when
// spec.ingressClassName is unset. This is the Ingress kind's own well-known
// field, not a source-controller-specific name — attributing an Ingress to
// a source is done entirely through the data-driven detect rules.
const legacyClassAnnotation = "kubernetes.io/ingress.class"

// FromUnstructured reduces an Ingress object to the fields Classify needs:
// its identity, effective class, and annotations. The effective class is
// spec.ingressClassName when set, otherwise the legacy
// kubernetes.io/ingress.class annotation.
func FromUnstructured(u *unstructured.Unstructured) Ingress {
	ing := Ingress{
		Namespace:   u.GetNamespace(),
		Name:        u.GetName(),
		Annotations: u.GetAnnotations(),
	}

	if className, ok, _ := unstructured.NestedString(u.Object, "spec", "ingressClassName"); ok && className != "" {
		ing.Class = className
	} else if legacy, ok := ing.Annotations[legacyClassAnnotation]; ok {
		ing.Class = legacy
	}

	return ing
}
