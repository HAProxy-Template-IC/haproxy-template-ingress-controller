// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package statusapplier

import (
	"crypto/sha256"
	"encoding/json"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type statusOwnership map[string][sha256.Size]byte

func statusFieldOwnership(applied *unstructured.Unstructured, apiVersion string) statusOwnership {
	result := statusOwnership{}
	seen := map[string]bool{}
	for _, entry := range applied.GetManagedFields() {
		if entry.APIVersion != apiVersion || entry.Subresource != statusKey ||
			entry.Operation != metav1.ManagedFieldsOperationApply || entry.FieldsType != "FieldsV1" ||
			entry.FieldsV1 == nil || !strings.HasPrefix(entry.Manager, fieldManagerPrefix+"-") {
			continue
		}
		if seen[entry.Manager] {
			delete(result, entry.Manager)
			continue
		}
		seen[entry.Manager] = true
		var fields map[string]map[string]any
		if json.Unmarshal(entry.FieldsV1.GetRawBytes(), &fields) != nil || len(fields["f:status"]) == 0 {
			continue
		}
		encoded, err := json.Marshal(fields["f:status"])
		if err == nil {
			result[entry.Manager] = sha256.Sum256(encoded)
		}
	}
	return result
}
