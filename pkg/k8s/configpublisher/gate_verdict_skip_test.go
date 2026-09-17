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
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

func acceptedVerdict() *GateVerdict {
	return &GateVerdict{
		Namespace: "haptic", Name: "test-config-haproxycfg", PlanID: "plan-1", Accepted: true,
	}
}

func TestApplyGateVerdict_SkipsUnchangedVerdictWithinInterval(t *testing.T) {
	publisher, crdClient := gateVerdictPublisher(t, runtimeConfigFixture())
	publisher.SetRepublishInterval(time.Hour)

	require.NoError(t, publisher.ApplyGateVerdict(t.Context(), acceptedVerdict()))
	crdClient.ClearActions()

	require.NoError(t, publisher.ApplyGateVerdict(t.Context(), acceptedVerdict()))
	assert.Empty(t, crdClient.Actions(),
		"an unchanged verdict inside the republish interval must not touch the API at all")
}

func TestApplyGateVerdict_NoSkipWhenIntervalUnset(t *testing.T) {
	publisher, crdClient := gateVerdictPublisher(t, runtimeConfigFixture())

	require.NoError(t, publisher.ApplyGateVerdict(t.Context(), acceptedVerdict()))
	crdClient.ClearActions()

	require.NoError(t, publisher.ApplyGateVerdict(t.Context(), acceptedVerdict()))
	assert.NotEmpty(t, crdClient.Actions(),
		"skip is opt-in; a publisher without a republish interval keeps the old behavior")
}

func TestApplyGateVerdict_ReappliesAfterInterval(t *testing.T) {
	publisher, crdClient := gateVerdictPublisher(t, runtimeConfigFixture())
	publisher.SetRepublishInterval(time.Nanosecond)

	require.NoError(t, publisher.ApplyGateVerdict(t.Context(), acceptedVerdict()))
	crdClient.ClearActions()

	require.NoError(t, publisher.ApplyGateVerdict(t.Context(), acceptedVerdict()))
	assert.NotEmpty(t, crdClient.Actions(),
		"past the republish interval the verdict is the authoritative self-heal and must hit the API")
}

func TestApplyGateVerdict_ReappliesOnVerdictChange(t *testing.T) {
	publisher, crdClient := gateVerdictPublisher(t, runtimeConfigFixture())
	publisher.SetRepublishInterval(time.Hour)

	require.NoError(t, publisher.ApplyGateVerdict(t.Context(), acceptedVerdict()))
	crdClient.ClearActions()

	changed := acceptedVerdict()
	changed.Accepted = false
	changed.Refused = true
	changed.Message = "[ALERT] unknown keyword 'bogus'"
	require.NoError(t, publisher.ApplyGateVerdict(t.Context(), changed))

	cfg, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("haptic").Get(t.Context(), "test-config-haproxycfg", metav1.GetOptions{})
	require.NoError(t, err)
	condition := meta.FindStatusCondition(cfg.Status.Conditions, ConditionConfigValidated)
	require.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionFalse, condition.Status,
		"a changed verdict must reach the status")
}

func TestApplyGateVerdict_MissingTargetIsNotRecorded(t *testing.T) {
	publisher, crdClient := gateVerdictPublisher(t)
	publisher.SetRepublishInterval(time.Hour)

	// The HAProxyCfg does not exist yet: not an error, but also not a
	// publication the skip may trust.
	require.NoError(t, publisher.ApplyGateVerdict(t.Context(), acceptedVerdict()))

	_, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("haptic").Create(t.Context(), runtimeConfigFixture(), metav1.CreateOptions{})
	require.NoError(t, err)

	require.NoError(t, publisher.ApplyGateVerdict(t.Context(), acceptedVerdict()))
	cfg, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("haptic").Get(t.Context(), "test-config-haproxycfg", metav1.GetOptions{})
	require.NoError(t, err)
	assert.NotNil(t, meta.FindStatusCondition(cfg.Status.Conditions, ConditionConfigValidated),
		"once the target exists the same verdict must land on it, not be skipped")
}

func TestApplyGateVerdict_ErrorClearsAppliedState(t *testing.T) {
	publisher, crdClient := gateVerdictPublisher(t, runtimeConfigFixture())
	publisher.SetRepublishInterval(time.Hour)

	failing := errors.New("injected update failure")
	crdClient.PrependReactor("update", "haproxycfgs",
		func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, failing })
	require.Error(t, publisher.ApplyGateVerdict(t.Context(), acceptedVerdict()))
	crdClient.ReactionChain = crdClient.ReactionChain[1:]

	require.NoError(t, publisher.ApplyGateVerdict(t.Context(), acceptedVerdict()))
	cfg, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs("haptic").Get(t.Context(), "test-config-haproxycfg", metav1.GetOptions{})
	require.NoError(t, err)
	assert.NotNil(t, meta.FindStatusCondition(cfg.Status.Conditions, ConditionConfigValidated),
		"after a failed write the state is unknown; the next identical verdict must not skip")
}
