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

//go:build e2e

package e2ecluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	unsetIsolationEnv(t)

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := Default()
	if got != want {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
}

func TestLoadIsolationOverrides(t *testing.T) {
	setIsolationEnv(t, validIsolationEnv())

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := Config{
		ClusterName:     "haptic-gwbench-abc123",
		KubeconfigPath:  "/tmp/haptic-gwbench-abc123.kubeconfig",
		DockerNetwork:   "haptic-gwbench-abc123",
		ExposeHostPorts: false,
		RequireNew:      true,
	}
	if got != want {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
}

func TestLoadRejectsInvalidExposeHostPorts(t *testing.T) {
	values := validIsolationEnv()
	values[exposeHostPortsEnv] = "0"
	setIsolationEnv(t, values)

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid boolean error")
	}
}

func TestLoadRejectsPartialIsolationOverrides(t *testing.T) {
	for _, missing := range []string{clusterNameEnv, kubeconfigPathEnv, exposeHostPortsEnv, dockerNetworkEnv} {
		t.Run(missing, func(t *testing.T) {
			values := validIsolationEnv()
			delete(values, missing)
			setIsolationEnv(t, values)

			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want incomplete isolation tuple error")
			}
		})
	}
}

func TestLoadRejectsEmptyIsolationOverrides(t *testing.T) {
	for _, empty := range []string{clusterNameEnv, kubeconfigPathEnv, exposeHostPortsEnv, dockerNetworkEnv} {
		t.Run(empty, func(t *testing.T) {
			values := validIsolationEnv()
			values[empty] = ""
			setIsolationEnv(t, values)

			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want empty isolation setting error")
			}
		})
	}
}

func TestLoadRejectsProtectedClusterNames(t *testing.T) {
	for _, name := range []string{"haptic-dev", "haptic-e2e", "haproxy-test", "helm-defaults"} {
		t.Run(name, func(t *testing.T) {
			values := validIsolationEnv()
			values[clusterNameEnv] = name
			values[dockerNetworkEnv] = name
			values[kubeconfigPathEnv] = "/tmp/" + name + ".kubeconfig"
			setIsolationEnv(t, values)

			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want protected cluster name error")
			}
		})
	}
}

func TestLoadRejectsUnsafeIsolationTuple(t *testing.T) {
	tests := map[string]func(map[string]string){
		"shared docker network": func(values map[string]string) {
			values[dockerNetworkEnv] = defaultDockerNetwork
		},
		"default kubeconfig": func(values map[string]string) {
			values[kubeconfigPathEnv] = defaultKubeconfigPath
		},
		"relative kubeconfig": func(values map[string]string) {
			values[kubeconfigPathEnv] = "haptic-gwbench-abc123.kubeconfig"
		},
		"unrelated kubeconfig": func(values map[string]string) {
			values[kubeconfigPathEnv] = "/tmp/haptic-gwbench-another.kubeconfig"
		},
		"host ports enabled": func(values map[string]string) {
			values[exposeHostPortsEnv] = "true"
		},
		"invalid cluster characters": func(values map[string]string) {
			values[clusterNameEnv] = "haptic-gwbench-ABC"
			values[dockerNetworkEnv] = "haptic-gwbench-ABC"
			values[kubeconfigPathEnv] = "/tmp/haptic-gwbench-ABC.kubeconfig"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			values := validIsolationEnv()
			mutate(values)
			setIsolationEnv(t, values)

			if _, err := Load(); err == nil {
				t.Fatal("Load() error = nil, want unsafe isolation tuple error")
			}
		})
	}
}

func TestKindConfigHostPortPolicy(t *testing.T) {
	if count := strings.Count(defaultKindConfig, hostPortMappings); count != 1 {
		t.Fatalf("default kind config contains host-port block %d times, want 1", count)
	}

	withPorts := Config{ExposeHostPorts: true}.KindConfig()
	if !strings.Contains(withPorts, hostPortMappings) {
		t.Fatal("default kind config does not contain the e2e host-port mappings")
	}

	withoutPorts := Config{ExposeHostPorts: false}.KindConfig()
	if strings.Contains(withoutPorts, "extraPortMappings:") || strings.Contains(withoutPorts, "hostPort:") {
		t.Fatal("portless kind config still contains host-port mappings")
	}
	for _, retained := range []string{
		`apiServerAddress: "0.0.0.0"`,
		`node-labels: "ingress-ready=true"`,
		"enable-admission-plugins: NodeRestriction,MutatingAdmissionWebhook,ValidatingAdmissionWebhook",
		"containerLogMaxSize: 200Mi",
		"value: docker",
	} {
		if !strings.Contains(withoutPorts, retained) {
			t.Fatalf("portless kind config lost %q", retained)
		}
	}
}

func TestWriteKubeconfigIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "haptic-gwbench-test.kubeconfig")
	config := Config{KubeconfigPath: path, RequireNew: true}
	if err := config.WriteKubeconfig([]byte("first")); err != nil {
		t.Fatalf("WriteKubeconfig() error = %v", err)
	}
	if err := config.WriteKubeconfig([]byte("second")); err == nil {
		t.Fatal("WriteKubeconfig() error = nil, want existing-path error")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read kubeconfig: %v", err)
	}
	if string(got) != "first" {
		t.Fatalf("kubeconfig = %q, want first contents", got)
	}
}

func TestWriteKubeconfigDefaultsReplaceExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "haproxy-e2e-kubeconfig")
	if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	config := Config{KubeconfigPath: path}
	if err := config.WriteKubeconfig([]byte("second")); err != nil {
		t.Fatalf("WriteKubeconfig() error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read kubeconfig: %v", err)
	}
	if string(got) != "second" {
		t.Fatalf("kubeconfig = %q, want replacement contents", got)
	}
}

func validIsolationEnv() map[string]string {
	return map[string]string{
		clusterNameEnv:     "haptic-gwbench-abc123",
		kubeconfigPathEnv:  "/tmp/haptic-gwbench-abc123.kubeconfig",
		dockerNetworkEnv:   "haptic-gwbench-abc123",
		exposeHostPortsEnv: "false",
	}
}

func setIsolationEnv(t *testing.T, values map[string]string) {
	t.Helper()
	unsetIsolationEnv(t)
	for name, value := range values {
		t.Setenv(name, value)
	}
}

func unsetIsolationEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{clusterNameEnv, kubeconfigPathEnv, exposeHostPortsEnv, dockerNetworkEnv} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
	}
}
