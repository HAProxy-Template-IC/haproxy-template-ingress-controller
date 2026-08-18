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
		next := parseEpoch(lease.Annotations[EpochAnnotation]) + 1
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

// parseEpoch reads the annotation, treating anything unreadable as none: a
// hand-edited value must not make the next epoch lower than one already sent.
func parseEpoch(value string) uint64 {
	epoch, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0
	}
	return epoch
}
