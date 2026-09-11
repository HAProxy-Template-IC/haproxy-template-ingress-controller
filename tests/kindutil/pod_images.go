// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package kindutil

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// ValidatePodContainerImages requires each named container and checks every replica.
func ValidatePodContainerImages(pods []corev1.Pod, expected map[string]string) error {
	seen := make(map[string]bool, len(expected))
	for podIndex := range pods {
		pod := &pods[podIndex]
		for containerIndex := range pod.Spec.Containers {
			container := &pod.Spec.Containers[containerIndex]
			image, checked := expected[container.Name]
			if !checked {
				continue
			}
			if container.Image != image {
				return fmt.Errorf("pod %s/%s container %s uses %q, not chart image %q; remove the test image override",
					pod.Namespace, pod.Name, container.Name, container.Image, image)
			}
			seen[container.Name] = true
		}
	}
	for name := range expected {
		if !seen[name] {
			return fmt.Errorf("chart container %q is absent; check the test deployment", name)
		}
	}
	return nil
}
