// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package kindutil

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestValidatePodContainerImages(t *testing.T) {
	for _, test := range []struct {
		name   string
		images []string
		valid  bool
	}{
		{name: "all replicas", images: []string{"chart:v1", "chart:v1"}, valid: true},
		{name: "missing container"},
		{name: "wrong image", images: []string{"chart:v2"}},
		{name: "one wrong replica", images: []string{"chart:v1", "chart:v2"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			pods := make([]corev1.Pod, len(test.images))
			for index, image := range test.images {
				pods[index].Spec.Containers = []corev1.Container{
					{Name: "checked", Image: image}, {Name: "unrelated", Image: "other:v2"},
				}
			}
			err := ValidatePodContainerImages(pods, map[string]string{"checked": "chart:v1"})
			if test.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
