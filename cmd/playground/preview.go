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

package main

import (
	"fmt"

	"github.com/haproxy-haptic/HAPTIC/pkg/controller/renderer"
	"github.com/haproxy-haptic/HAPTIC/pkg/dataplane"
	"github.com/haproxy-haptic/HAPTIC/pkg/dataplane/renderplan"
	"github.com/haproxy-haptic/HAPTIC/pkg/templating"
)

type previewResult struct {
	HAProxyConfig     string
	AuxiliaryFiles    *dataplane.AuxiliaryFiles
	Plan              *renderplan.Plan
	StatusPatches     []templating.StatusPatch
	Events            []templating.RenderedEvent
	RenderedResources []templating.RenderedResource
	DurationMs        int64
	IncludeStats      []templating.IncludeStats
}

func materializePreview(out *renderer.RenderResult) (*previewResult, error) {
	plan, err := out.MaterializePlan()
	if err != nil {
		return nil, fmt.Errorf("reading render plan: %w", err)
	}
	files, err := out.MaterializeAuxiliaryFiles()
	if err != nil {
		return nil, fmt.Errorf("reading rendered files: %w", err)
	}
	patches, err := out.MaterializeStatusPatches()
	if err != nil {
		return nil, fmt.Errorf("reading status patches: %w", err)
	}
	events, err := out.MaterializeEvents()
	if err != nil {
		return nil, fmt.Errorf("reading events: %w", err)
	}
	resources, err := out.MaterializeRenderedResources()
	if err != nil {
		return nil, fmt.Errorf("reading rendered resources: %w", err)
	}
	return &previewResult{
		HAProxyConfig: out.HAProxyConfig, AuxiliaryFiles: files, Plan: plan,
		StatusPatches: patches, Events: events, RenderedResources: resources,
		DurationMs: out.DurationMs, IncludeStats: out.IncludeStats,
	}, nil
}
