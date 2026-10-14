// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/compression"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestVerifyMap(t *testing.T) {
	content := "example.com backend\n"
	checksum := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(content)))
	cfg := &haproxyv1alpha1.HAProxyCfg{
		ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "haptic", UID: "config-uid"},
		Status: haproxyv1alpha1.HAProxyCfgStatus{
			AuxiliaryFiles: &haproxyv1alpha1.AuxiliaryFileReferences{SetID: "content-sha256:current"},
		},
	}
	file := &haproxyv1alpha1.HAProxyMapFile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "map" + configpublisher.AuxiliaryContentSuffix("HAProxyMapFile", "host.map", checksum, false),
			Namespace: "haptic", UID: "map-uid",
			OwnerReferences: []metav1.OwnerReference{{APIVersion: "haproxy-haptic.org/v1alpha1", Kind: "HAProxyCfg",
				Name: cfg.Name, UID: cfg.UID, Controller: new(true)}},
		},
		Spec: haproxyv1alpha1.HAProxyMapFileSpec{MapName: "host.map", Path: "host.map", Entries: content, Checksum: checksum},
	}
	for _, test := range []struct {
		name   string
		mutate func(*haproxyv1alpha1.HAProxyCfg, *haproxyv1alpha1.HAProxyMapFile)
		want   string
	}{
		{name: "content identity", mutate: func(_ *haproxyv1alpha1.HAProxyCfg, _ *haproxyv1alpha1.HAProxyMapFile) {}},
		{name: "compressed content", mutate: func(_ *haproxyv1alpha1.HAProxyCfg, f *haproxyv1alpha1.HAProxyMapFile) {
			f.Spec.Compressed, f.Spec.Entries = true, compression.Compress(content)
		}},
		{name: "publication suffix", mutate: func(_ *haproxyv1alpha1.HAProxyCfg, f *haproxyv1alpha1.HAProxyMapFile) { f.Name += "-invalid" }},
		{name: "forged owner", mutate: func(_ *haproxyv1alpha1.HAProxyCfg, f *haproxyv1alpha1.HAProxyMapFile) {
			f.OwnerReferences[0].UID = "other"
		}, want: "not owned"},
		{name: "wrong namespace", mutate: func(_ *haproxyv1alpha1.HAProxyCfg, f *haproxyv1alpha1.HAProxyMapFile) { f.Namespace = "other" }, want: "not owned"},
		{name: "terminating map", mutate: func(_ *haproxyv1alpha1.HAProxyCfg, f *haproxyv1alpha1.HAProxyMapFile) {
			f.DeletionTimestamp = new(metav1.Now())
		}, want: "not owned"},
		{name: "corrupted bytes", mutate: func(_ *haproxyv1alpha1.HAProxyCfg, f *haproxyv1alpha1.HAProxyMapFile) { f.Spec.Entries = "corrupted" }, want: "differs from its checksum"},
		{name: "broken compression", mutate: func(_ *haproxyv1alpha1.HAProxyCfg, f *haproxyv1alpha1.HAProxyMapFile) { f.Spec.Compressed = true }, want: "decode map content"},
		{name: "wrong path", mutate: func(_ *haproxyv1alpha1.HAProxyCfg, f *haproxyv1alpha1.HAProxyMapFile) { f.Spec.Path = "other.map" }, want: "differs from its committed name"},
		{name: "forged name", mutate: func(_ *haproxyv1alpha1.HAProxyCfg, f *haproxyv1alpha1.HAProxyMapFile) { f.Name += "forged" }, want: "differs from its committed name"},
		{name: "legacy publication", mutate: func(c *haproxyv1alpha1.HAProxyCfg, f *haproxyv1alpha1.HAProxyMapFile) {
			c.Status.AuxiliaryFiles.SetID = "legacy-set"
			f.Annotations = map[string]string{configpublisher.AuxiliarySetIDAnnotationKey: "legacy-set"}
		}},
		{name: "wrong legacy set", mutate: func(c *haproxyv1alpha1.HAProxyCfg, _ *haproxyv1alpha1.HAProxyMapFile) {
			c.Status.AuxiliaryFiles.SetID = "legacy-set"
		}, want: "different auxiliary set"},
	} {
		t.Run(test.name, func(t *testing.T) {
			changedCfg, changedFile := cfg.DeepCopy(), file.DeepCopy()
			test.mutate(changedCfg, changedFile)
			err := verifyMap(changedCfg, changedFile)
			if test.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.want)
			}
		})
	}
}
