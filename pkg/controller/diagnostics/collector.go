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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/debug"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

const controllerRole = "controller"

type PodAccess interface {
	GetFromPod(context.Context, string, string, int64) ([]byte, error)
	Exec(context.Context, string, string, []string, int64) ([]byte, error)
}

type Options struct {
	Namespace        string
	Release          string
	ConfigName       string
	DebugPort        int
	MaxResources     int
	MaxResponseBytes int64
}

type Collector struct {
	options Options
	client  kubernetes.Interface
	dynamic dynamic.Interface
	access  func(int) PodAccess
}

func New(options *Options, client kubernetes.Interface, dyn dynamic.Interface, access func(int) PodAccess) (*Collector, error) {
	if options == nil || options.Namespace == "" || options.Release == "" || options.ConfigName == "" {
		return nil, errors.New("diagnostics require a namespace, release, and configuration name")
	}
	if len(validation.IsDNS1123Label(options.Namespace)) > 0 || len(validation.IsValidLabelValue(options.Release)) > 0 || len(validation.IsDNS1123Subdomain(options.ConfigName)) > 0 {
		return nil, errors.New("diagnostic namespace, release, or configuration name is invalid")
	}
	if options.MaxResources <= 0 || options.MaxResponseBytes <= 0 || options.DebugPort < 0 || options.DebugPort > 65535 {
		return nil, errors.New("diagnostic limits must be positive and the debug port must be between 0 and 65535")
	}
	if client == nil || dyn == nil || access == nil {
		return nil, errors.New("diagnostics require Kubernetes and pod clients")
	}
	return &Collector{options: *options, client: client, dynamic: dyn, access: access}, nil
}

func (c *Collector) Collect(ctx context.Context) *Report {
	report := &Report{SchemaVersion: 1, CollectedAt: time.Now().UTC(), Namespace: c.options.Namespace,
		Release: c.options.Release, Complete: true, Healthy: true, Configurations: []Configuration{},
		Controllers: []Controller{}, Agents: []Agent{}, Resources: []Resource{}, Findings: []Finding{}}
	configPath := c.collectConfigurations(ctx, report)
	for _, role := range []string{controllerRole, "loadbalancer"} {
		c.collectPods(ctx, report, role, configPath)
	}
	assessDeployments(report)
	return report
}

func (c *Collector) collectPods(ctx context.Context, report *Report, role, configPath string) {
	selector := "app.kubernetes.io/instance=" + c.options.Release + ",app.kubernetes.io/component=" + role
	pods, err := c.client.CoreV1().Pods(c.options.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		report.incomplete("pods-unavailable", role, "Allow listing the release's pods and retry.")
		return
	}
	count := 0
	for index := range pods.Items {
		pod := &pods.Items[index]
		if pod.DeletionTimestamp != nil {
			continue
		}
		count++
		if role == controllerRole {
			c.collectController(ctx, report, pod)
		} else {
			c.collectAgent(ctx, report, pod, configPath)
		}
	}
	if count == 0 {
		report.incomplete("fleet-empty", role, "Check the namespace, release name, and workload rollout.")
	}
}

func (c *Collector) collectController(ctx context.Context, report *Report, pod *corev1.Pod) {
	view := Controller{Pod: podView(pod)}
	port := c.options.DebugPort
	if port == 0 {
		port = controllerDebugPort(pod)
	}
	if port == 0 {
		report.incomplete("debug-port-unknown", pod.Name, "Set --debug-port to the controller's health and debug port.")
	} else {
		access := c.access(port)
		pipeline, err := access.GetFromPod(ctx, pod.Name, "/debug/vars/pipeline", c.options.MaxResponseBytes)
		var status *debug.PipelineStatus
		if err != nil || json.Unmarshal(pipeline, &status) != nil || status == nil {
			report.incomplete("pipeline-unavailable", pod.Name, "Check controller health, pods/portforward permission, and --max-response-bytes.")
		} else {
			view.Observed = true
			controllerPipeline(&view, status)
			for _, phase := range []Phase{view.Rendering, view.Validation, view.Deployment} {
				if phase.Status == "failed" {
					report.problem("pipeline-failed", pod.Name, "Inspect this controller's private debug output for the failed phase.")
					break
				}
			}
		}
		c.collectFailure(ctx, report, &view, access)
	}
	if !view.Ready {
		report.problem("controller-not-ready", pod.Name, "Inspect the controller's pod status and logs.")
	}
	report.Controllers = append(report.Controllers, view)
}

func (c *Collector) collectFailure(ctx context.Context, report *Report, view *Controller, access PodAccess) {
	data, err := access.GetFromPod(ctx, view.Name, "/debug/events?limit=100", c.options.MaxResponseBytes)
	var response struct {
		Events []Failure `json:"events"`
	}
	if err != nil || json.Unmarshal(data, &response) != nil {
		report.incomplete("events-unavailable", view.Name, "Check controller health, pods/portforward permission, and --max-response-bytes.")
		return
	}
	for index := range response.Events {
		event := &response.Events[index]
		if !strings.HasSuffix(event.Type, ".failed") && !strings.HasSuffix(event.Type, ".rejected") {
			continue
		}
		if view.LastFailure != nil && !event.Timestamp.After(view.LastFailure.Timestamp) {
			continue
		}
		view.LastFailure = &Failure{Type: identifier(event.Type), Timestamp: event.Timestamp, CorrelationID: identifier(event.CorrelationID)}
	}
}

func (c *Collector) collectAgent(ctx context.Context, report *Report, pod *corev1.Pod, configPath string) {
	view := Agent{Pod: podView(pod)}
	data, err := c.access(0).Exec(ctx, pod.Name, "agent", []string{"haptic", "agent", "state", "--output", "json", "--verify", "--base-dir", argumentValue(pod, "agent", "--base-dir", "/etc/haproxy")}, c.options.MaxResponseBytes)
	var state api.State
	if err != nil || json.Unmarshal(data, &state) != nil || state.APIVersion == 0 {
		report.incomplete("agent-unavailable", pod.Name, "Check agent health, pods/exec permission, and --max-response-bytes.")
	} else {
		view = agentView(pod, &state, configPath)
		if !view.WorkerRunning || view.AppliedPlanID == "" || view.RunningPlanID == "" || view.LastApplyFailed || view.InvariantFailed {
			report.problem("agent-unhealthy", pod.Name, "Inspect haptic agent state --verify in this pod.")
		}
		if view.ReloadFallback {
			report.Findings = append(report.Findings, Finding{Code: "agent-version-skew", Severity: "warning", Resource: pod.Name, Action: "Align controller and agent versions; this agent uses reload fallback."})
		}
	}
	if !view.Ready {
		report.problem("agent-pod-not-ready", pod.Name, "Inspect the HAProxy pod's container states and readiness checks.")
	}
	report.Agents = append(report.Agents, view)
}

func controllerDebugPort(pod *corev1.Pod) int {
	for index := range pod.Spec.Containers {
		container := &pod.Spec.Containers[index]
		if container.Name != controllerRole {
			continue
		}
		for _, port := range container.Ports {
			if port.Name == "healthz" {
				return int(port.ContainerPort)
			}
		}
	}
	return 0
}

func (r *Report) incomplete(code, resource, action string) {
	r.Complete = false
	r.problem(code, resource, action)
}

func (r *Report) problem(code, resource, action string) {
	r.Healthy = false
	r.Findings = append(r.Findings, Finding{Code: code, Severity: "error", Resource: resource, Action: action})
}

func assessDeployments(report *Report) {
	for index := range report.Configurations {
		config := &report.Configurations[index]
		if config.Kind != "HAProxyCfg" {
			continue
		}
		for index := range report.Agents {
			agent := &report.Agents[index]
			if !agent.Observed {
				continue
			}
			assessAgentDeployment(report, config, agent)
		}
	}
}

func assessAgentDeployment(report *Report, config *Configuration, agent *Agent) {
	for index := range config.Deployments {
		deployment := &config.Deployments[index]
		if deployment.Pod != agent.Name || deployment.UID != agent.UID {
			continue
		}
		if deployment.AppliedPlanID != agent.AppliedPlanID || deployment.RunningPlanID != agent.RunningPlanID ||
			deployment.Checksum != config.DesiredChecksum || config.DesiredChecksum == "" || deployment.HasError || deployment.ConsecutiveErrors > 0 {
			report.problem("deployment-proof-mismatch", agent.Name, "Inspect deployment status and this agent's state, then rerun doctor.")
		}
		return
	}
	report.problem("deployment-proof-missing", agent.Name, fmt.Sprintf("Check %s deployment status for the current pod UID.", config.Name))
}
