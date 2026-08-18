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

//go:build agentdocker

package agent

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/tests/kindutil"
)

// defaultHAProxyVersion matches the bracket the docker job runs; HAPROXY_VERSION
// overrides it.
const defaultHAProxyVersion = "3.4"

var (
	imageOnce sync.Once
	imageName string
	imageErr  error
)

func haproxyVersion() string {
	if version := os.Getenv("HAPROXY_VERSION"); version != "" {
		return version
	}
	return defaultHAProxyVersion
}

func haproxyImage() string {
	return "haproxytech/haproxy-debian:" + haproxyVersion()
}

// requireAgentImage returns an image that is the HAProxy image plus the haptic
// binary: the agent then runs as its own container against the same mounts,
// which is the chart's topology.
func requireAgentImage(t *testing.T) string {
	t.Helper()
	if err := kindutil.DockerUsable(); err != nil {
		t.Skipf("tests/agent needs a working docker: %v", err)
	}
	imageOnce.Do(func() {
		imageName = fmt.Sprintf("haptic-agent-test:%s-%d", haproxyVersion(), os.Getpid())
		imageErr = kindutil.BuildHAProxyAgentImage(haproxyImage(), imageName)
	})
	switch {
	case errors.Is(imageErr, kindutil.ErrAgentUnavailable):
		t.Skipf("tests/agent skipped: %v", imageErr)
	case imageErr != nil:
		t.Fatalf("building the agent image: %v", imageErr)
	}
	return imageName
}
