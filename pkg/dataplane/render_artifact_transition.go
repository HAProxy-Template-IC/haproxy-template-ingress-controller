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

package dataplane

import "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"

// BuildAuxiliaryFileTransition seals files and their exact transition from previous.
func BuildAuxiliaryFileTransition(
	authority *renderartifact.Authority,
	previous *renderartifact.Snapshot,
	files *AuxiliaryFiles,
) (*renderartifact.Snapshot, *renderartifact.Delta, error) {
	return BuildAuxiliaryFileTransitionWithRuntimePaths(authority, previous, files, nil)
}

// BuildAuxiliaryFileTransitionWithRuntimePaths seals files with resolved plan paths.
func BuildAuxiliaryFileTransitionWithRuntimePaths(
	authority *renderartifact.Authority,
	previous *renderartifact.Snapshot,
	files *AuxiliaryFiles,
	resolve func(renderartifact.Family, string) (string, error),
) (*renderartifact.Snapshot, *renderartifact.Delta, error) {
	desired, err := BuildAuxiliaryFileSnapshotWithRuntimePaths(
		authority, previous, files, resolve,
	)
	if err != nil {
		return nil, nil, err
	}
	if previous == nil {
		return desired, nil, nil
	}
	return renderartifact.ReconcileSnapshot(authority, previous, desired)
}
