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

package e2ecluster

import (
	"fmt"
	"strings"
)

const (
	GatewayAPIChannelStandard     = "standard"
	GatewayAPIChannelExperimental = "experimental"
	applyCommand                  = "apply"
	kubeconfigFlag                = "--kubeconfig"
)

func normalizeGatewayAPIChannel(channel string) (string, error) {
	if channel == "" {
		return GatewayAPIChannelStandard, nil
	}
	if channel != GatewayAPIChannelStandard && channel != GatewayAPIChannelExperimental {
		return "", fmt.Errorf("gateway API channel must be standard or experimental, got %q", channel)
	}
	return channel, nil
}

// GatewayAPIInstallArgs returns kubectl arguments for one upstream CRD channel.
func GatewayAPIInstallArgs(version, channel, kubeconfig string) ([]string, error) {
	var err error
	channel, err = normalizeGatewayAPIChannel(channel)
	if err != nil {
		return nil, err
	}
	if version == "" {
		return nil, fmt.Errorf("gateway API version is empty")
	}

	args := []string{applyCommand, kubeconfigFlag, kubeconfig}
	if strings.HasPrefix(version, "v") {
		manifest := fmt.Sprintf(
			"https://github.com/kubernetes-sigs/gateway-api/releases/download/%s/%s-install.yaml",
			version,
			channel,
		)
		args = append(args, "-f", manifest)
	} else {
		path := "config/crd"
		if channel == GatewayAPIChannelExperimental {
			path += "/experimental"
		}
		args = append(args, "-k", fmt.Sprintf("github.com/kubernetes-sigs/gateway-api/%s?ref=%s", path, version))
	}
	if channel == GatewayAPIChannelExperimental {
		args = append(args, "--server-side")
	}
	return args, nil
}

// GatewayAPIHelmArgs enables chart validation for the installed CRD channel.
func GatewayAPIHelmArgs(channel string) ([]string, error) {
	channel, err := normalizeGatewayAPIChannel(channel)
	if err != nil {
		return nil, err
	}
	if channel == GatewayAPIChannelExperimental {
		return []string{"--set", "controller.templateLibraries.gateway.experimentalChannel=true"}, nil
	}
	return nil, nil
}
