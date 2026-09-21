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

//go:build !gateway_conformance

package conformance

import "testing"

func TestReportInvocationRejectsIncompleteRuns(t *testing.T) {
	for _, tc := range []struct {
		name, run, skip, version string
		short, wantError         bool
	}{
		{name: "whole suite", version: "0.2.0-alpha.3"},
		{name: "one shard", run: "TestGatewayAPIConformance/HTTPRoute.*", version: "0.2.0-alpha.3", wantError: true},
		{name: "empty selection", run: "^$", version: "0.2.0-alpha.3", wantError: true},
		{name: "excluded tests", skip: "BackendTLS", version: "0.2.0-alpha.3", wantError: true},
		{name: "short mode", short: true, version: "0.2.0-alpha.3", wantError: true},
		{name: "unknown implementation", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateReportInvocation(tc.run, tc.skip, tc.version, tc.short)
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected report eligibility: %v", err)
			}
		})
	}
}
