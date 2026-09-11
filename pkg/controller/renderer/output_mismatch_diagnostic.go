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
	"errors"
	"fmt"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func (s *RenderService) reportOutputPublicationMismatch(
	publicationErr error,
	previous *renderoutput.Snapshot,
	input *dataplane.AuxiliaryFiles,
	transition *rendercontext.DocumentPlanTransition,
	artifacts *renderartifact.Snapshot,
) {
	var mismatch *renderoutput.ArtifactContentMismatchError
	if s.logger == nil || !errors.As(publicationErr, &mismatch) || mismatch == nil {
		return
	}
	s.logger.Error("Rendered output rejected", "path", mismatch.Path,
		"plan_bytes", mismatch.PlanBytes, "artifact_bytes", mismatch.ArtifactBytes,
		"written", mismatch.Written, "matched", mismatch.Matched,
		"read_ok", mismatch.ReadOK, "exact_read", mismatch.ExactRead, "plan_digest_valid", mismatch.PlanDigestValid)
	if transition == nil {
		return
	}
	err := s.reportOutputPublicationFile(mismatch.Path, previous, input, transition, artifacts)
	if err != nil {
		s.logger.Error("Output publication diagnostic failed", "error", err)
	}
}

func (s *RenderService) reportOutputPublicationFile(
	path string,
	previous *renderoutput.Snapshot,
	input *dataplane.AuxiliaryFiles,
	transition *rendercontext.DocumentPlanTransition,
	artifacts *renderartifact.Snapshot,
) error {
	plan, err := transition.Plan.LegacyCopy()
	if err != nil {
		return err
	}
	fresh, err := dataplane.BuildAuxiliaryFileSnapshotWithRuntimePaths(
		s.artifactAuthority, nil, input, s.resolveAuxiliaryRuntimePath,
	)
	if err != nil {
		return err
	}
	inputContents, err := outputDiagnosticArtifactContents(fresh)
	if err != nil {
		return err
	}
	artifactContents, err := outputDiagnosticArtifactContents(artifacts)
	if err != nil {
		return err
	}
	deltaContents, err := outputDiagnosticPlanDelta(transition.PlanDelta)
	if err != nil {
		return err
	}
	previousContents := map[string]string{}
	if previous != nil {
		base, baseErr := previous.ArtifactSnapshot()
		if baseErr != nil {
			return baseErr
		}
		previousContents, err = outputDiagnosticArtifactContents(base)
		if err != nil {
			return err
		}
	}
	for _, file := range plan.Files {
		if file.Path != path || file.Kind == renderplan.FileKindConfig {
			continue
		}
		inputText, inputFound := inputContents[file.Path]
		artifactText, artifactFound := artifactContents[file.Path]
		deltaText, deltaFound := deltaContents[file.Path]
		previousText, previousFound := previousContents[file.Path]
		s.logger.Error("Rejected output file evidence", "path", file.Path,
			"plan_bytes", len(file.Content), "input_bytes", len(inputText), "artifact_bytes", len(artifactText),
			"input_found", inputFound, "artifact_found", artifactFound, "delta_found", deltaFound,
			"input_equals_plan", inputText == file.Content, "artifact_equals_plan", artifactText == file.Content,
			"delta_equals_plan", deltaText == file.Content,
			"plan_equals_previous", previousFound && previousText == file.Content,
			"artifact_equals_previous", previousFound && previousText == artifactText)
	}
	return nil
}

func outputDiagnosticArtifactContents(snapshot *renderartifact.Snapshot) (map[string]string, error) {
	contents := map[string]string{}
	err := snapshot.Walk(func(artifact *renderartifact.Artifact) error {
		descriptor, err := artifact.Descriptor()
		if err != nil {
			return err
		}
		content, err := artifact.Content()
		if err != nil {
			return err
		}
		text, err := content.String()
		if err != nil {
			return err
		}
		if _, duplicate := contents[descriptor.RuntimePath]; duplicate {
			return fmt.Errorf("duplicate diagnostic path %q", descriptor.RuntimePath)
		}
		contents[descriptor.RuntimePath] = text
		return nil
	})
	return contents, err
}

func outputDiagnosticPlanDelta(delta *renderplan.Delta) (map[string]string, error) {
	contents := map[string]string{}
	if delta == nil {
		return contents, nil
	}
	changes, err := delta.Changes()
	if err != nil {
		return nil, err
	}
	for _, change := range changes.Files {
		if change.After == nil {
			continue
		}
		file, err := change.After.LegacyCopy()
		if err != nil {
			return nil, err
		}
		contents[file.Path] = file.Content
	}
	return contents, nil
}
