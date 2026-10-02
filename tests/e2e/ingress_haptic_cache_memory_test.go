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

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

func TestHapticVarnishRuntimeMemory(t *testing.T) {
	RequireCacheProfile(t)
	feature := features.New("Varnish: workdir and shared-memory residency").
		Assess("the deployed process uses writable executable tmpfs that cannot page out", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			configs := listHaptic(ctx, t, cfg, configGVR)
			if len(configs) != 1 {
				t.Fatalf("expected one controller config, got %d", len(configs))
			}
			image, found, err := unstructured.NestedString(configs[0].Object,
				"spec", "templatingSettings", "extraContext", "cache", "varnish", "image")
			if err != nil || !found {
				t.Fatalf("read configured Varnish image: found=%t, error=%v", found, err)
			}
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatal(err)
			}
			pods, err := varnishPods(ctx, client)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range pods {
				var pod corev1.Pod
				if err := client.Resources(ControllerNamespace).Get(ctx, name, ControllerNamespace, &pod); err != nil {
					t.Fatal(err)
				}
				if err := verifyVarnishRuntimeMemory(ctx, t, &pod, image); err != nil {
					t.Fatalf("Varnish pod %s: %v", name, err)
				}
			}
			return ctx
		})
	testEnv.Test(t, feature.Feature())
}

func verifyVarnishRuntimeMemory(ctx context.Context, tb testing.TB, pod *corev1.Pod, expectedImage string) error {
	tb.Helper()
	if err := verifyVarnishMemoryPod(pod, expectedImage); err != nil {
		return err
	}
	values, err := readVarnishMemoryProbe(ctx, pod.Name)
	if err != nil {
		return err
	}
	if values["filesystem"] != "tmpfs" || values["owner"] != "1000:1000" || values["reload"] != "ok" ||
		values["worker_uid"] != "1000:1000" || values["worker_capabilities"] != "0000000000004000" || values["worker_no_new_privileges"] != "1" {
		return fmt.Errorf("unexpected workdir evidence: %v", values)
	}
	if err := addVarnishSharedMappings(ctx, pod, values); err != nil {
		return err
	}
	residency, err := testutil.VerifyVarnishMemoryResidency(values)
	if err != nil {
		return err
	}
	if residency != "mlock" {
		return fmt.Errorf("Varnish shared memory is not locked: %s", residency)
	}
	tb.Logf("Varnish %s residency=%s evidence=%v", pod.Name, residency, values)
	return nil
}

func verifyVarnishMemoryPod(pod *corev1.Pod, expectedImage string) error {
	if pod.Spec.SecurityContext == nil || pod.Spec.SecurityContext.FSGroup == nil || *pod.Spec.SecurityContext.FSGroup != 1000 {
		return fmt.Errorf("workdir requires fsGroup 1000")
	}
	volume := slices.IndexFunc(pod.Spec.Volumes, func(v corev1.Volume) bool { return v.Name == "varnish-workdir" })
	if volume < 0 || pod.Spec.Volumes[volume].EmptyDir == nil || pod.Spec.Volumes[volume].EmptyDir.Medium != corev1.StorageMediumMemory {
		return fmt.Errorf("workdir is not a memory-backed emptyDir")
	}
	container := slices.IndexFunc(pod.Spec.Containers, func(c corev1.Container) bool { return c.Name == "varnish" })
	if container < 0 {
		return fmt.Errorf("Varnish container is missing")
	}
	if pod.Spec.Containers[container].Image != expectedImage {
		return fmt.Errorf("pod image %q does not match configured image %q", pod.Spec.Containers[container].Image, expectedImage)
	}
	return verifyVarnishSecurity(pod.Spec.Containers[container].SecurityContext)
}

func verifyVarnishSecurity(security *corev1.SecurityContext) error {
	if security == nil || security.RunAsNonRoot == nil || !*security.RunAsNonRoot ||
		security.RunAsUser == nil || *security.RunAsUser != 1000 ||
		security.RunAsGroup == nil || *security.RunAsGroup != 1000 ||
		security.AllowPrivilegeEscalation == nil || *security.AllowPrivilegeEscalation ||
		security.Privileged != nil && *security.Privileged {
		return fmt.Errorf("Varnish must run as user 1000 without privilege escalation")
	}
	if security.Capabilities == nil ||
		!slices.Equal(security.Capabilities.Add, []corev1.Capability{"IPC_LOCK"}) ||
		!slices.Equal(security.Capabilities.Drop, []corev1.Capability{"ALL"}) {
		return fmt.Errorf("Varnish must drop every capability except IPC_LOCK")
	}
	return nil
}

func readVarnishMemoryProbe(ctx context.Context, podName string) (map[string]string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(probeCtx, "kubectl", "--kubeconfig", kubeconfigPath,
		"-n", ControllerNamespace, "exec", podName, "-c", "varnish", "--", "/bin/sh", "-c", varnishMemoryProbe).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("probe workdir and reload VCL: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return parseVarnishProbe(output)
}

func parseVarnishProbe(output []byte) (map[string]string, error) {
	values := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		key, value, found := strings.Cut(line, "=")
		_, duplicate := values[key]
		if !found || key == "" || duplicate {
			return nil, fmt.Errorf("invalid memory probe output %q", line)
		}
		values[key] = value
	}
	return values, nil
}

func addVarnishSharedMappings(ctx context.Context, pod *corev1.Pod, values map[string]string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	node := pod.Spec.NodeName
	owner, err := exec.CommandContext(ctx, "docker", "inspect", "--format",
		`{{index .Config.Labels "io.x-k8s.kind.cluster"}}`, node).Output()
	if err != nil || strings.TrimSpace(string(owner)) != ClusterName {
		return fmt.Errorf("node %s does not belong to test cluster %s: %v", node, ClusterName, err)
	}
	rootPID, err := varnishContainerPID(ctx, pod)
	if err != nil {
		return err
	}
	workerPID, err := strconv.Atoi(values["worker_pid"])
	if err != nil || workerPID <= 0 {
		return fmt.Errorf("invalid Varnish worker PID %q", values["worker_pid"])
	}
	// File capabilities restrict smaps access; inspect from the owned Kind node.
	workerPath := fmt.Sprintf("/proc/%d/root/proc/%d/smaps", rootPID, workerPID)
	managerPath := fmt.Sprintf("/proc/%d/smaps", rootPID)
	output, err := exec.CommandContext(ctx, "docker", "exec", node, "awk", varnishSharedMappingProbe, managerPath, workerPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("read Varnish mappings: %w: %s", err, output)
	}
	mappings, err := parseVarnishProbe(output)
	if err != nil {
		return err
	}
	for key, value := range mappings {
		values[key] = value
	}
	return nil
}

func varnishContainerPID(ctx context.Context, pod *corev1.Pod) (int, error) {
	index := slices.IndexFunc(pod.Status.ContainerStatuses, func(status corev1.ContainerStatus) bool {
		return status.Name == "varnish"
	})
	if index < 0 {
		return 0, fmt.Errorf("Varnish container status is missing")
	}
	id := strings.TrimPrefix(pod.Status.ContainerStatuses[index].ContainerID, "containerd://")
	output, err := exec.CommandContext(ctx, "docker", "exec", pod.Spec.NodeName, "crictl", "inspect", id).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("inspect Varnish process: %w: %s", err, output)
	}
	var container struct {
		Info struct {
			PID int `json:"pid"`
		} `json:"info"`
	}
	if err := json.Unmarshal(output, &container); err != nil {
		return 0, fmt.Errorf("decode Varnish process identity: %w", err)
	}
	if container.Info.PID <= 0 {
		return 0, fmt.Errorf("Varnish container has no running process")
	}
	return container.Info.PID, nil
}

// Count each backing-file extent once; mlock is not inherited across fork.
// VmFlags reports locking; Locked is PSS: https://docs.kernel.org/filesystems/proc.html.
const varnishSharedMappingProbe = `
/^[0-9a-f]+-[0-9a-f]+ / {
  shared = ($0 ~ /\/_.vsm_(child|mgt)\//)
  extent = $4 ":" $5 ":" $3
}
shared && $1 == "Size:" {key = extent ":" $2; sizes[key] = $2}
shared && $1 == "Rss:" && $2 > rss[key] {rss[key] = $2}
shared && $1 == "VmFlags:" {for (i = 2; i <= NF; i++) if ($i == "lo") locks[key] = sizes[key]}
shared && $1 == "Swap:" && $2 > swaps[key] {swaps[key] = $2}
END {
  for (key in sizes) {
    mapped += sizes[key]; resident += rss[key]; locked += locks[key]; swapped += swaps[key]
  }
  printf "shared_size_kib=%.0f\nshared_rss_kib=%.0f\nshared_locked_kib=%.0f\nshared_swap_kib=%.0f\n", mapped, resident, locked, swapped
}`

func TestHapticVarnishSharedMappingProbe(t *testing.T) {
	mapping := func(inode, offset, rss, locked int, flags string) string {
		return fmt.Sprintf("1000-2000 rw-s %08x 00:229 %d /var/lib/varnish/_.vsm_mgt/_.Params\nSize: 4 kB\nRss: %d kB\nLocked: %d kB\nSwap: 0 kB\nVmFlags: rd wr sh %s\n",
			offset, inode, rss, locked, flags)
	}
	tests := []struct {
		name, input, size, resident, locked string
	}{
		{"inherited mapping locked by manager", mapping(21, 0, 4, 2, "lo") + mapping(21, 0, 4, 0, ""), "4", "4", "4"},
		{"unlocked in both processes", mapping(21, 0, 4, 0, "") + mapping(21, 0, 0, 0, ""), "4", "4", "0"},
		{"different files are not combined", mapping(21, 0, 4, 4, "lo") + mapping(22, 0, 4, 0, ""), "8", "8", "4"},
		{"different extents are not combined", mapping(21, 0, 4, 4, "lo") + mapping(21, 4096, 4, 0, ""), "8", "8", "4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "awk", varnishSharedMappingProbe)
			cmd.Stdin = strings.NewReader(tt.input)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run mapping probe: %v: %s", err, output)
			}
			values, err := parseVarnishProbe(output)
			if err != nil {
				t.Fatal(err)
			}
			if values["shared_size_kib"] != tt.size || values["shared_rss_kib"] != tt.resident || values["shared_locked_kib"] != tt.locked {
				t.Fatalf("unexpected mappings: %v", values)
			}
		})
	}
}

const varnishMemoryProbe = `set -eu
workdir=/var/lib/varnish/varnishd
test -w "$workdir"
test -x "$workdir"
printf 'filesystem=%s\n' "$(stat -f -c %T "$workdir")"
printf 'owner=%s\n' "$(stat -c '%u:%g' "$workdir")"
awk '$2 == "/var/lib/varnish" {found=1; if ($4 ~ /(^|,)noexec(,|$)/) exit 1} END {if (!found) exit 1}' /proc/mounts
printf 'mount_noswap=%s\n' "$(awk '$2 == "/var/lib/varnish" {print ($4 ~ /(^|,)noswap(,|$)/) ? "true" : "false"}' /proc/mounts)"
printf 'node_swap_devices=%s\n' "$(awk 'END {print NR-1}' /proc/swaps)"
worker=$(varnishadm -n "$workdir" pid | awk '$1 == "Worker:" {print $2}')
test -n "$worker"
printf 'worker_pid=%s\n' "$worker"
printf 'worker_uid=%s\n' "$(awk '$1 == "Uid:" {print $2 ":" $3}' "/proc/$worker/status")"
printf 'worker_no_new_privileges=%s\n' "$(awk '$1 == "NoNewPrivs:" {print $2}' "/proc/$worker/status")"
printf 'worker_capabilities=%s\n' "$(awk '$1 == "CapEff:" {print $2}' "/proc/$worker/status")"
printf 'locked_kib=%s\n' "$(awk '$1 == "VmLck:" && $3 == "kB" {print $2}' "/proc/$worker/status")"
cgroup_path=$(awk -F: '$1 == "0" && $2 == "" {print $3}' "/proc/$worker/cgroup")
cgroup_dir=/sys/fs/cgroup$cgroup_path
if test -n "$cgroup_path" && test -r "$cgroup_dir/memory.swap.max" && test -r "$cgroup_dir/memory.swap.current"; then
  printf 'cgroup_swap_max=%s\n' "$(cat "$cgroup_dir/memory.swap.max")"
  printf 'cgroup_swap_current=%s\n' "$(cat "$cgroup_dir/memory.swap.current")"
else
  printf 'cgroup_swap_max=unknown\ncgroup_swap_current=unknown\n'
fi
printf 'vsl_space=%s\n' "$(varnishadm -n "$workdir" param.show vsl_space | awk '$1 == "Value" && $2 == "is:" {print $3}')"
active=$(varnishadm -n "$workdir" vcl.list | awk '$1 == "active" {print $NF}')
test -n "$active"
candidate=haptic_memory_probe_$$
varnishadm -n "$workdir" vcl.load "$candidate" /etc/varnish/default.vcl >/dev/null
trap 'varnishadm -n "$workdir" vcl.use "$active" >/dev/null; varnishadm -n "$workdir" vcl.discard "$candidate" >/dev/null' EXIT
varnishadm -n "$workdir" vcl.use "$candidate" >/dev/null
varnishadm -n "$workdir" vcl.use "$active" >/dev/null
varnishadm -n "$workdir" vcl.discard "$candidate" >/dev/null
trap - EXIT
printf 'reload=ok\n'
`
