// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package configpublisher

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned/fake"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// createOrUpdateSSLSecret must absorb a write conflict in-call via
// retry.RetryOnConflict, exactly like its three sibling CRD publish helpers
// (createOrUpdateMapFile / GeneralFile / CRTListFile). A concurrent writer
// (e.g. a brief two-leader overlap during a leadership handoff) makes the first
// Update fail with a Conflict; the helper must re-Get the fresh resourceVersion
// and retry rather than bubbling the error up and skipping the publish until the
// next reconcile.
func TestCreateOrUpdateSSLSecret_RetriesOnConflict(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()

	// Fail the first Update with a Conflict, then let subsequent ones through.
	var updateCalls int
	k8sClient.PrependReactor("update", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		updateCalls++
		if updateCalls == 1 {
			return true, nil, apierrors.NewConflict(
				schema.GroupResource{Resource: "secrets"}, "ssl-secret", assert.AnError)
		}
		return false, nil, nil // passthrough to the tracker
	})

	publisher := New(k8sClient, crdClient, testLogger())
	req := &PublishRequest{TemplateConfigNamespace: "default"}
	owner := &haproxyv1alpha1.HAProxyCfg{ObjectMeta: metav1.ObjectMeta{Name: "owner", UID: types.UID("u1")}}
	cert := auxiliaryfiles.SSLCertificate{Path: "/etc/haproxy/ssl/cert.pem"}

	// First publish creates the secret (Update reactor not hit on create).
	cert.Content = "v1-content"
	name, err := publisher.createOrUpdateSSLSecret(ctx, req, owner, cert)
	require.NoError(t, err)
	require.NotEmpty(t, name)

	// Second publish changes content → goes through the Update path, whose first
	// attempt is rejected with a Conflict. The retry must recover and succeed.
	cert.Content = "v2-content"
	_, err = publisher.createOrUpdateSSLSecret(ctx, req, owner, cert)
	require.NoError(t, err, "conflict on first Update must be retried, not surfaced")
	assert.GreaterOrEqual(t, updateCalls, 2, "the rejected Update must have been retried")

	// The retried write must have landed the new content.
	got, err := k8sClient.CoreV1().Secrets("default").Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, []byte("v2-content"), got.Data["certificate"])
}

// On Create, an AlreadyExists (a racing writer created the Secret between our
// Get-returns-NotFound and our Create) must be retried — re-Get the now-existing
// object and take the update path — not surfaced to the caller. retry.RetryOnConflict
// alone does NOT cover AlreadyExists (a different 409 reason than Conflict);
// retriableWrite + retry.OnError is what makes the create path's "retry to update"
// actually true. Regression guard for the Gitar review finding on !1062.
func TestCreateOrUpdateSSLSecret_RetriesOnAlreadyExistsCreate(t *testing.T) {
	ctx := context.Background()
	k8sClient := k8sfake.NewClientset()
	crdClient := fake.NewSimpleClientset()
	publisher := New(k8sClient, crdClient, testLogger())

	req := &PublishRequest{TemplateConfigNamespace: "default"}
	owner := &haproxyv1alpha1.HAProxyCfg{ObjectMeta: metav1.ObjectMeta{Name: "owner", UID: types.UID("u1")}}
	cert := auxiliaryfiles.SSLCertificate{Path: "/etc/haproxy/ssl/cert.pem", Content: "desired-cert"}
	name := publisher.generateSecretName(filepath.Base(cert.Path))

	secretsGVR := corev1.SchemeGroupVersion.WithResource("secrets")
	var createCalls int
	k8sClient.PrependReactor("create", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		createCalls++
		if createCalls == 1 {
			// Simulate a racing writer: the object now exists (with different
			// content), so OUR create loses with AlreadyExists.
			racing := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:        name,
					Namespace:   "default",
					Annotations: map[string]string{"haproxy-haptic.org/checksum": "from-racing-writer"},
				},
				Data: map[string][]byte{"certificate": []byte("racing-cert"), "path": []byte(cert.Path)},
			}
			require.NoError(t, k8sClient.Tracker().Create(secretsGVR, racing, "default"))
			return true, nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "secrets"}, name)
		}
		return false, nil, nil
	})

	gotName, err := publisher.createOrUpdateSSLSecret(ctx, req, owner, cert)
	require.NoError(t, err, "AlreadyExists on create must be retried into the update path, not surfaced")
	require.Equal(t, name, gotName)
	assert.Equal(t, 1, createCalls, "create attempted once; the retry takes the update path since the object now exists")

	// The retry must have re-Got the racing object and Updated it to OUR content.
	got, err := k8sClient.CoreV1().Secrets("default").Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, []byte("desired-cert"), got.Data["certificate"],
		"retry should re-Get the racing object and update it to the desired content")
}
