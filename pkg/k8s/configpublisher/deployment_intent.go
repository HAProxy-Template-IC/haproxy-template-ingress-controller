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
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
)

const kindTemplateConfig = "HAProxyTemplateConfig"

const deploymentIntentAnnotation = "haproxy-haptic.org/deployment-intent"

// DeploymentAuthority binds one writer to the template UID and leadership term.
type DeploymentAuthority struct {
	Namespace  string
	Name       string
	UID        types.UID
	Epoch      uint64
	Claim      string
	Standalone bool
	LeaseName  string
	Identity   string
}

type deploymentIntent struct {
	UID      types.UID `json:"uid"`
	Epoch    uint64    `json:"epoch"`
	Claim    string    `json:"claim"`
	PlanID   string    `json:"planID,omitempty"`
	Checksum string    `json:"checksum,omitempty"`
}

// ClaimDeploymentAuthority fences previous writers without changing the last intent.
func (p *Publisher) ClaimDeploymentAuthority(ctx context.Context, authority *DeploymentAuthority) error {
	if authority.Claim == "" {
		return errors.New("deployment authority has no claim")
	}
	return p.updateDeploymentIntent(ctx, authority, func(status *deploymentIntent) error {
		if !authority.Standalone && status.Epoch > authority.Epoch {
			return errors.New("deployment authority belongs to a newer controller term")
		}
		if status.UID != authority.UID {
			*status = deploymentIntent{UID: authority.UID}
		}
		status.Epoch, status.Claim = authority.Epoch, authority.Claim
		return nil
	})
}

// RecordDeploymentIntent must succeed before the first apply for this output.
func (p *Publisher) RecordDeploymentIntent(ctx context.Context, authority *DeploymentAuthority, planID, checksum string) error {
	if planID == "" || checksum == "" {
		return errors.New("deployment intent has no output identity")
	}
	return p.updateDeploymentIntent(ctx, authority, func(status *deploymentIntent) error {
		if status.UID != authority.UID || status.Claim != authority.Claim {
			return errors.New("deployment authority changed before apply")
		}
		status.Epoch, status.PlanID, status.Checksum = authority.Epoch, planID, checksum
		return nil
	})
}

// CheckDeploymentIntent refuses a checkpoint superseded before or during recovery.
func (p *Publisher) CheckDeploymentIntent(ctx context.Context, authority *DeploymentAuthority, planID, checksum string) error {
	lease, err := p.deploymentLease(ctx, authority, false)
	if err != nil {
		return err
	}
	status, err := readDeploymentIntent(lease, authority)
	if err != nil {
		return err
	}
	if status.UID != authority.UID || status.Claim != authority.Claim {
		return errors.New("deployment authority changed during recovery")
	}
	if status.PlanID != planID || status.Checksum != checksum {
		return errors.New("a newer deployment intent supersedes the retained configuration")
	}
	return nil
}

func (p *Publisher) updateDeploymentIntent(ctx context.Context, authority *DeploymentAuthority, update func(*deploymentIntent) error) error {
	return retry.OnError(retry.DefaultRetry, retriableWrite, func() error {
		lease, err := p.deploymentLease(ctx, authority, true)
		if err != nil {
			return err
		}
		status, err := readDeploymentIntent(lease, authority)
		if err != nil {
			return err
		}
		if err := update(&status); err != nil {
			return err
		}
		encoded, err := json.Marshal(status)
		if err != nil {
			return err
		}
		if lease.Annotations[deploymentIntentAnnotation] == string(encoded) {
			return nil
		}
		if lease.Annotations == nil {
			lease.Annotations = map[string]string{}
		}
		lease.Annotations[deploymentIntentAnnotation] = string(encoded)
		_, err = p.k8sClient.CoordinationV1().Leases(authority.Namespace).Update(ctx, lease, metav1.UpdateOptions{})
		return err
	})
}

func (p *Publisher) deploymentLease(ctx context.Context, authority *DeploymentAuthority, create bool) (*coordinationv1.Lease, error) {
	name := authority.LeaseName
	if authority.Standalone {
		name = stableResourceName(authority.Name, "-retained-state", authority.Name)
	}
	client := p.k8sClient.CoordinationV1().Leases(authority.Namespace)
	lease, err := client.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) && create && authority.Standalone {
		lease, err = client.Create(ctx, &coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: authority.Namespace, OwnerReferences: []metav1.OwnerReference{{APIVersion: apiVersionV1Alpha1, Kind: kindTemplateConfig, Name: authority.Name, UID: authority.UID, Controller: ptr.To(true)}},
		}}, metav1.CreateOptions{})
	}
	if err != nil {
		return nil, err
	}
	if authority.Standalone {
		if !ownedByTemplate(lease, authority.Name, authority.UID) {
			return nil, errors.New("retained-state Lease has a foreign owner")
		}
	} else {
		epoch, err := strconv.ParseUint(lease.Annotations["haptic.io/leader-epoch"], 10, 64)
		if err != nil || epoch != authority.Epoch || lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != authority.Identity {
			return nil, errors.New("deployment authority belongs to a newer controller term")
		}
	}
	return lease, nil
}

func readDeploymentIntent(lease *coordinationv1.Lease, authority *DeploymentAuthority) (deploymentIntent, error) {
	status := deploymentIntent{UID: authority.UID}
	if encoded := lease.Annotations[deploymentIntentAnnotation]; encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &status); err != nil {
			return status, fmt.Errorf("decoding deployment intent: %w", err)
		}
	}
	if authority.UID == "" {
		return status, errors.New("deployment intent belongs to another template UID")
	}
	return status, nil
}
