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

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"gitlab.com/haproxy-haptic/haptic/pkg/introspection"
)

const (
	siblingRefreshInterval = 5 * time.Second
	siblingRequestTimeout  = 2 * time.Second
	siblingHealthPortName  = "healthz"
	maxSiblingHealthBody   = 1 << 20
	// controllerPodSelectorEnv carries the controller Deployment's
	// spec.selector, which stays the same across chart upgrades.
	controllerPodSelectorEnv = "CONTROLLER_POD_SELECTOR"
)

// siblingConvergence answers whether another controller replica runs a
// converged iteration, from a result refreshed in the background so a probe
// never waits on the API server or the network. A result that is missing,
// failed or outdated reads as "a sibling may be converged".
type siblingConvergence struct {
	ctx       context.Context
	clientset kubernetes.Interface
	namespace string
	podName   string
	selector  labels.Selector
	client    *http.Client
	now       func() time.Time
	logger    *slog.Logger

	mu            sync.Mutex
	refreshing    bool
	checkedAt     time.Time
	noneConverged bool
}

func newSiblingConvergence(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace, podName, selector string,
	logger *slog.Logger,
) (*siblingConvergence, error) {
	parsed, err := labels.Parse(selector)
	if err != nil {
		return nil, fmt.Errorf("parsing %s %q: %w", controllerPodSelectorEnv, selector, err)
	}
	if parsed.Empty() {
		return nil, fmt.Errorf("%s is empty and would match every pod in the namespace", controllerPodSelectorEnv)
	}
	return &siblingConvergence{
		ctx:       ctx,
		clientset: clientset,
		namespace: namespace,
		podName:   podName,
		selector:  parsed,
		client:    &http.Client{Timeout: siblingRequestTimeout},
		now:       time.Now,
		logger:    logger,
	}, nil
}

// NoneConverged reports whether the last check found no converged sibling,
// and starts a new check when that result is due.
func (s *siblingConvergence) NoneConverged() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	age := s.now().Sub(s.checkedAt)
	if !s.refreshing && age >= siblingRefreshInterval {
		s.refreshing = true
		go s.refresh()
	}
	return s.noneConverged && age < 2*siblingRefreshInterval
}

func (s *siblingConvergence) refresh() {
	none, err := s.check(s.ctx)
	if err != nil {
		s.logger.Debug("Could not rule out a converged controller replica", "error", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshing = false
	s.checkedAt = s.now()
	s.noneConverged = err == nil && none
}

func (s *siblingConvergence) check(ctx context.Context) (bool, error) {
	pods, err := s.clientset.CoreV1().Pods(s.namespace).List(ctx, metav1.ListOptions{LabelSelector: s.selector.String()})
	if err != nil {
		return false, fmt.Errorf("listing controller pods: %w", err)
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Name == s.podName || pod.Status.PodIP == "" || !podReady(pod) {
			continue
		}
		converged, err := s.converged(ctx, pod)
		if err != nil {
			return false, fmt.Errorf("pod %s: %w", pod.Name, err)
		}
		if converged {
			return false, nil
		}
	}
	return true, nil
}

// converged reports whether pod's /healthz is 200 without a grace entry.
func (s *siblingConvergence) converged(ctx context.Context, pod *corev1.Pod) (bool, error) {
	port, ok := namedContainerPort(pod, siblingHealthPortName)
	if !ok {
		return false, fmt.Errorf("no %q container port", siblingHealthPortName)
	}
	url := "http://" + net.JoinHostPort(pod.Status.PodIP, strconv.Itoa(int(port))) + "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return false, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, nil
	}
	var body struct {
		Components map[string]introspection.ComponentHealth `json:"components"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxSiblingHealthBody)).Decode(&body); err != nil {
		return false, fmt.Errorf("decoding /healthz: %w", err)
	}
	for _, component := range body.Components {
		if strings.HasPrefix(component.Error, reinitGracePrefix) {
			return false, nil
		}
	}
	return true, nil
}

func podReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func namedContainerPort(pod *corev1.Pod, name string) (int32, bool) {
	for i := range pod.Spec.Containers {
		for _, port := range pod.Spec.Containers[i].Ports {
			if port.Name == name {
				return port.ContainerPort, true
			}
		}
	}
	return 0, false
}
