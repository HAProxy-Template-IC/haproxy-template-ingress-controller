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
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func requireRenderPlan(t *testing.T, result *RenderResult) *renderplan.Plan {
	t.Helper()
	plan, err := result.MaterializePlan()
	require.NoError(t, err)
	return plan
}

func requireAuxiliaryFiles(t *testing.T, result *RenderResult) *dataplane.AuxiliaryFiles {
	t.Helper()
	files, err := result.MaterializeAuxiliaryFiles()
	require.NoError(t, err)
	return files
}

func requireRenderEvents(t *testing.T, result *RenderResult) []templating.RenderedEvent {
	t.Helper()
	events, err := result.MaterializeEvents()
	require.NoError(t, err)
	return events
}

func requireRenderedResources(t *testing.T, result *RenderResult) []templating.RenderedResource {
	t.Helper()
	resources, err := result.MaterializeRenderedResources()
	require.NoError(t, err)
	return resources
}
