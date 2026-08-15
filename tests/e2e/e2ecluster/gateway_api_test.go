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

//go:build e2e

package e2ecluster

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGatewayAPIInstallArgs(t *testing.T) {
	t.Parallel()
	const release = "v1.4.0"

	tests := []struct {
		name    string
		version string
		channel string
		want    []string
		wantErr string
	}{
		{
			name:    "default release channel",
			version: release,
			want: []string{
				applyCommand, kubeconfigFlag, "/tmp/e2e.kubeconfig", "-f",
				"https://github.com/kubernetes-sigs/gateway-api/releases/download/" + release + "/standard-install.yaml",
			},
		},
		{
			name:    "experimental release",
			version: release,
			channel: GatewayAPIChannelExperimental,
			want: []string{
				applyCommand, kubeconfigFlag, "/tmp/e2e.kubeconfig", "-f",
				"https://github.com/kubernetes-sigs/gateway-api/releases/download/" + release + "/experimental-install.yaml",
				"--server-side",
			},
		},
		{
			name:    "standard canary",
			version: "main",
			channel: GatewayAPIChannelStandard,
			want: []string{
				applyCommand, kubeconfigFlag, "/tmp/e2e.kubeconfig", "-k",
				"github.com/kubernetes-sigs/gateway-api/config/crd?ref=main",
			},
		},
		{
			name:    "experimental canary",
			version: "main",
			channel: GatewayAPIChannelExperimental,
			want: []string{
				applyCommand, kubeconfigFlag, "/tmp/e2e.kubeconfig", "-k",
				"github.com/kubernetes-sigs/gateway-api/config/crd/experimental?ref=main",
				"--server-side",
			},
		},
		{name: "invalid channel", version: release, channel: "beta", wantErr: "must be standard or experimental"},
		{name: "empty version", channel: GatewayAPIChannelStandard, wantErr: "version is empty"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := GatewayAPIInstallArgs(test.version, test.channel, "/tmp/e2e.kubeconfig")
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

func TestGatewayAPIHelmArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		channel string
		want    []string
		wantErr string
	}{
		{name: "default standard"},
		{name: "explicit standard", channel: GatewayAPIChannelStandard},
		{
			name:    "experimental validation",
			channel: GatewayAPIChannelExperimental,
			want: []string{
				"--set", "controller.templateLibraries.gateway.experimentalChannel=true",
			},
		},
		{name: "invalid", channel: "beta", wantErr: "must be standard or experimental"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := GatewayAPIHelmArgs(test.channel)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}
