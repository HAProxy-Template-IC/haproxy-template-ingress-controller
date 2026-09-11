// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package configpublisher

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/retry"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
)

func TestDeleteOwnedResource_RefreshesAuthorityAndIdentity(t *testing.T) {
	resource := schema.GroupResource{Group: "haproxy-haptic.org", Resource: "haproxymapfiles"}
	tests := []struct {
		name          string
		change        func(*metav1.PartialObjectMetadata)
		refreshErr    error
		superseded    bool
		wantDeleted   bool
		wantAuthority int
		wantErr       string
	}{
		{name: "status change", wantDeleted: true, wantAuthority: 2},
		{name: "replacement", wantAuthority: 1, change: func(obj *metav1.PartialObjectMetadata) { obj.UID = "replacement" }},
		{name: "new owner name", wantAuthority: 1, change: func(obj *metav1.PartialObjectMetadata) { obj.OwnerReferences[0].Name = "other" }},
		{name: "new owner uid", wantAuthority: 1, change: func(obj *metav1.PartialObjectMetadata) { obj.OwnerReferences[0].UID = "other" }},
		{name: "removed owner", wantAuthority: 1, change: func(obj *metav1.PartialObjectMetadata) { obj.OwnerReferences = nil }},
		{name: "removed label", wantAuthority: 1, change: func(obj *metav1.PartialObjectMetadata) { obj.Labels = nil }},
		{name: "already deleted", wantAuthority: 1, refreshErr: apierrors.NewNotFound(resource, "stale")},
		{name: "failed refresh", wantAuthority: 1, refreshErr: errors.New("refresh unavailable"), wantErr: "refresh unavailable"},
		{name: "new publication after refresh", superseded: true, wantAuthority: 2, wantErr: "publication superseded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, listed := cleanupMetadataFixture()
			current := changedCleanupMetadata(listed, tt.change)
			reads, deletes, authorityChecks := 0, 0, 0
			deleted := false
			err := deleteOwnedResource(t.Context(), owner, listed,
				func(context.Context) error {
					authorityChecks++
					if tt.superseded && reads > 0 {
						return errors.New("publication superseded")
					}
					return nil
				},
				func(_ context.Context, name string) (metav1.Object, error) {
					reads++
					assert.Equal(t, listed.Name, name)
					return current, tt.refreshErr
				},
				func(_ context.Context, name string, options metav1.DeleteOptions) error {
					deletes++
					assert.Equal(t, listed.Name, name)
					if deletes == 1 {
						assert.Equal(t, deletionOptions(listed), options)
						return apierrors.NewConflict(resource, name, errors.New("status changed"))
					}
					assert.Equal(t, deletionOptions(current), options)
					deleted = true
					return nil
				})
			requireCleanupError(t, err, tt.wantErr)
			assert.Equal(t, tt.wantDeleted, deleted)
			assert.Equal(t, 1, reads)
			assert.Equal(t, tt.wantAuthority, authorityChecks)
			if tt.wantDeleted {
				assert.Equal(t, 2, deletes)
			} else {
				assert.Equal(t, 1, deletes)
			}
		})
	}
}

func TestDeleteOwnedResource_RetryBoundsAndCancellation(t *testing.T) {
	resource := schema.GroupResource{Resource: "secrets"}
	tests := []struct {
		name       string
		conflicts  int
		deleteErr  error
		cancel     bool
		wantWrites int
		wantErr    bool
	}{
		{name: "no conflict", wantWrites: 1},
		{name: "already deleted", deleteErr: apierrors.NewNotFound(resource, "stale"), wantWrites: 1},
		{name: "two status changes", conflicts: 2, wantWrites: 3},
		{name: "continuous conflicts", conflicts: retry.DefaultRetry.Steps, wantWrites: retry.DefaultRetry.Steps, wantErr: true},
		{name: "not a conflict", deleteErr: errors.New("storage unavailable"), wantWrites: 1, wantErr: true},
		{name: "canceled after conflict", conflicts: 1, cancel: true, wantWrites: 1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			owner, listed := cleanupMetadataFixture()
			reads, writes, authorityChecks := 0, 0, 0
			err := deleteOwnedResource(ctx, owner, listed,
				func(context.Context) error { authorityChecks++; return nil },
				func(context.Context, string) (metav1.Object, error) {
					reads++
					current := listed.DeepCopy()
					current.ResourceVersion = strconv.Itoa(reads + 1)
					return current, nil
				},
				func(_ context.Context, name string, options metav1.DeleteOptions) error {
					writes++
					require.NotNil(t, options.Preconditions)
					assert.Equal(t, listed.UID, *options.Preconditions.UID)
					assert.Equal(t, strconv.Itoa(writes), *options.Preconditions.ResourceVersion)
					if tt.cancel {
						cancel()
					}
					if writes <= tt.conflicts {
						return apierrors.NewConflict(resource, name, errors.New("concurrent update"))
					}
					return tt.deleteErr
				})
			assert.Equal(t, tt.wantErr, err != nil, "cleanup error: %v", err)
			if tt.cancel {
				assert.ErrorIs(t, err, context.Canceled)
			}
			assert.Equal(t, tt.wantWrites, writes)
			assert.Equal(t, writes, authorityChecks)
			assert.Equal(t, writes-1, reads)
		})
	}
}

func changedCleanupMetadata(listed *metav1.PartialObjectMetadata, change func(*metav1.PartialObjectMetadata)) *metav1.PartialObjectMetadata {
	current := listed.DeepCopy()
	current.ResourceVersion = "2"
	if change != nil {
		change(current)
	}
	return current
}

func requireCleanupError(t *testing.T, err error, want string) {
	t.Helper()
	if want != "" {
		require.ErrorContains(t, err, want)
	} else {
		require.NoError(t, err)
	}
}

func cleanupMetadataFixture() (owner *haproxyv1alpha1.HAProxyCfg, file *metav1.PartialObjectMetadata) {
	owner = &haproxyv1alpha1.HAProxyCfg{ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "default", UID: "owner-uid"}}
	file = &metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{
		Name: "stale", Namespace: owner.Namespace, UID: "file-uid", ResourceVersion: "1",
		Labels: runtimeConfigLabels(owner), OwnerReferences: runtimeConfigOwnerRefs(owner),
	}}
	return owner, file
}
