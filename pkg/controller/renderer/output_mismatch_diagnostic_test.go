// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderer

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
)

func TestOutputMismatchDiagnosticDoesNotLogContents(t *testing.T) {
	service := newOutputPublicationService(t)
	var logs bytes.Buffer
	service.logger = slog.New(slog.NewTextHandler(&logs, nil))
	base := outputPublicationSnapshot(t, service, nil, "private-before")
	changed := outputPublicationSnapshot(t, service, base, "private-change")
	plan, err := base.PlanSnapshot()
	require.NoError(t, err)
	artifacts, err := changed.ArtifactSnapshot()
	require.NoError(t, err)
	document, err := base.ConfigDocument()
	require.NoError(t, err)
	legacyPlan, err := plan.LegacyCopy()
	require.NoError(t, err)
	rejected, mismatchErr := renderoutput.NewSnapshotFromDocument(service.outputAuthority, document, legacyPlan, artifacts, base)
	require.Nil(t, rejected)
	var mismatch *renderoutput.ArtifactContentMismatchError
	require.ErrorAs(t, mismatchErr, &mismatch)
	deltas := noOpOutputDeltas(t, service, base)
	reload := false
	input := &dataplane.AuxiliaryFiles{GeneralFiles: []auxiliaryfiles.GeneralFile{{
		Filename: "output.txt", Path: "files/output.txt", Content: "private-change\n", ReloadOnPush: &reload,
	}}}
	service.reportOutputPublicationMismatch(fmt.Errorf("sealing output: %w", mismatchErr), base, input, &rendercontext.DocumentPlanTransition{
		Plan: plan, PlanDelta: deltas.plan,
	}, artifacts)
	output := logs.String()
	assert.Contains(t, output, "Rejected output file evidence")
	assert.Contains(t, output, "Rendered output rejected")
	assert.Contains(t, output, "written=0 matched=0 read_ok=true exact_read=false plan_digest_valid=true")
	assert.Contains(t, output, "input_equals_plan=false")
	assert.Contains(t, output, "artifact_equals_plan=false")
	assert.Contains(t, output, "plan_equals_previous=true")
	assert.NotContains(t, output, "private-before")
	assert.NotContains(t, output, "private-change")
	assert.NotContains(t, output, "diagnostic failed")
	assert.NotContains(t, output, "haproxy.cfg")
	require.NoError(t, base.ValidateAuthentication())
}

func TestOutputMismatchDiagnosticIgnoresUnrelatedErrors(t *testing.T) {
	service := newOutputPublicationService(t)
	var logs bytes.Buffer
	service.logger = slog.New(slog.NewTextHandler(&logs, nil))
	service.reportOutputPublicationMismatch(nil, nil, nil, nil, nil)
	service.reportOutputPublicationMismatch(errors.New("private-error"), nil, nil, nil, nil)
	var typedNil *renderoutput.ArtifactContentMismatchError
	service.reportOutputPublicationMismatch(typedNil, nil, nil, nil, nil)
	assert.Empty(t, logs.String())
	service.logger = nil
	service.reportOutputPublicationMismatch(&renderoutput.ArtifactContentMismatchError{Path: "maps/test.map"}, nil, nil, nil, nil)
}

func TestOutputMismatchDiagnosticRetainsEvidenceWithoutPlan(t *testing.T) {
	service := newOutputPublicationService(t)
	var logs bytes.Buffer
	service.logger = slog.New(slog.NewTextHandler(&logs, nil))
	mismatch := &renderoutput.ArtifactContentMismatchError{Path: "maps/test.map", PlanBytes: 5, ArtifactBytes: 5}
	service.reportOutputPublicationMismatch(mismatch, nil, nil, nil, nil)
	assert.Contains(t, logs.String(), "Rendered output rejected")
	assert.NotContains(t, logs.String(), "diagnostic failed")
	logs.Reset()
	service.reportOutputPublicationMismatch(mismatch, nil, nil, &rendercontext.DocumentPlanTransition{}, nil)
	assert.Contains(t, logs.String(), "Rendered output rejected")
	assert.Contains(t, logs.String(), "Output publication diagnostic failed")
}
