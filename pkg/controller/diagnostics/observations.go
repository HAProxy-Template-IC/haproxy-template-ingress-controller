// Copyright 2026 Philipp Hossner
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

package diagnostics

import (
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	hapticv1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/debug"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	agentclient "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
)

func podView(pod *corev1.Pod) Pod {
	result := Pod{Identity: Identity{APIVersion: "v1", Kind: "Pod", Namespace: pod.Namespace,
		Name: pod.Name, UID: string(pod.UID), Generation: pod.Generation}, Containers: []Container{}}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			result.Ready = condition.Status == corev1.ConditionTrue
		}
	}
	for _, statuses := range [][]corev1.ContainerStatus{pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses} {
		for index := range statuses {
			status := &statuses[index]
			entry := Container{Name: status.Name, Image: status.Image, ImageID: status.ImageID,
				Ready: status.Ready, Restarts: status.RestartCount}
			switch {
			case status.State.Running != nil:
				entry.Phase = "Running"
			case status.State.Terminated != nil:
				entry.Phase, entry.Reason = "Terminated", identifier(status.State.Terminated.Reason)
			case status.State.Waiting != nil:
				entry.Phase, entry.Reason = "Waiting", identifier(status.State.Waiting.Reason)
			}
			result.Containers = append(result.Containers, entry)
		}
	}
	return result
}

func configurationView(object *unstructured.Unstructured) (Configuration, error) {
	result := Configuration{Resource: resourceView(object, "")}
	switch object.GetKind() {
	case "HAProxyTemplateConfig":
		var config hapticv1.HAProxyTemplateConfig
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, &config); err != nil {
			return result, err
		}
		result.ValidationStatus = identifier(config.Status.ValidationStatus)
		result.ValidationErrors = len(config.Status.ValidationErrors)
		result.ObservedGeneration = config.Status.ObservedGeneration
	case "HAProxyCfg":
		var config hapticv1.HAProxyCfg
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, &config); err != nil {
			return result, err
		}
		result.DesiredChecksum = identifier(config.Spec.Checksum)
		result.ObservedGeneration = config.Status.ObservedGeneration
		if config.Status.ValidationError != "" {
			result.ValidationErrors = 1
		}
		for index := range config.Status.DeployedToPods {
			pod := &config.Status.DeployedToPods[index]
			result.Deployments = append(result.Deployments, PodDeployment{Pod: pod.PodName, UID: pod.PodUID,
				Checksum: identifier(pod.Checksum), AppliedPlanID: identifier(pod.AppliedPlanID), RunningPlanID: identifier(pod.RunningPlanID),
				ConsecutiveErrors: pod.ConsecutiveErrors, HasError: pod.LastError != ""})
		}
	}
	return result, nil
}

func agentView(pod *corev1.Pod, state *api.State, configPath string) Agent {
	baseDir := argumentValue(pod, "agent", "--base-dir", "/etc/haproxy")
	relative, err := filepath.Rel(baseDir, configPath)
	checksum := ""
	if err == nil && relative != ".." && !strings.HasPrefix(relative, "../") {
		checksum = state.Files[relative].Digest
	}
	major, missing := agentclient.CheckSkew(state)
	return Agent{Pod: podView(pod), Observed: true, ProtocolVersion: state.APIVersion, PlanSchemaVersion: state.PlanSchemaVersion,
		AgentVersion: identifier(state.AgentVersion), HAProxyVersion: identifier(state.HAProxy.Version),
		WorkerRunning: state.HAProxy.HasWorkerIdentity(), WorkerGeneration: state.Generation,
		WorkerPID: state.HAProxy.WorkerPID, WorkerStartTimeUnixMicros: state.HAProxy.WorkerStartTimeUnixMicros,
		AppliedPlanID: identifier(state.AppliedPlanID), RunningPlanID: identifier(state.RunningPlanID),
		WorkerOpsPlanID: identifier(state.WorkerOpsPlanID), ConfigFileDigest: identifier(checksum), FileCount: len(state.Files),
		ReloadFallback:  major || len(missing) > 0,
		LastApplyFailed: state.LastApply != nil && !state.LastApply.OK, InvariantFailed: state.InvariantViolation != ""}
}

func controllerPipeline(view *Controller, pipeline *debug.PipelineStatus) {
	if phase := pipeline.Rendering; phase != nil {
		view.Rendering = Phase{Status: identifier(phase.Status), Timestamp: phase.Timestamp}
		if phase.Error != "" {
			view.Rendering.Errors = 1
		}
	}
	if phase := pipeline.Validation; phase != nil {
		view.Validation = Phase{Status: identifier(phase.Status), Timestamp: phase.Timestamp, Errors: len(phase.Errors)}
		view.ValidatedPlan = identifier(phase.PlanID)
	}
	if phase := pipeline.Deployment; phase != nil {
		view.Deployment = Phase{Status: identifier(phase.Status), Timestamp: phase.Timestamp, Errors: phase.EndpointsFailed}
	}
}

func identifier(value string) string {
	if conditionIdentifier.MatchString(value) {
		return value
	}
	return ""
}

func argumentValue(pod *corev1.Pod, containerName, name, fallback string) string {
	for _, containers := range [][]corev1.Container{pod.Spec.InitContainers, pod.Spec.Containers} {
		for index := range containers {
			container := &containers[index]
			if container.Name != containerName {
				continue
			}
			for index, arg := range container.Args {
				if value, found := strings.CutPrefix(arg, name+"="); found {
					return value
				}
				if arg == name && index+1 < len(container.Args) {
					return container.Args[index+1]
				}
			}
		}
	}
	return fallback
}
