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

package rendercontext

import "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"

// CurrentConfigSource binds a lazily materialized currentConfig to an exact root.
type CurrentConfigSource interface {
	ValidateAuthentication() error
	SameRoot(CurrentConfigSource) (bool, error)
	MaterializeCurrentConfig() (*renderplan.CurrentConfig, error)
}

// CurrentAuxFilesSource binds lazily materialized currentFiles to an exact root.
type CurrentAuxFilesSource interface {
	ValidateAuthentication() error
	SameRoot(CurrentAuxFilesSource) (bool, error)
	MaterializeCurrentAuxFiles() (map[string]string, error)
}
