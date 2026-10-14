// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package kubeexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

func (c Client) WaitCRDsEstablished(ctx context.Context, names []string, timeout time.Duration) error {
	if len(names) == 0 {
		return errors.New("CRD establishment requires at least one resource")
	}
	cfg := testutil.FastWaitConfig()
	cfg.Timeout = timeout
	return testutil.Poll(ctx, cfg, "CRD establishment", func(ctx context.Context) (testutil.PollResult, error) {
		result, err := c.Run(ctx, nil, "get", "crd", "-o", "json")
		if err != nil {
			return testutil.PollFailed, fmt.Errorf("list CRDs: %w: %s", err, result.Combined)
		}
		return crdsEstablished([]byte(result.Stdout), names)
	})
}

func crdsEstablished(data []byte, names []string) (testutil.PollResult, error) {
	// CRDs can report conditions:null before establishment; decode it as a nil slice.
	var list apiextensionsv1.CustomResourceDefinitionList
	if err := json.Unmarshal(data, &list); err != nil {
		return testutil.PollFailed, fmt.Errorf("decode CRDs: %w", err)
	}
	byName := make(map[string]*apiextensionsv1.CustomResourceDefinition, len(list.Items))
	for i := range list.Items {
		byName[list.Items[i].Name] = &list.Items[i]
	}
	pending := false
	for _, resource := range names {
		_, name, qualified := strings.Cut(resource, "/")
		if !qualified {
			name = resource
		}
		crd := byName[name]
		if crd == nil || crd.DeletionTimestamp != nil {
			return testutil.PollFailed, fmt.Errorf("CRD %s is missing or deleting", name)
		}
		established, err := crdEstablished(crd)
		if err != nil {
			return testutil.PollFailed, err
		}
		pending = pending || !established
	}
	if pending {
		return testutil.PollPending, nil
	}
	return testutil.PollSucceeded, nil
}

func crdEstablished(crd *apiextensionsv1.CustomResourceDefinition) (bool, error) {
	established := false
	for _, condition := range crd.Status.Conditions {
		if condition.Type == apiextensionsv1.NamesAccepted && condition.Status == apiextensionsv1.ConditionFalse {
			return false, fmt.Errorf("CRD %s names rejected: %s", crd.Name, condition.Message)
		}
		if condition.Type == apiextensionsv1.Established && condition.Status == apiextensionsv1.ConditionTrue {
			established = true
		}
	}
	return established, nil
}
