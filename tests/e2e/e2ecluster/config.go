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

// Package e2ecluster resolves the isolated kind environment used by the e2e suite.
package e2ecluster

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	clusterNameEnv     = "HAPTIC_E2E_CLUSTER_NAME"
	kubeconfigPathEnv  = "HAPTIC_E2E_KUBECONFIG_PATH"
	exposeHostPortsEnv = "HAPTIC_E2E_EXPOSE_HOST_PORTS"
	dockerNetworkEnv   = "KIND_EXPERIMENTAL_DOCKER_NETWORK"

	defaultClusterName    = "haptic-e2e"
	defaultKubeconfigPath = "/tmp/haproxy-e2e-kubeconfig"
	defaultDockerNetwork  = "kind"
	isolationNamePrefix   = "haptic-gwbench-"
)

// Config identifies the kind resources owned by one e2e suite invocation.
type Config struct {
	ClusterName     string
	KubeconfigPath  string
	DockerNetwork   string
	ExposeHostPorts bool
	RequireNew      bool
}

// Default returns the established e2e cluster settings.
func Default() Config {
	return Config{
		ClusterName:     defaultClusterName,
		KubeconfigPath:  defaultKubeconfigPath,
		DockerNetwork:   defaultDockerNetwork,
		ExposeHostPorts: true,
	}
}

// Load accepts either the established defaults or one complete isolation tuple.
func Load() (Config, error) {
	type setting struct {
		name  string
		value string
		set   bool
	}
	clusterName, clusterNameSet := os.LookupEnv(clusterNameEnv)
	kubeconfigPath, kubeconfigPathSet := os.LookupEnv(kubeconfigPathEnv)
	exposeHostPorts, exposeHostPortsSet := os.LookupEnv(exposeHostPortsEnv)
	dockerNetwork, dockerNetworkSet := os.LookupEnv(dockerNetworkEnv)
	settings := []setting{
		{name: clusterNameEnv, value: clusterName, set: clusterNameSet},
		{name: kubeconfigPathEnv, value: kubeconfigPath, set: kubeconfigPathSet},
		{name: exposeHostPortsEnv, value: exposeHostPorts, set: exposeHostPortsSet},
		{name: dockerNetworkEnv, value: dockerNetwork, set: dockerNetworkSet},
	}
	setCount := 0
	for _, setting := range settings {
		if setting.set {
			setCount++
		}
	}
	if setCount == 0 {
		return Default(), nil
	}
	if setCount != len(settings) {
		return Config{}, fmt.Errorf("e2e isolation overrides must set %s, %s, %s=false, and %s together",
			clusterNameEnv, kubeconfigPathEnv, exposeHostPortsEnv, dockerNetworkEnv)
	}
	for _, setting := range settings {
		if setting.value == "" {
			return Config{}, fmt.Errorf("%s must not be empty in an e2e isolation tuple", setting.name)
		}
	}

	if exposeHostPorts != "false" {
		return Config{}, fmt.Errorf("%s must be false in an e2e isolation tuple", exposeHostPortsEnv)
	}
	config := Config{
		ClusterName:     clusterName,
		KubeconfigPath:  kubeconfigPath,
		DockerNetwork:   dockerNetwork,
		ExposeHostPorts: false,
		RequireNew:      true,
	}
	if !validIsolationName(config.ClusterName) {
		return Config{}, fmt.Errorf("%s must match %s<lowercase DNS token>, got %q",
			clusterNameEnv, isolationNamePrefix, config.ClusterName)
	}
	if config.DockerNetwork != config.ClusterName {
		return Config{}, fmt.Errorf("%s must equal %s in an e2e isolation tuple",
			dockerNetworkEnv, clusterNameEnv)
	}
	expectedKubeconfigBase := config.ClusterName + ".kubeconfig"
	if !filepath.IsAbs(config.KubeconfigPath) || filepath.Clean(config.KubeconfigPath) != config.KubeconfigPath ||
		filepath.Base(config.KubeconfigPath) != expectedKubeconfigBase {
		return Config{}, fmt.Errorf("%s must be an absolute clean path ending in %q",
			kubeconfigPathEnv, expectedKubeconfigBase)
	}

	return config, nil
}

func validIsolationName(name string) bool {
	if !strings.HasPrefix(name, isolationNamePrefix) || len(name) > 63 {
		return false
	}
	suffix := strings.TrimPrefix(name, isolationNamePrefix)
	if suffix == "" || !isLowerAlphaNumeric(suffix[0]) || !isLowerAlphaNumeric(suffix[len(suffix)-1]) {
		return false
	}
	for i := range suffix {
		if isLowerAlphaNumeric(suffix[i]) || suffix[i] == '-' {
			continue
		}
		return false
	}
	return true
}

func isLowerAlphaNumeric(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
}

// KindConfig omits host mappings when another local kind cluster may own them.
func (c Config) KindConfig() string {
	if c.ExposeHostPorts {
		return defaultKindConfig
	}
	return strings.Replace(defaultKindConfig, hostPortMappings, "", 1)
}

// WriteKubeconfig never replaces a path owned by another isolated run.
func (c Config) WriteKubeconfig(contents []byte) error {
	if !c.RequireNew {
		return os.WriteFile(c.KubeconfigPath, contents, 0o600)
	}
	file, err := os.OpenFile(c.KubeconfigPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		_ = os.Remove(c.KubeconfigPath)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(c.KubeconfigPath)
		return err
	}
	return nil
}

const hostPortMappings = `    extraPortMappings:
      - containerPort: 30080
        hostPort: 31080
        protocol: TCP
        listenAddress: "0.0.0.0"
      - containerPort: 30443
        hostPort: 31443
        protocol: TCP
        listenAddress: "0.0.0.0"
      - containerPort: 30404
        hostPort: 31404
        protocol: TCP
        listenAddress: "0.0.0.0"
`

const defaultKindConfig = `kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
networking:
  apiServerAddress: "0.0.0.0"
nodes:
  - role: control-plane
    kubeadmConfigPatches:
      - |
        kind: InitConfiguration
        nodeRegistration:
          kubeletExtraArgs:
            node-labels: "ingress-ready=true"
    extraPortMappings:
      - containerPort: 30080
        hostPort: 31080
        protocol: TCP
        listenAddress: "0.0.0.0"
      - containerPort: 30443
        hostPort: 31443
        protocol: TCP
        listenAddress: "0.0.0.0"
      - containerPort: 30404
        hostPort: 31404
        protocol: TCP
        listenAddress: "0.0.0.0"
kubeadmConfigPatches:
  - |
    kind: ClusterConfiguration
    apiServer:
      extraArgs:
        enable-admission-plugins: NodeRestriction,MutatingAdmissionWebhook,ValidatingAdmissionWebhook
  # Raise the kubelet's per-container log rotation cap (default 10Mi).
  # The controller logs at DEBUG during e2e/conformance runs and the
  # leader replica exceeds 10Mi well within one suite, after which
  # "kubectl logs" (used by the CI after_script diagnostics capture)
  # returns only the newest rotated file — job 15180387459's artifacts
  # carried just ~7s of leader logs, none covering the failure window
  # (issue #56). 200Mi buys roughly a minute at the observed ~3 MB/s.
  #
  # It is NOT sufficient on its own: kubectl logs serves only the CURRENT
  # rotated file, so a run longer than that minute still loses its earlier
  # history to this capture path. The CI after_script therefore also reads the
  # rotated files directly off the node (/var/log/pods) into
  # debug-logs/_suite/controller-full.log.gz — that, not this cap, is what
  # makes a whole run retrievable.
  - |
    kind: KubeletConfiguration
    containerLogMaxSize: 200Mi
kubeadmConfigPatchesJSON6902:
  # Both kubeadm config versions: kind applies the one matching the node's
  # k8s version (v1beta3 for <= 1.35, v1beta4 for >= 1.36) and skips the other.
  # The e2e suite uses kind's default 1.36 node (v1beta4); keep both so the
  # SAN lands regardless of node version. Do not collapse to a single version.
  - group: kubeadm.k8s.io
    version: v1beta3
    kind: ClusterConfiguration
    patch: |
      - op: add
        path: /apiServer/certSANs/-
        value: docker
  - group: kubeadm.k8s.io
    version: v1beta4
    kind: ClusterConfiguration
    patch: |
      - op: add
        path: /apiServer/certSANs/-
        value: docker
`
