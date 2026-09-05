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

package events

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

var errMissingRenderOccurrence = errors.New("render occurrence is missing")

type renderOccurrenceCarrier struct {
	occurrence *rendercycle.Occurrence
}

type renderOccurrenceIdentity struct {
	cycle             *rendercycle.Snapshot
	output            *renderoutput.Snapshot
	artifacts         *renderartifact.Snapshot
	statusPatches     *templating.StatusPatchSnapshot
	renderedEvents    *templating.RenderedEventSnapshot
	renderedResources *templating.RenderedResourceSnapshot
	proof             string
	config            string
	planID            string
	contentChecksum   string
	counts            renderoutput.Counts
}

func newRenderOccurrenceCarrier(occurrence *rendercycle.Occurrence) (renderOccurrenceCarrier, error) {
	if occurrence == nil {
		return renderOccurrenceCarrier{}, errMissingRenderOccurrence
	}
	if err := occurrence.ValidateAuthentication(); err != nil {
		return renderOccurrenceCarrier{}, fmt.Errorf("render occurrence: %w", err)
	}
	return renderOccurrenceCarrier{occurrence: occurrence}, nil
}

func createRenderOccurrenceCarrier(snapshot *rendercycle.Snapshot) (renderOccurrenceCarrier, error) {
	occurrence, err := rendercycle.NewOccurrence(snapshot)
	if err != nil {
		return renderOccurrenceCarrier{}, fmt.Errorf("create render occurrence: %w", err)
	}
	return newRenderOccurrenceCarrier(occurrence)
}

// RenderOccurrence returns the exact opaque occurrence carried by the event.
func (c renderOccurrenceCarrier) RenderOccurrence() (*rendercycle.Occurrence, error) {
	if c.occurrence == nil {
		return nil, errMissingRenderOccurrence
	}
	if err := c.occurrence.ValidateAuthentication(); err != nil {
		return nil, fmt.Errorf("render occurrence: %w", err)
	}
	return c.occurrence, nil
}

// AuthenticatedRenderIdentity returns the exact cycle and diagnostic proof.
func (c renderOccurrenceCarrier) AuthenticatedRenderIdentity() (*rendercycle.Snapshot, string, error) {
	occurrence, err := c.RenderOccurrence()
	if err != nil {
		return nil, "", err
	}
	snapshot, err := occurrence.Snapshot()
	if err != nil {
		return nil, "", fmt.Errorf("render occurrence snapshot: %w", err)
	}
	proof, err := occurrence.Proof()
	if err != nil {
		return nil, "", fmt.Errorf("render occurrence proof: %w", err)
	}
	return snapshot, proof, nil
}

func inspectRenderOccurrence(occurrence *rendercycle.Occurrence) (renderOccurrenceCarrier, *renderOccurrenceIdentity, error) {
	carrier, err := newRenderOccurrenceCarrier(occurrence)
	if err != nil {
		return renderOccurrenceCarrier{}, nil, err
	}
	cycle, proof, err := carrier.AuthenticatedRenderIdentity()
	if err != nil {
		return renderOccurrenceCarrier{}, nil, err
	}
	output, err := cycle.OutputSnapshot()
	if err != nil {
		return renderOccurrenceCarrier{}, nil, fmt.Errorf("render occurrence output: %w", err)
	}
	artifacts, err := output.ArtifactSnapshot()
	if err != nil {
		return renderOccurrenceCarrier{}, nil, fmt.Errorf("render occurrence artifacts: %w", err)
	}
	statusPatches, err := cycle.StatusPatchSnapshot()
	if err != nil {
		return renderOccurrenceCarrier{}, nil, fmt.Errorf("render occurrence status patches: %w", err)
	}
	renderedEvents, err := cycle.RenderedEventSnapshot()
	if err != nil {
		return renderOccurrenceCarrier{}, nil, fmt.Errorf("render occurrence events: %w", err)
	}
	renderedResources, err := cycle.RenderedResourceSnapshot()
	if err != nil {
		return renderOccurrenceCarrier{}, nil, fmt.Errorf("render occurrence resources: %w", err)
	}
	config, err := output.Config()
	if err != nil {
		return renderOccurrenceCarrier{}, nil, fmt.Errorf("render occurrence config: %w", err)
	}
	planID, err := output.PlanID()
	if err != nil {
		return renderOccurrenceCarrier{}, nil, fmt.Errorf("render occurrence plan ID: %w", err)
	}
	contentChecksum, err := cycle.ContentChecksum()
	if err != nil {
		return renderOccurrenceCarrier{}, nil, fmt.Errorf("render occurrence checksum: %w", err)
	}
	counts, err := output.Counts()
	if err != nil {
		return renderOccurrenceCarrier{}, nil, fmt.Errorf("render occurrence counts: %w", err)
	}
	return carrier, &renderOccurrenceIdentity{
		cycle: cycle, output: output, artifacts: artifacts,
		statusPatches: statusPatches, renderedEvents: renderedEvents,
		renderedResources: renderedResources, proof: proof, config: config,
		planID: planID, contentChecksum: contentChecksum, counts: counts,
	}, nil
}

func mustInspectRenderOccurrence(carrier renderOccurrenceCarrier) *renderOccurrenceIdentity {
	occurrence, err := carrier.RenderOccurrence()
	if err != nil {
		panic(fmt.Sprintf("invalid authenticated render event: %v", err))
	}
	_, identity, err := inspectRenderOccurrence(occurrence)
	if err != nil {
		panic(fmt.Sprintf("invalid authenticated render event: %v", err))
	}
	return identity
}

func cloneStatusPatches(patches []templating.StatusPatch) []templating.StatusPatch {
	cloned := slices.Clone(patches)
	for index := range cloned {
		if cloned[index].Variants == nil {
			continue
		}
		cloned[index].Variants = make(map[string]map[string]any, len(cloned[index].Variants))
		for phase, variant := range patches[index].Variants {
			cloned[index].Variants[phase] = cloneStringAnyMap(variant)
		}
	}
	return cloned
}

func cloneRenderedResources(resources []templating.RenderedResource) []templating.RenderedResource {
	cloned := slices.Clone(resources)
	for index := range cloned {
		cloned[index].Object = cloneStringAnyMap(resources[index].Object)
	}
	return cloned
}

func cloneStringAnyMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		clonedValue := cloneCollectionValue(reflect.ValueOf(value))
		if !clonedValue.IsValid() {
			cloned[key] = nil
			continue
		}
		cloned[key] = clonedValue.Interface()
	}
	return cloned
}

func cloneCollectionValue(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return value
	}
	switch value.Kind() {
	case reflect.Interface:
		return cloneInterfaceValue(value)
	case reflect.Map:
		return cloneMapValue(value)
	case reflect.Slice:
		return cloneSliceValue(value)
	case reflect.Array:
		return cloneArrayValue(value)
	case reflect.Pointer:
		return clonePointerValue(value)
	default:
		return value
	}
}

func cloneInterfaceValue(value reflect.Value) reflect.Value {
	if value.IsNil() {
		return reflect.Zero(value.Type())
	}
	cloned := cloneCollectionValue(value.Elem())
	wrapped := reflect.New(value.Type()).Elem()
	wrapped.Set(cloned)
	return wrapped
}

func cloneMapValue(value reflect.Value) reflect.Value {
	if value.IsNil() {
		return reflect.Zero(value.Type())
	}
	cloned := reflect.MakeMapWithSize(value.Type(), value.Len())
	iterator := value.MapRange()
	for iterator.Next() {
		cloned.SetMapIndex(cloneCollectionValue(iterator.Key()), cloneCollectionValue(iterator.Value()))
	}
	return cloned
}

func cloneSliceValue(value reflect.Value) reflect.Value {
	if value.IsNil() {
		return reflect.Zero(value.Type())
	}
	cloned := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
	for index := range value.Len() {
		cloned.Index(index).Set(cloneCollectionValue(value.Index(index)))
	}
	return cloned
}

func cloneArrayValue(value reflect.Value) reflect.Value {
	cloned := reflect.New(value.Type()).Elem()
	for index := range value.Len() {
		cloned.Index(index).Set(cloneCollectionValue(value.Index(index)))
	}
	return cloned
}

func clonePointerValue(value reflect.Value) reflect.Value {
	if value.IsNil() {
		return reflect.Zero(value.Type())
	}
	cloned := reflect.New(value.Type().Elem())
	cloned.Elem().Set(cloneCollectionValue(value.Elem()))
	return cloned
}

func cloneOperationBreakdown(source map[string]int) map[string]int {
	return maps.Clone(source)
}
