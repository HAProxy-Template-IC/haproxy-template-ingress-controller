// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package configpublisher

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	v1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func retainedPublishRequest(t *testing.T) PublishRequest {
	t.Helper()
	fixture := newConfigPublisherOutputFixture(t)
	req := basePublishRequest()
	req.OutputSnapshot = fixture.snapshot
	planID, err := fixture.snapshot.PlanID()
	require.NoError(t, err)
	req.ConfirmedPod = &v1.PodDeploymentStatus{PodName: "haproxy-0", PodUID: "pod-uid", PodRuntimeID: "container-id", Checksum: fixture.checksum, AppliedPlanID: planID, RunningPlanID: planID, WorkerOpsPlanID: planID, Mode: "reload"}
	return req
}

func TestRetainedReferencesPreserveOlderRunningVersions(t *testing.T) {
	newest := v1.RetainedConfigReference{Name: "newest", Checksum: "newest"}
	previous := v1.RetainedConfigReference{Name: "previous", Checksum: "previous"}
	unused := v1.RetainedConfigReference{Name: "unused", Checksum: "unused"}
	running := v1.RetainedConfigReference{Name: "running", Checksum: "running"}
	status := v1.HAProxyCfgStatus{
		RetainedConfigs: []v1.RetainedConfigReference{previous, unused, running},
		DeployedToPods:  []v1.PodDeploymentStatus{{PodName: "lagging", Checksum: running.Checksum}},
	}
	assert.Equal(t, []v1.RetainedConfigReference{newest, previous, running}, retainedReferences(newest, &status))
	status.DeployedToPods[0].Checksum = newest.Checksum
	assert.Equal(t, []v1.RetainedConfigReference{newest, previous}, retainedReferences(newest, &status))
}

func TestRetainedRoundTripPreservesEveryArtifactFamily(t *testing.T) {
	ctx, _, _, publisher := newTestPublisher(t)
	req := retainedPublishRequest(t)
	_, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	require.NoError(t, publisher.PublishRetained(ctx, &req))
	loaded, err := publisher.LoadRetained(ctx, req.TemplateConfigNamespace, req.TemplateConfigName, req.TemplateConfigUID)
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	want, err := req.OutputSnapshot.PlanSnapshot()
	require.NoError(t, err)
	wantPlan, err := want.SharedPlan()
	require.NoError(t, err)
	got, err := loaded[0].Output.PlanSnapshot()
	require.NoError(t, err)
	gotPlan, err := got.SharedPlan()
	require.NoError(t, err)
	assert.True(t, renderplan.ExactlyEqual(wantPlan, gotPlan))
	assert.Equal(t, *req.ConfirmedPod, loaded[0].ConfirmedPod)
	assert.NoError(t, loaded[0].Authority.ValidateSnapshot(loaded[0].Output))
}

func TestRetainedPublicationSurvivesNewUnacknowledgedRender(t *testing.T) {
	ctx, _, crd, publisher := newTestPublisher(t)
	req := retainedPublishRequest(t)
	_, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	require.NoError(t, publisher.PublishRetained(ctx, &req))
	before, err := publisher.LoadRetained(ctx, req.TemplateConfigNamespace, req.TemplateConfigName, req.TemplateConfigUID)
	require.NoError(t, err)
	unconfirmed := basePublishRequest()
	unconfirmed.Config = "not valid HAProxy configuration"
	unconfirmed.Checksum = "unconfirmed"
	_, err = publisher.PublishConfig(ctx, &unconfirmed)
	require.NoError(t, err)
	loaded, err := publisher.LoadRetained(ctx, req.TemplateConfigNamespace, req.TemplateConfigName, req.TemplateConfigUID)
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	assert.Equal(t, before[0].Reference, loaded[0].Reference)
	crd.ClearActions()
	_, err = publisher.LoadRetained(ctx, req.TemplateConfigNamespace, req.TemplateConfigName, req.TemplateConfigUID)
	require.NoError(t, err)
	for _, action := range crd.Actions() {
		assert.Equal(t, "get", action.GetVerb(), "recovery must not publish or clean up")
	}
}

func TestRetainedReceiptSurvivesPodRetirement(t *testing.T) {
	for _, discovered := range []bool{false, true} {
		t.Run(fmt.Sprintf("discovery=%t", discovered), func(t *testing.T) {
			ctx, _, crd, publisher := newTestPublisher(t)
			req := retainedPublishRequest(t)
			_, err := publisher.PublishConfig(ctx, &req)
			require.NoError(t, err)
			require.NoError(t, publisher.PublishRetained(ctx, &req))
			client := crd.HaproxyTemplateICV1alpha1().HAProxyCfgs(req.TemplateConfigNamespace)
			current, err := client.Get(ctx, GenerateRuntimeConfigName(req.TemplateConfigName), metav1.GetOptions{})
			require.NoError(t, err)
			current.Status.DeployedToPods = []v1.PodDeploymentStatus{*req.ConfirmedPod}
			_, err = client.UpdateStatus(ctx, current, metav1.UpdateOptions{})
			require.NoError(t, err)
			checkpoint, err := client.Get(ctx, current.Status.RetainedConfigs[0].Name, metav1.GetOptions{})
			require.NoError(t, err)
			stamp := *req.ConfirmedPod
			require.NoError(t, publisher.mutateAuxiliaryFilePods(ctx, checkpoint.Status.AuxiliaryFiles,
				func([]v1.PodDeploymentStatus) ([]v1.PodDeploymentStatus, bool) {
					return []v1.PodDeploymentStatus{stamp}, true
				}))
			filesBefore, err := publisher.readRetainedFiles(ctx, checkpoint)
			require.NoError(t, err)
			if discovered {
				err = publisher.ReconcileDeployedToPods(ctx, req.TemplateConfigNamespace, nil)
			} else {
				err = publisher.CleanupPodReferences(ctx, &PodCleanupRequest{
					Namespace: req.TemplateConfigNamespace, PodName: stamp.PodName, PodUID: stamp.PodUID,
				})
			}
			require.NoError(t, err)
			after, err := client.Get(ctx, checkpoint.Name, metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, checkpoint, after)
			filesAfter, err := publisher.readRetainedFiles(ctx, after)
			require.NoError(t, err)
			assert.Equal(t, filesBefore, filesAfter)
			current, err = client.Get(ctx, current.Name, metav1.GetOptions{})
			require.NoError(t, err)
			assert.Empty(t, current.Status.DeployedToPods, "live fleet status still drops departed pods")
			loaded, err := publisher.LoadRetained(ctx, req.TemplateConfigNamespace, req.TemplateConfigName, req.TemplateConfigUID)
			require.NoError(t, err)
			require.Len(t, loaded, 1)
			assert.Equal(t, stamp, loaded[0].ConfirmedPod)
		})
	}
}

func TestRetainedRefusesTamperedConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*v1.HAProxyCfg)
		message string
	}{
		{"content", func(c *v1.HAProxyCfg) { c.Spec.Content += "corrupt" }, "checkpoint section"},
		{"checksum", func(c *v1.HAProxyCfg) { c.Spec.Checksum = "forged" }, "content address differs"},
		{"plan", func(c *v1.HAProxyCfg) { c.Spec.RetainedPlan += "corrupt" }, "content address differs"},
		{"owner", func(c *v1.HAProxyCfg) { c.OwnerReferences[0].UID = "foreign" }, "owner or UID differs"},
		{"receipt", func(c *v1.HAProxyCfg) { c.Status.DeployedToPods = nil }, "no confirmed worker receipt"},
		{"pending reload", func(c *v1.HAProxyCfg) { c.Status.DeployedToPods[0].Mode = "scheduled" }, "no confirmed worker receipt"},
		{"auxiliary set", func(c *v1.HAProxyCfg) { c.Status.AuxiliaryFiles.SetID = "content-sha256:forged" }, "set differs"},
		{"missing reference", func(c *v1.HAProxyCfg) { c.Status.AuxiliaryFiles.MapFiles = nil }, "missing or differs"},
		{"foreign namespace", func(c *v1.HAProxyCfg) { c.Status.AuxiliaryFiles.MapFiles[0].Namespace = "foreign" }, "foreign kind or namespace"},
		{"repeated reference", func(c *v1.HAProxyCfg) {
			c.Status.AuxiliaryFiles.MapFiles = append(c.Status.AuxiliaryFiles.MapFiles, c.Status.AuxiliaryFiles.MapFiles[0])
		}, "reference is repeated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _, crd, publisher := newTestPublisher(t)
			req := retainedPublishRequest(t)
			_, err := publisher.PublishConfig(ctx, &req)
			require.NoError(t, err)
			require.NoError(t, publisher.PublishRetained(ctx, &req))
			client := crd.HaproxyTemplateICV1alpha1().HAProxyCfgs(req.TemplateConfigNamespace)
			main, err := client.Get(ctx, GenerateRuntimeConfigName(req.TemplateConfigName), metav1.GetOptions{})
			require.NoError(t, err)
			checkpoint, err := client.Get(ctx, main.Status.RetainedConfigs[0].Name, metav1.GetOptions{})
			require.NoError(t, err)
			tc.mutate(checkpoint)
			_, err = client.Update(ctx, checkpoint, metav1.UpdateOptions{})
			require.NoError(t, err)
			_, err = publisher.LoadRetained(ctx, req.TemplateConfigNamespace, req.TemplateConfigName, req.TemplateConfigUID)
			require.ErrorContains(t, err, tc.message)
		})
	}
}

func TestRetainedChecksSecretBytesRatherThanAnnotations(t *testing.T) {
	ctx, kube, crd, publisher := newTestPublisher(t)
	req := retainedPublishRequest(t)
	_, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	require.NoError(t, publisher.PublishRetained(ctx, &req))
	client := crd.HaproxyTemplateICV1alpha1().HAProxyCfgs(req.TemplateConfigNamespace)
	main, err := client.Get(ctx, GenerateRuntimeConfigName(req.TemplateConfigName), metav1.GetOptions{})
	require.NoError(t, err)
	checkpoint, err := client.Get(ctx, main.Status.RetainedConfigs[0].Name, metav1.GetOptions{})
	require.NoError(t, err)
	secretName := checkpoint.Status.AuxiliaryFiles.SSLCertificates[0].Name
	secret, err := kube.CoreV1().Secrets(req.TemplateConfigNamespace).Get(ctx, secretName, metav1.GetOptions{})
	require.NoError(t, err)
	secret.Data["certificate"] = []byte("tampered key material")
	_, err = kube.CoreV1().Secrets(req.TemplateConfigNamespace).Update(ctx, secret, metav1.UpdateOptions{})
	require.NoError(t, err)
	_, err = publisher.LoadRetained(ctx, req.TemplateConfigNamespace, req.TemplateConfigName, req.TemplateConfigUID)
	require.ErrorContains(t, err, "auxiliary checksum differs")
}

func TestRetainedIncompletePublicationNeverCommitsReference(t *testing.T) {
	ctx, _, crd, publisher := newTestPublisher(t)
	req := retainedPublishRequest(t)
	_, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	crd.PrependReactor("create", "haproxymapfiles", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("publication interrupted")
	})
	require.ErrorContains(t, publisher.PublishRetained(ctx, &req), "publication interrupted")
	_, err = publisher.LoadRetained(ctx, req.TemplateConfigNamespace, req.TemplateConfigName, req.TemplateConfigUID)
	require.ErrorContains(t, err, "no acknowledged retained configuration")
}

func TestRetainedRequiresRunningReceipt(t *testing.T) {
	ctx, _, _, publisher := newTestPublisher(t)
	req := retainedPublishRequest(t)
	req.ConfirmedPod.Mode = "scheduled"
	require.ErrorContains(t, publisher.PublishRetained(ctx, &req), "no worker acknowledgement")
	req.ConfirmedPod.Mode = "reload"
	req.ConfirmedPod.PodUID = ""
	require.ErrorContains(t, publisher.PublishRetained(ctx, &req), "no worker acknowledgement")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	req = retainedPublishRequest(t)
	require.Error(t, publisher.PublishRetained(cancelled, &req))
}

func TestRetainedRoundTripPreservesEmptyArtifactFamilies(t *testing.T) {
	for _, withList := range []bool{false, true} {
		t.Run(fmt.Sprint(withList), func(t *testing.T) {
			ctx, _, _, publisher := newTestPublisher(t)
			config := "global\n"
			plan := &renderplan.Plan{SchemaVersion: renderplan.SchemaVersion,
				Sections: []renderplan.Section{{Kind: renderplan.SectionKindCore, Name: "core#0", Text: config, TextKnown: true, TextDigest: renderplan.DigestString(config), Length: len(config)}},
				Files:    []renderplan.File{exactPublisherPlanFile(renderplan.ConfigFilePath, renderplan.FileKindConfig, true, config)}}
			files := &dataplane.AuxiliaryFiles{}
			if withList {
				files.GeneralFiles = []auxiliaryfiles.GeneralFile{{Filename: "blocked.acl", Path: "general/blocked.acl", Content: "192.0.2.1\n"}}
				plan.Files = append(plan.Files, exactPublisherPlanFile("general/blocked.acl", renderplan.FileKindGeneral, true, "192.0.2.1\n"))
			}
			plan.ComputeID()
			artifactAuthority := renderartifact.NewAuthority()
			artifacts, err := dataplane.BuildAuxiliaryFileSnapshot(artifactAuthority, nil, files)
			require.NoError(t, err)
			authority, err := renderoutput.NewAuthority(renderplan.NewAuthority(), artifactAuthority)
			require.NoError(t, err)
			output, err := renderoutput.NewSnapshot(authority, config, plan, artifacts, nil)
			require.NoError(t, err)
			checksum, err := output.ContentChecksum()
			require.NoError(t, err)
			req := basePublishRequest()
			req.OutputSnapshot = output
			req.ConfirmedPod = &v1.PodDeploymentStatus{PodName: "worker", PodUID: "uid", PodRuntimeID: "runtime", Checksum: checksum, AppliedPlanID: plan.ID, RunningPlanID: plan.ID, Mode: "reload"}
			_, err = publisher.PublishConfig(ctx, &req)
			require.NoError(t, err)
			require.NoError(t, publisher.PublishRetained(ctx, &req))
			loaded, err := publisher.LoadRetained(ctx, req.TemplateConfigNamespace, req.TemplateConfigName, req.TemplateConfigUID)
			require.NoError(t, err)
			require.Len(t, loaded, 1)
			require.Equal(t, checksum, loaded[0].Reference.Checksum)
		})
	}
}
