// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package statusapplier

import (
	"context"
	"errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/retry"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func (c *Component) applyStatusPayload(
	ctx context.Context,
	gvr schema.GroupVersionResource,
	patch *templating.StatusPatch,
	phase string,
	payload map[string]any,
	exactLineage bool,
	resourceVersion string,
) (*unstructured.Unstructured, error) {
	if patch.ListOwnership != "" {
		return c.applyOwnedLists(ctx, gvr, patch, phase, payload)
	}
	encoded, err := encodeStatusApplyPayload(patch, payload, exactLineage, resourceVersion)
	if err != nil {
		return nil, err
	}
	applied, err := c.applyStatus(ctx, gvr, patch, phase, encoded)
	if exactLineage && apierrors.IsConflict(err) {
		return c.retryStatusAtCurrentResourceVersion(ctx, gvr, patch, phase, payload, err)
	}
	return applied, err
}

func (c *Component) applyOwnedLists(
	ctx context.Context,
	gvr schema.GroupVersionResource,
	patch *templating.StatusPatch,
	phase string,
	payload map[string]any,
) (*unstructured.Unstructured, error) {
	if patch.UID == "" {
		return nil, errors.New("owned status lists require a source resource UID")
	}
	var applied *unstructured.Unstructured
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := c.dynamicClient.Resource(gvr).Namespace(patch.Namespace).Get(ctx, patch.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if string(current.GetUID()) != patch.UID || current.GetResourceVersion() == "" {
			return errors.New("owned status list target has changed or missing object lineage")
		}
		status, _, err := unstructured.NestedMap(current.Object, statusKey)
		if err != nil {
			return err
		}
		merged, err := templating.MergeStatusLists(payload, status, patch.ListOwnership)
		if err != nil {
			return err
		}
		encoded, err := encodeStatusApplyPayload(patch, merged, true, current.GetResourceVersion())
		if err != nil {
			return err
		}
		applied, err = c.applyStatus(ctx, gvr, patch, phase, encoded)
		return err
	})
	return applied, err
}
