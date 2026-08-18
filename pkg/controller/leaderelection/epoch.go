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

package leaderelection

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

// EpochAnnotation carries the fencing epoch on the leader Lease.
const EpochAnnotation = "haptic.io/leader-epoch"

// LeaseEpoch is the fencing epoch of this controller's leadership terms: a
// counter on the leader Lease that each term increments before it dispatches
// anything. Every apply carries it, and a pod that has accepted a higher epoch
// refuses lower ones — so a controller that lost the lease but has not noticed
// yet cannot write over its successor.
//
// The Lease's own fields are not usable for this: the identity is the pod name,
// so client-go carries AcquireTime and LeaderTransitions across a same-identity
// re-acquire.
type LeaseEpoch struct {
	client    kubernetes.Interface
	namespace string
	name      string
	identity  string
	logger    *slog.Logger
	current   atomic.Uint64
}

// NewLeaseEpoch builds the epoch source for one Lease.
func NewLeaseEpoch(client kubernetes.Interface, namespace, name, identity string, logger *slog.Logger) *LeaseEpoch {
	if logger == nil {
		logger = slog.Default()
	}
	return &LeaseEpoch{client: client, namespace: namespace, name: name, identity: identity, logger: logger}
}

// LeaderEpoch is the epoch this controller last claimed. Zero means it has
// claimed none, which every pod's fence outranks.
func (e *LeaseEpoch) LeaderEpoch() uint64 {
	if e == nil {
		return 0
	}
	return e.current.Load()
}

// Identity is this controller's leader-election identity.
func (e *LeaseEpoch) Identity() string {
	if e == nil {
		return ""
	}
	return e.identity
}

// ErrForeignLeader reports that the Lease names another holder, or an epoch
// this controller never claimed: a newer leader owns the fleet.
var ErrForeignLeader = errors.New("the lease is held at a newer epoch")

// Bump claims the next epoch by incrementing the Lease annotation, retrying the
// read-modify-write while another writer wins the race. It must complete before
// this term dispatches: an unclaimed epoch is refused by every pod that has
// seen a higher one, which stalls deployments rather than corrupting them.
func (e *LeaseEpoch) Bump(ctx context.Context) error {
	if e == nil || e.client == nil {
		return nil
	}
	leases := e.client.CoordinationV1().Leases(e.namespace)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		lease, err := leases.Get(ctx, e.name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("reading lease %s/%s: %w", e.namespace, e.name, err)
		}
		current, err := parseEpoch(lease.Annotations[EpochAnnotation])
		if err != nil {
			return fmt.Errorf("lease %s/%s: %w", e.namespace, e.name, err)
		}
		next := current + 1
		if lease.Annotations == nil {
			lease.Annotations = map[string]string{}
		}
		lease.Annotations[EpochAnnotation] = strconv.FormatUint(next, 10)
		if _, err := leases.Update(ctx, lease, metav1.UpdateOptions{}); err != nil {
			return err
		}
		e.current.Store(next)
		e.logger.Info("Claimed leader epoch", "epoch", next, "lease", e.name, "identity", e.identity)
		return nil
	})
}

// Reclaim lifts this controller's epoch past one a pod has already accepted.
// A pod outranking the leader means the counter regressed — a Lease that was
// deleted, recreated or restored from a backup loses the annotation — so the
// Lease is re-read: while it still names this holder at the epoch this term
// claimed, no rival exists and the annotation is raised to floor+1. It reports
// ErrForeignLeader when the Lease proves otherwise, which is the one case where
// the pod is right and this controller must stop writing.
func (e *LeaseEpoch) Reclaim(ctx context.Context, floor uint64) (uint64, error) {
	if e == nil || e.client == nil {
		return 0, ErrForeignLeader
	}
	leases := e.client.CoordinationV1().Leases(e.namespace)
	var adopted uint64
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Re-read per attempt: the pods of one deployment reclaim concurrently,
		// and the first one to win raises the epoch for all of them.
		claimed := e.current.Load()
		if floor < claimed {
			adopted = claimed
			return nil
		}
		lease, err := leases.Get(ctx, e.name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("reading lease %s/%s: %w", e.namespace, e.name, err)
		}
		if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != e.identity {
			return ErrForeignLeader
		}
		if stored, err := parseEpoch(lease.Annotations[EpochAnnotation]); err == nil && stored > claimed {
			return ErrForeignLeader
		}
		next := floor + 1
		if lease.Annotations == nil {
			lease.Annotations = map[string]string{}
		}
		lease.Annotations[EpochAnnotation] = strconv.FormatUint(next, 10)
		if _, err := leases.Update(ctx, lease, metav1.UpdateOptions{}); err != nil {
			return err
		}
		e.current.Store(next)
		adopted = next
		e.logger.Warn("Raised the leader epoch past the fleet's",
			"epoch", next, "fleet_epoch", floor, "lease", e.name, "identity", e.identity)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return adopted, nil
}

// parseEpoch reads the annotation. An absent one is epoch zero — the first term
// on a fresh Lease — while an unreadable one is an error, because the counter it
// stands for is unknown and guessing low is an epoch some pod already outranks.
func parseEpoch(value string) (uint64, error) {
	if value == "" {
		return 0, nil
	}
	epoch, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("annotation %s is %q, not an epoch: %w", EpochAnnotation, value, err)
	}
	return epoch, nil
}
