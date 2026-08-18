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

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	agentclient "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
)

// defaultAgentPort is what the chart listens on when the HAProxyTemplateConfig
// names no spec.dataplane.port.
const defaultAgentPort = 5555

// readPodState reads one HAProxy pod's agent state. A nil ref takes whichever
// pod the HAProxyTemplateConfig's selector reports first, which is what the
// baseline side of a diff defaults to.
func readPodState(ctx context.Context, ref *podRef) (*api.State, error) {
	k8sClient, err := client.New(client.Config{Kubeconfig: diffKubeconfig, Namespace: diffNamespace})
	if err != nil {
		return nil, fmt.Errorf("no cluster access: %w\n"+
			"Hint: pass --from <config.yaml> to compare two files instead", err)
	}

	namespace := firstNonEmpty(diffNamespace, refNamespace(ref), k8sClient.Namespace())
	if namespace == "" {
		return nil, fmt.Errorf("no namespace for the HAProxyTemplateConfig; pass --namespace")
	}

	crdClient, err := versioned.NewForConfig(k8sClient.RestConfig())
	if err != nil {
		return nil, fmt.Errorf("creating the HAProxyTemplateConfig client: %w", err)
	}
	configName := resolveConfigName(diffCRDName)
	templateConfig, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyTemplateConfigs(namespace).Get(ctx, configName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting HAProxyTemplateConfig %s/%s: %w\n"+
			"Hint: pass --namespace and --crd-name, or --from <config.yaml> to compare two files instead",
			namespace, configName, err)
	}

	pod, err := findHAProxyPod(ctx, k8sClient, namespace, ref, &templateConfig.Spec.PodSelector)
	if err != nil {
		return nil, err
	}
	username, password, err := agentCredentials(ctx, k8sClient, namespace, &templateConfig.Spec.CredentialsSecretRef)
	if err != nil {
		return nil, err
	}

	port := diffPort
	if port == 0 {
		port = templateConfig.Spec.Dataplane.Port
	}
	if port == 0 {
		port = defaultAgentPort
	}
	url := "http://" + net.JoinHostPort(pod.Status.PodIP, strconv.Itoa(port))
	fmt.Fprintf(os.Stderr, "reading %s/%s at %s\n", pod.Namespace, pod.Name, url)

	agent, err := agentclient.New(&agentclient.Config{BaseURL: url, Username: username, Password: password})
	if err != nil {
		return nil, err
	}
	defer agent.Close()
	state, err := agent.State(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("reading %s from %s/%s: %w", api.PathState, pod.Namespace, pod.Name, err)
	}
	return state, nil
}

// findHAProxyPod resolves the named pod, or the first one the config's selector
// matches that has an address to talk to.
func findHAProxyPod(ctx context.Context, k8sClient *client.Client, namespace string, ref *podRef, selector *v1alpha1.PodSelector) (*corev1.Pod, error) {
	pods := k8sClient.Clientset().CoreV1().Pods(namespace)
	if ref != nil {
		pod, err := pods.Get(ctx, ref.name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("getting pod %s/%s: %w", ref.namespace, ref.name, err)
		}
		if pod.Status.PodIP == "" {
			return nil, fmt.Errorf("pod %s/%s has no address yet", pod.Namespace, pod.Name)
		}
		return pod, nil
	}

	if len(selector.MatchLabels) == 0 {
		return nil, fmt.Errorf("the HAProxyTemplateConfig selects no pods; name one with --from pod://<namespace>/<pod>")
	}
	list, err := pods.List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(selector.MatchLabels).String(),
	})
	if err != nil {
		return nil, fmt.Errorf("listing HAProxy pods in %s: %w", namespace, err)
	}
	// By name, so two runs against an unchanged fleet read the same pod.
	slices.SortFunc(list.Items, func(a, b corev1.Pod) int { return strings.Compare(a.Name, b.Name) })
	for i := range list.Items {
		if pod := &list.Items[i]; pod.Status.PodIP != "" && pod.Status.Phase == corev1.PodRunning {
			return pod, nil
		}
	}
	return nil, fmt.Errorf("no running HAProxy pod with an address in %s.\n"+
		"Hint: pass --from <config.yaml> to compare two files instead", namespace)
}

// agentCredentials reads the Secret the pods' agents authenticate against —
// the config's credentialsSecretRef, or --secret-name / SECRET_NAME.
func agentCredentials(ctx context.Context, k8sClient *client.Client, namespace string, ref *v1alpha1.SecretReference) (username, password string, err error) {
	name := firstNonEmpty(ref.Name, diffSecretName, os.Getenv("SECRET_NAME"), defaultSecretName)
	secretNamespace := firstNonEmpty(ref.Namespace, namespace)
	secret, err := k8sClient.Clientset().CoreV1().Secrets(secretNamespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", "", fmt.Errorf("getting credentials Secret %s/%s: %w", secretNamespace, name, err)
	}
	credentials, err := coreconfig.LoadCredentials(secret.Data)
	if err != nil {
		return "", "", fmt.Errorf("reading credentials Secret %s/%s: %w", secretNamespace, name, err)
	}
	return credentials.DataplaneUsername, credentials.DataplanePassword, nil
}

func refNamespace(ref *podRef) string {
	if ref == nil {
		return ""
	}
	return ref.namespace
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
