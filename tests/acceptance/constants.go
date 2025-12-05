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

//go:build acceptance

package acceptance

// CRD and API constants.
const (
	// HAProxyTemplateConfigAPIVersion is the API version for HAProxyTemplateConfig resources.
	HAProxyTemplateConfigAPIVersion = "haproxy-template-ic.gitlab.io/v1alpha1"

	// HAProxyTemplateConfigKind is the Kind name for HAProxyTemplateConfig resources.
	HAProxyTemplateConfigKind = "HAProxyTemplateConfig"

	// CRDDirectory is the path to the CRD manifests relative to the test directory.
	CRDDirectory = "../../charts/haproxy-template-ic/crds/"

	// ControllerImageName is the Docker image name used for acceptance tests.
	ControllerImageName = "haproxy-template-ic:test"
)

// RequiredCRDs lists the CRDs that must be installed for acceptance tests.
var RequiredCRDs = []string{
	"crd/haproxytemplateconfigs.haproxy-template-ic.gitlab.io",
	"crd/haproxycfgs.haproxy-template-ic.gitlab.io",
	"crd/haproxymapfiles.haproxy-template-ic.gitlab.io",
}

// Debug endpoint paths.
const (
	DebugPathConfig    = "/debug/vars/config"
	DebugPathRendered  = "/debug/vars/rendered"
	DebugPathPipeline  = "/debug/vars/pipeline"
	DebugPathValidated = "/debug/vars/validated"
	DebugPathErrors    = "/debug/vars/errors"
	DebugPathEvents    = "/debug/vars/events"
	DebugPathAuxFiles  = "/debug/vars/auxfiles"
)

// DebugServiceName returns the service name for a controller's debug endpoint.
func DebugServiceName(deploymentName string) string {
	return deploymentName + "-debug"
}

// MetricsServiceName returns the service name for a controller's metrics endpoint.
func MetricsServiceName(deploymentName string) string {
	return deploymentName + "-metrics"
}
