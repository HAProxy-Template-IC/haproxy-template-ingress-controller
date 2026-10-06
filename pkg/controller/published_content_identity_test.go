// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package controller

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
)

func contentReference(kind string, file *publishedAuxFile) publishedAuxRef {
	checksum := file.checksum
	if kind != secretKind {
		checksum = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(file.content)))
	}
	return publishedAuxRef{name: "file" + configpublisher.AuxiliaryContentSuffix(kind, file.path, checksum, file.caFile)}
}

func TestPublishedContentIdentityRejectsChangedInputs(t *testing.T) {
	mapKind := publishedKind(t, haproxyMapFileGVR.String())
	original := publishedAuxFile{path: "/maps/routes.map", content: "original"}
	ref := contentReference(mapKind.kind, &original)
	for _, tc := range []struct {
		name string
		file publishedAuxFile
	}{
		{"content", publishedAuxFile{path: original.path, content: "changed"}},
		{"path", publishedAuxFile{path: "/maps/other.map", content: original.content}},
		{"role", publishedAuxFile{path: original.path, content: original.content, caFile: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var legacy *string
			err := validatePublishedFileIdentity("content-sha256:set", &legacy, mapKind, "mapFiles", ref, &tc.file)
			require.ErrorContains(t, err, "does not match")
		})
	}
	secret := publishedAuxFile{path: "/certs/site.pem", checksum: "sha256:certificate"}
	secretRef := contentReference(secretKind, &secret)
	secret.checksum = "sha256:other"
	var legacy *string
	err := validatePublishedFileIdentity("content-sha256:set", &legacy, publishedKind(t, secretGVR.String()), "sslCertificates", secretRef, &secret)
	require.ErrorContains(t, err, "does not match")
}

func TestPublishedContentIdentityWaitsForCompleteReplacement(t *testing.T) {
	published := newPublishedAuxFiles("haptic")
	kind := publishedKind(t, haproxyMapFileGVR.String())
	first := publishedAuxFile{path: "/maps/first.map", content: "old"}
	shared := publishedAuxFile{path: "/maps/shared.map", content: "unchanged"}
	firstRef, sharedRef := contentReference(kind.kind, &first), contentReference(kind.kind, &shared)
	published.setForGVR(kind.gvr.String(), map[string]publishedAuxFile{firstRef.name: first, sharedRef.name: shared})
	published.setCommit(&publishedAuxCommit{setID: "content-sha256:first", refs: map[string][]publishedAuxRef{"mapFiles": {firstRef, sharedRef}}})
	require.NoError(t, published.readinessError())
	old := publishedSnapshot(t, published)
	next := publishedAuxFile{path: first.path, content: "new"}
	nextRef := contentReference(kind.kind, &next)
	published.setCommit(&publishedAuxCommit{setID: "content-sha256:next", refs: map[string][]publishedAuxRef{"mapFiles": {nextRef, sharedRef}}})
	assert.Equal(t, old, publishedSnapshot(t, published))
	published.setForGVR(kind.gvr.String(), map[string]publishedAuxFile{nextRef.name: next, sharedRef.name: shared})
	assert.Equal(t, map[string]string{"first.map": "new", "shared.map": "unchanged"}, publishedSnapshot(t, published))
}

func TestPublishedContentIdentityRejectsDowngrade(t *testing.T) {
	published := newPublishedAuxFiles("haptic")
	published.setCommit(&publishedAuxCommit{setID: "content-sha256:new", refs: map[string][]publishedAuxRef{}})
	require.NoError(t, published.readinessError())
	published.setCommit(&publishedAuxCommit{setID: "sha256:old", refs: map[string][]publishedAuxRef{}})
	_, err := published.get()
	require.ErrorContains(t, err, "lost its content identity")
	published.setCommit(&publishedAuxCommit{setID: "content-sha256:new", refs: map[string][]publishedAuxRef{}})
	require.NoError(t, published.readinessError())
}
