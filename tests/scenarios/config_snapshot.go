// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type ConfigSnapshot struct {
	Fingerprint string
	Content     []byte
}

func (s *Session) ConfigSnapshot(ctx context.Context) (*ConfigSnapshot, error) {
	var items []unstructured.Unstructured
	for _, kind := range []string{"haproxytemplateconfigs", "haproxytemplatelibraries", "haproxyvalidationtests"} {
		installed, err := s.Kube(ctx, nil, "get", "crd", kind+".haproxy-haptic.org", "--ignore-not-found", "-o", fieldName)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(installed.Stdout) == "" {
			continue
		}
		list, err := readJSON[unstructured.UnstructuredList](ctx, s, "get", kind)
		if err != nil {
			return nil, err
		}
		items = append(items, list.Items...)
	}
	return snapshot(items)
}

func snapshot(items []unstructured.Unstructured) (*ConfigSnapshot, error) {
	if len(items) == 0 {
		return nil, errors.New("no HAPTIC configuration objects to fingerprint")
	}
	type input struct {
		Kind string
		Name string
		Spec any
	}
	inputs := make([]input, 0, len(items))
	for i := range items {
		inputs = append(inputs, input{Kind: items[i].GetKind(), Name: items[i].GetName(), Spec: items[i].Object["spec"]})
	}
	slices.SortFunc(inputs, func(a, b input) int {
		if cmp := strings.Compare(a.Kind, b.Kind); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.Name, b.Name)
	})
	content, err := json.Marshal(inputs)
	if err != nil {
		return nil, err
	}
	checksum := sha256.Sum256(content)
	return &ConfigSnapshot{Fingerprint: hex.EncodeToString(checksum[:]), Content: content}, nil
}

func (snapshot *ConfigSnapshot) RequireUnchanged(current *ConfigSnapshot) error {
	if snapshot.Fingerprint != current.Fingerprint {
		return fmt.Errorf("rejected upgrade changed configuration: %s -> %s", snapshot.Fingerprint, current.Fingerprint)
	}
	return nil
}
