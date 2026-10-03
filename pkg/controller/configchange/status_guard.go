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

package configchange

import (
	"context"
	"slices"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The status guard checks soon after each verdict, then backs off: a stale
// writer is an old pod in its termination grace, so it stops on its own.
const (
	statusGuardMinInterval = time.Second
	statusGuardMaxInterval = 30 * time.Second
)

func assertedKey(namespace, name string) string {
	return namespace + "/" + name
}

// recordAsserted remembers the status this leader stands behind for a source
// and restarts the guard's backoff.
func (u *StatusUpdater) recordAsserted(namespace, name string, status *v1alpha1.HAProxyTemplateConfigStatus) {
	u.mu.Lock()
	u.asserted[assertedKey(namespace, name)] = *status.DeepCopy()
	u.mu.Unlock()
	select {
	case u.statusGuardWake <- struct{}{}:
	default:
	}
}

// runStatusGuard makes the stored status converge to this leader's verdict.
// Pods that are not the leader still write status: the startup load gate
// reports its rejection before leader election, and an old controller version
// keeps doing so for its whole termination grace during a rolling upgrade. The
// last write would otherwise win (#270).
func (u *StatusUpdater) runStatusGuard(ctx context.Context) {
	interval := u.statusGuardMinInterval
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-u.statusGuardWake:
			interval = u.statusGuardMinInterval
		case <-timer.C:
			u.reassertVerdicts(ctx)
			interval = min(2*interval, u.statusGuardMaxInterval)
		}
		timer.Reset(interval)
	}
}

func (u *StatusUpdater) reassertVerdicts(ctx context.Context) {
	u.mu.RLock()
	refs := slices.Clone(u.configRefs)
	u.mu.RUnlock()

	for _, ref := range refs {
		u.writeMu.Lock()
		u.mu.RLock()
		want, ok := u.asserted[assertedKey(ref.Namespace, ref.Name)]
		u.mu.RUnlock()
		if ok {
			u.reassertVerdict(ctx, ref.Namespace, ref.Name, &want)
		}
		u.writeMu.Unlock()
	}
}

// reassertVerdict rewrites a source's status when a different writer replaced
// this leader's verdict with one for the same or an older generation. A newer
// generation is left alone: this leader hasn't judged it yet.
func (u *StatusUpdater) reassertVerdict(ctx context.Context, namespace, name string, want *v1alpha1.HAProxyTemplateConfigStatus) {
	client := u.crdClient.HaproxyTemplateICV1alpha1().HAProxyTemplateConfigs(namespace)
	current, err := client.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		u.Logger().Debug("Status guard could not read HAProxyTemplateConfig",
			"namespace", namespace, "name", name, "error", err)
		return
	}
	if current.Status.ObservedGeneration > want.ObservedGeneration ||
		statusEqualIgnoringTimestamps(&current.Status, want) {
		return
	}

	stale := current.Status.DeepCopy()
	current.Status = *want.DeepCopy()
	if _, err := client.UpdateStatus(ctx, current, metav1.UpdateOptions{}); err != nil {
		u.Logger().Warn("Status guard failed to restore the leader's verdict",
			"namespace", namespace, "name", name, "error", err)
		return
	}
	u.Logger().Info("Restored the leader's verdict over a status another controller wrote",
		"namespace", namespace, "name", name,
		"generation", want.ObservedGeneration,
		"overwritten_generation", stale.ObservedGeneration,
		"overwritten_status", stale.ValidationStatus)
}
