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
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
)

// specGenerationHistory is how many recently-published spec checksums keep their
// generation. A pod's status update follows its publish by one deploy (seconds),
// while churn republishes every few seconds — so only the newest handful are ever
// looked up. The bound exists so a long-lived leader under constant churn cannot
// grow the map without limit; overflowing it costs an unknown generation, which
// reads as not-yet-converged and self-corrects on the next publish.
const specGenerationHistory = 64

// specGenerations remembers which spec generation each published checksum became,
// so a pod reporting a checksum can be ordered against the spec.
//
// The correlation cannot be made at status-write time: by then the spec has often
// moved on, and the API gives no way to ask which generation carried a given
// checksum. The publisher is the only place that sees both at once — it holds the
// object the API server just stamped.
type specGenerations struct {
	mu    sync.Mutex
	byKey map[string]int64
	// order records insertion sequence so the oldest entry can be evicted once
	// byKey exceeds specGenerationHistory.
	order []string
}

func newSpecGenerations() *specGenerations {
	return &specGenerations{byKey: make(map[string]int64, specGenerationHistory)}
}

// record associates a published checksum with the generation the API server
// assigned. Re-recording a known checksum keeps the FIRST generation: a spec
// republished unchanged does not bump metadata.generation, and the earliest one
// is the point from which the content was live.
func (s *specGenerations) record(namespace, name, checksum string, generation int64) {
	if checksum == "" || generation <= 0 {
		return
	}
	k := generationKey(namespace, name, checksum)

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, seen := s.byKey[k]; seen {
		return
	}
	s.byKey[k] = generation
	s.order = append(s.order, k)
	if len(s.order) > specGenerationHistory {
		delete(s.byKey, s.order[0])
		s.order = s.order[1:]
	}
}

// lookup returns the generation a checksum was published as, or 0 when this
// publisher has no record of it (a previous leader published it, or the entry
// aged out). Zero is the API's documented "unknown" and reads as not-converged.
func (s *specGenerations) lookup(namespace, name, checksum string) int64 {
	if checksum == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byKey[generationKey(namespace, name, checksum)]
}

// generationKey scopes a checksum to its owning object. Identical content
// published under two HAProxyCfgs carries the same checksum but unrelated
// generations, so the object identity has to be part of the key.
func generationKey(namespace, name, checksum string) string {
	return namespace + "/" + name + "/" + checksum
}

// recordSpecGeneration remembers the generation the API server assigned to the
// spec cfg now carries. Nil-safe on specGens so a zero-value Publisher (tests
// that construct one directly) still works.
func (p *Publisher) recordSpecGeneration(cfg *haproxyv1alpha1.HAProxyCfg) {
	if p.specGens == nil || cfg == nil {
		return
	}
	p.specGens.record(cfg.Namespace, cfg.Name, cfg.Spec.Checksum, cfg.Generation)
}

// specGenerationFor resolves the generation a checksum was published as,
// returning 0 (unknown) when this publisher never saw it.
func (p *Publisher) specGenerationFor(namespace, name, checksum string) int64 {
	if p.specGens == nil {
		return 0
	}
	return p.specGens.lookup(namespace, name, checksum)
}

// backfillObservedGeneration re-stamps pod status entries that were written
// before this checksum's generation was known.
//
// The per-pod status write and the deploy-driven publish are independent paths
// with no ordering between them. When the status lands first, the checksum is
// not yet in specGens and the entry records generation 0 (unknown). Under churn
// the next deploy would correct it — but at quiescence there IS no next deploy,
// so the final entry would keep saying "unknown" forever and every consumer
// ordering pods against the spec would wait out its budget on a converged
// cluster. Publishing is exactly the moment the missing fact becomes available,
// so close the gap here.
//
// The status is re-read rather than taken from cfg: cfg is what the spec write
// returned, and its status was loaded BEFORE that write, so a pod entry that
// landed in between is missing from it. Repairing that stale list silently
// skipped the very entries this exists to fix, leaving pods reporting a
// generation they had long since passed (observed in CI: a pod pinned at
// generation 53 while the spec was at 60, failing four tests at once).
//
// Best-effort: a failure leaves the entry unknown, which reads as
// not-yet-converged (never as falsely converged) and the next publish retries.
func (p *Publisher) backfillObservedGeneration(ctx context.Context, cfg *haproxyv1alpha1.HAProxyCfg) {
	if cfg == nil || cfg.Generation <= 0 || cfg.Spec.Checksum == "" {
		return
	}
	pods := p.currentDeployedPods(ctx, cfg)
	for i := range pods {
		entry := pods[i]
		if entry.Checksum != cfg.Spec.Checksum || entry.ObservedGeneration > 0 {
			continue
		}
		entry.ObservedGeneration = cfg.Generation
		ssaBytes, err := buildPodStatusSSAPayload("HAProxyCfg", cfg.Name, cfg.Namespace, &entry)
		if err != nil {
			continue
		}
		if _, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyCfgs(cfg.Namespace).
			Patch(ctx, cfg.Name, types.ApplyPatchType, ssaBytes,
				metav1.PatchOptions{FieldManager: podStatusFieldManager(entry.PodName), Force: new(true)},
				"status",
			); err != nil {
			p.logger.Debug("Backfilling observedGeneration failed; next publish retries",
				"pod", entry.PodName, "checksum", entry.Checksum, "error", err)
		}
	}
}

// currentDeployedPods reads the freshest per-pod status available (lister cache
// first, API fallback), falling back to the caller's copy when neither is
// readable. The caller's copy is a pre-write snapshot, so it is the last resort,
// not the default.
func (p *Publisher) currentDeployedPods(ctx context.Context, cfg *haproxyv1alpha1.HAProxyCfg) []haproxyv1alpha1.PodDeploymentStatus {
	if p.listers != nil && p.listers.HAProxyCfgs != nil {
		if fresh, err := p.listers.HAProxyCfgs.HAProxyCfgs(cfg.Namespace).Get(cfg.Name); err == nil {
			return fresh.Status.DeployedToPods
		}
	}
	if fresh, err := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs(cfg.Namespace).
		Get(ctx, cfg.Name, metav1.GetOptions{}); err == nil {
		return fresh.Status.DeployedToPods
	}
	return cfg.Status.DeployedToPods
}

// generationIfCurrentSpec returns the live spec's generation when checksum is
// exactly what it carries. An exact match needs no history: that checksum IS
// this generation. Lister first, API fallback; (0,false) when it does not match
// or the object is unreadable.
func (p *Publisher) generationIfCurrentSpec(ctx context.Context, namespace, name, checksum string) (int64, bool) {
	if checksum == "" {
		return 0, false
	}
	match := func(cfg *haproxyv1alpha1.HAProxyCfg) (int64, bool) {
		if cfg.Spec.Checksum == checksum && cfg.Generation > 0 {
			return cfg.Generation, true
		}
		return 0, false
	}
	// A lister MISS is not an answer, only a cheap "not from here": the cache
	// can lag the spec write that just happened, and treating its stale
	// checksum as authoritative would decline the one probe that unpins a pod.
	// So the lister can only ever confirm, never deny — a non-match falls
	// through to the API. Affordable because this runs only when the recorded
	// map already missed, which is the uncommon path.
	if p.listers != nil && p.listers.HAProxyCfgs != nil {
		if cfg, err := p.listers.HAProxyCfgs.HAProxyCfgs(namespace).Get(name); err == nil {
			if gen, ok := match(cfg); ok {
				return gen, true
			}
		}
	}
	cfg, err := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs(namespace).
		Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return 0, false
	}
	return match(cfg)
}

// learnSpecGeneration records a checksum→generation pair discovered outside the
// publish path. Nil-safe on specGens, like recordSpecGeneration.
func (p *Publisher) learnSpecGeneration(namespace, name, checksum string, generation int64) {
	if p.specGens == nil {
		return
	}
	p.specGens.record(namespace, name, checksum, generation)
}
