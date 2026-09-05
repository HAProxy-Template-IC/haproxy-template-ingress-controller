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

import "gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"

type incrementalDerivedResource struct {
	Identity rendercontext.DerivedResourceIdentity
	Source   string
	Value    string
}

func ownValidatedIncrementalDerivedResource(
	entry *rendercontext.DerivedResource,
) incrementalDerivedResource {
	return incrementalDerivedResource{
		Identity: entry.Identity,
		Source:   string(entry.Source),
		Value:    string(entry.Value),
	}
}

func (r *incrementalDerivedResource) materialize() rendercontext.DerivedResource {
	return rendercontext.DerivedResource{
		Identity: r.Identity,
		Source:   []byte(r.Source),
		Value:    []byte(r.Value),
	}
}

func (r *incrementalDerivedResource) matches(entry *rendercontext.DerivedResource) bool {
	return entry != nil && r.Identity == entry.Identity &&
		stringBytesEqual(r.Source, entry.Source) && stringBytesEqual(r.Value, entry.Value)
}
