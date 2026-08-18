//go:build integration

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

package integration

import (
	"fmt"
	"os"

	"gitlab.com/haproxy-haptic/haptic/tests/kindutil"
)

// defaultHAProxyVersion is the bracket a local run uses; the CI matrix sets
// HAPROXY_VERSION per job.
const defaultHAProxyVersion = "3.2"

// HAProxyVersion is the HAProxy release under test, as "major.minor".
func HAProxyVersion() string {
	if version := os.Getenv("HAPROXY_VERSION"); version != "" {
		return version
	}
	return defaultHAProxyVersion
}

// baseHAProxyImage is the upstream image the pod's haproxy container runs.
func baseHAProxyImage() string {
	return "haproxytech/haproxy-debian:" + HAProxyVersion()
}

// agentImageTag names the image both containers of the test pod run: the
// HAProxy image with the haptic binary laid in, exactly like the chart, where
// the agent container runs the controller image's binary against the HAProxy
// pod's mounts.
func agentImageTag() string {
	return fmt.Sprintf("haptic-integration-test:%s-%d", HAProxyVersion(), os.Getpid())
}

// buildAndLoadAgentImage builds the pod image once per run and puts it into the
// Kind node, which has no registry to pull it from.
func buildAndLoadAgentImage(cluster *KindCluster) (string, error) {
	if err := kindutil.DockerUsable(); err != nil {
		return "", fmt.Errorf("the integration suite needs a working docker: %w", err)
	}
	tag := agentImageTag()
	if err := kindutil.BuildHAProxyAgentImage(baseHAProxyImage(), tag); err != nil {
		return "", err
	}
	if err := cluster.LoadDockerImage(tag); err != nil {
		kindutil.RemoveImage(tag)
		return "", err
	}
	return tag, nil
}
