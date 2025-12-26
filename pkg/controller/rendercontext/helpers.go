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

package rendercontext

import (
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// SeparateHAProxyPodStore separates the haproxy-pods store from resource stores.
//
// This is needed because haproxy-pods goes in the controller namespace (controller.haproxy_pods),
// not in the resources namespace. This function extracts haproxy-pods and returns both the
// filtered resource stores and the haproxy pod store separately.
//
// Returns:
//   - resourceStores: All stores except haproxy-pods
//   - haproxyPodStore: The haproxy-pods store, or nil if not present
func SeparateHAProxyPodStore(stores map[string]types.Store) (resourceStores map[string]types.Store, haproxyPodStore types.Store) {
	resourceStores = make(map[string]types.Store)
	for resourceTypeName, store := range stores {
		if resourceTypeName == "haproxy-pods" {
			haproxyPodStore = store
		} else {
			resourceStores[resourceTypeName] = store
		}
	}
	return resourceStores, haproxyPodStore
}

// PathResolverFromValidationPaths creates a PathResolver from ValidationPaths.
//
// This is a convenience function for creating PathResolvers in contexts where
// ValidationPaths is available (testrunner, dryrunvalidator, benchmark).
func PathResolverFromValidationPaths(vp *dataplane.ValidationPaths) *templating.PathResolver {
	return &templating.PathResolver{
		MapsDir:    vp.MapsDir,
		SSLDir:     vp.SSLCertsDir,
		CRTListDir: vp.CRTListDir,
		GeneralDir: vp.GeneralStorageDir,
	}
}
