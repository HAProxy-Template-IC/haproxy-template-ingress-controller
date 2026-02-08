// Copyright 2025 Philipp Hossner
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

// Package names provides well-known string constants used across the controller layer.
//
// Centralizing these names prevents typos and enables safe refactoring through
// compile-time checks rather than runtime string matching.
package names

const (
	// MainTemplateName is the template name for the primary HAProxy configuration file.
	MainTemplateName = "haproxy.cfg"

	// HAProxyPodsResourceType is the resource type name for the auto-injected HAProxy
	// pod watcher. It appears in store maps, event filters, and rendering context.
	HAProxyPodsResourceType = "haproxy-pods"
)
