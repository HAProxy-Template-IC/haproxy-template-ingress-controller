// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package publication

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
)

type retainedFixture struct {
	*fixture
	configs []api.HAProxyCfg
}

func retainedPublicationFixture(t *testing.T) *retainedFixture {
	t.Helper()
	f := publicationFixture(t)
	f.config.Name = "config"
	f.config.Namespace = "test"
	f.config.ResourceVersion = "10"
	f.config.OwnerReferences = []metav1.OwnerReference{{APIVersion: api.SchemeGroupVersion.String(), Kind: "HAProxyTemplateConfig", Name: "template", UID: "template-uid", Controller: new(true)}}
	checkpoint := f.config.DeepCopy()
	checkpoint.Name, checkpoint.UID = "checkpoint", "checkpoint-uid"
	checkpoint.Spec.RetainedPlan = "encoded-plan"
	checkpoint.Labels = map[string]string{annotationPrefix + "retained-for": "template-uid"}
	checkpoint.Status.AuxiliaryFiles.SSLCertificates[0].Name = "checkpoint-certificate"
	certificate := f.secrets[0].DeepCopy()
	certificate.Name = "checkpoint-certificate"
	certificate.OwnerReferences = []metav1.OwnerReference{{APIVersion: api.SchemeGroupVersion.String(), Kind: "HAProxyCfg", Name: checkpoint.Name, UID: checkpoint.UID, Controller: new(true)}}
	f.secrets = append(f.secrets, *certificate)
	f.config.Status.RetainedConfigs = []api.RetainedConfigReference{{Name: checkpoint.Name, UID: string(checkpoint.UID), Checksum: checkpoint.Spec.Checksum}}
	return &retainedFixture{fixture: f, configs: []api.HAProxyCfg{*f.config.DeepCopy(), *checkpoint}}
}

func TestRetainedPublicationRequiresCommittedCheckpointAndCleanup(t *testing.T) {
	for name, mutate := range map[string]func(*retainedFixture){
		"missing checkpoint": func(f *retainedFixture) { f.configs = f.configs[:1] },
		"wrong UID":          func(f *retainedFixture) { f.configs[1].UID = "replacement" },
		"wrong name":         func(f *retainedFixture) { f.configs[1].Name = "replacement" },
		"wrong checksum":     func(f *retainedFixture) { f.configs[1].Spec.Checksum = "wrong" },
		"missing plan":       func(f *retainedFixture) { f.configs[1].Spec.RetainedPlan = "" },
		"missing pointer":    func(f *retainedFixture) { f.config.Status.RetainedConfigs = nil },
		"duplicate pointer": func(f *retainedFixture) {
			f.config.Status.RetainedConfigs = append(f.config.Status.RetainedConfigs, f.config.Status.RetainedConfigs[0])
		},
		"missing acknowledgement": func(f *retainedFixture) { f.configs[1].Status.DeployedToPods = nil },
		"old acknowledgement": func(f *retainedFixture) {
			for i := range f.configs[1].Status.DeployedToPods {
				f.configs[1].Status.DeployedToPods[i].AppliedPlanID = "old"
			}
		},
		"missing certificates": func(f *retainedFixture) { f.secrets = f.secrets[:1] },
		"terminating certificate": func(f *retainedFixture) {
			f.secrets[1].DeletionTimestamp = new(metav1.Now())
		},
		"foreign owner":         func(f *retainedFixture) { f.configs[1].OwnerReferences[0].UID = "foreign" },
		"foreign namespace":     func(f *retainedFixture) { f.configs[1].Namespace = "foreign" },
		"missing auxiliary set": func(f *retainedFixture) { f.configs[1].Status.AuxiliaryFiles = nil },
		"changed observation":   func(f *retainedFixture) { f.configs[0].ResourceVersion = "11" },
	} {
		t.Run(name, func(t *testing.T) {
			f := retainedPublicationFixture(t)
			require.True(t, RetainedMatches(&f.config, f.configs, f.secrets))
			mutate(f)
			require.False(t, RetainedMatches(&f.config, f.configs, f.secrets))
		})
	}
}

func TestRetainedPublicationWaitsForCascadingSecretDeletion(t *testing.T) {
	f := retainedPublicationFixture(t)
	obsolete := f.configs[1].DeepCopy()
	obsolete.Name, obsolete.UID = "obsolete-checkpoint", "obsolete-uid"
	f.configs = append(f.configs, *obsolete)
	orphan := f.secrets[1].DeepCopy()
	orphan.Name = "obsolete-certificate"
	orphan.OwnerReferences[0].Name, orphan.OwnerReferences[0].UID = obsolete.Name, obsolete.UID
	f.secrets = append(f.secrets, *orphan)
	require.True(t, Matches(&f.config, f.agents, f.secrets, 2), "live publication alone misses checkpoint cleanup")
	require.False(t, RetainedMatches(&f.config, f.configs, f.secrets))
	f.configs = f.configs[:2]
	require.False(t, RetainedMatches(&f.config, f.configs, f.secrets), "deleted owner does not mean its certificate has disappeared")
	f.secrets = f.secrets[:2]
	require.True(t, RetainedMatches(&f.config, f.configs, f.secrets))
}

func TestRetainedPublicationKeepsReferencedHistoricalCertificates(t *testing.T) {
	f := retainedPublicationFixture(t)
	previous := f.configs[1].DeepCopy()
	previous.Name, previous.UID, previous.Spec.Checksum = "previous", "previous-uid", "previous-checksum"
	previous.Status.AuxiliaryFiles.SSLCertificates[0].Name = "previous-certificate"
	f.configs = append(f.configs, *previous)
	f.config.Status.RetainedConfigs = append(f.config.Status.RetainedConfigs, api.RetainedConfigReference{Name: previous.Name, UID: string(previous.UID), Checksum: previous.Spec.Checksum})
	certificate := f.secrets[1].DeepCopy()
	certificate.Name = "previous-certificate"
	certificate.OwnerReferences[0].Name, certificate.OwnerReferences[0].UID = previous.Name, previous.UID
	f.secrets = append(f.secrets, *certificate, corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "credentials"}})
	require.True(t, RetainedMatches(&f.config, f.configs, f.secrets))
}
