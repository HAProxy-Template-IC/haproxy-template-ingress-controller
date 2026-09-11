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
	"fmt"
	"os/exec"
	"slices"
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
		values["worker_uid"] != "1000:1000" || values["worker_capabilities"] != "0000000000000000" {
		return fmt.Errorf("unexpected workdir evidence: %v", values)
	}
	residency, err := testutil.VerifyVarnishMemoryResidency(values)
	if err != nil {
		return err
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
	security := pod.Spec.Containers[container].SecurityContext
	if security != nil && (security.Capabilities != nil && len(security.Capabilities.Add) > 0 ||
		security.Privileged != nil && *security.Privileged) {
		return fmt.Errorf("stock Varnish must not add capabilities or run privileged")
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
printf 'worker_uid=%s\n' "$(awk '$1 == "Uid:" {print $2 ":" $3}' "/proc/$worker/status")"
printf 'worker_capabilities=%s\n' "$(awk '$1 == "CapEff:" {print $2}' "/proc/$worker/status")"
printf 'locked_kib=%s\n' "$(awk '$1 == "VmLck:" && $3 == "kB" {print $2}' "/proc/$worker/status")"
awk '
/^[0-9a-f]+-[0-9a-f]+ / {shared = ($0 ~ /\/_.vsm_(child|mgt)\//)}
shared && $1 == "Size:" {mapped += $2}
shared && $1 == "Rss:" {resident += $2}
shared && $1 == "Locked:" {locked += $2}
shared && $1 == "Swap:" {swapped += $2}
END {
  printf "shared_size_kib=%.0f\nshared_rss_kib=%.0f\nshared_locked_kib=%.0f\nshared_swap_kib=%.0f\n", mapped, resident, locked, swapped
}' "/proc/$worker/smaps"
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
