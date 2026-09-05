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

package renderer

import (
	"errors"
	"fmt"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

func (s *RenderService) commitIncrementalOutput(
	previous *renderoutput.Snapshot,
	documentDelta *rendercontent.DocumentDelta,
	planDelta *renderplan.Delta,
	artifactDelta *renderartifact.Delta,
) (*renderoutput.Snapshot, error) {
	if s == nil || s.outputAuthority == nil {
		return nil, errors.New("incremental output publication has no authority")
	}
	transaction, err := renderoutput.BeginTransaction(
		s.outputAuthority, previous, documentDelta, planDelta, artifactDelta,
	)
	if err != nil {
		return nil, fmt.Errorf("starting incremental output publication: %w", err)
	}
	next, _, err := transaction.Commit()
	if err != nil {
		return nil, fmt.Errorf("committing incremental output publication: %w", err)
	}
	return next, nil
}
