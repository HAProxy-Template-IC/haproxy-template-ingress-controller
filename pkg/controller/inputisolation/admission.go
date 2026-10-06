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

package inputisolation

import (
	"context"

	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

type rejectedIdentity struct {
	store     string
	namespace string
	name      string
}

// IsolatedInputs retains every observed change except exact rejected revisions.
func (s *Service) IsolatedInputs(ctx context.Context, observed stores.StoreProvider) (stores.StoreProvider, bool, error) {
	accepted := s.accepted.Load()
	if accepted == nil || len(accepted.rejections) == 0 {
		return nil, false, nil
	}
	groups, err := s.captureChanges(ctx, observed, accepted.branches)
	if err != nil {
		return nil, false, err
	}
	rejected := make(map[rejectedIdentity]*Rejection, len(accepted.rejections))
	for index := range accepted.rejections {
		rejection := &accepted.rejections[index]
		rejected[rejectedIdentity{rejection.Store, rejection.Namespace, rejection.Name}] = rejection
	}
	selected := make([]changeGroup, 0, len(groups))
	omitted := false
	for _, group := range groups {
		if groupMatchesRejections(group, rejected) {
			omitted = true
			continue
		}
		selected = append(selected, group)
	}
	if !omitted {
		return nil, false, nil
	}
	return branchProvider(applyGroups(accepted.branches, selected)), true, nil
}

func groupMatchesRejections(group changeGroup, rejected map[rejectedIdentity]*Rejection) bool {
	matched := false
	for alias, changes := range group.changes {
		for _, change := range changes {
			if !changeMatchesRejection(alias, change, rejected) {
				return false
			}
			matched = true
		}
	}
	return matched
}

func changeMatchesRejection(alias string, change k8sstore.BranchChange, rejected map[rejectedIdentity]*Rejection) bool {
	rejection := rejected[rejectedIdentity{alias, change.Namespace(), change.Name()}]
	return rejection != nil && rejection.Deleted == change.Deleted() && rejection.revision == change.Revision()
}
