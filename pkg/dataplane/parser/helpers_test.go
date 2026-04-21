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

package parser

import "testing"

// newTestParser constructs a Parser for tests, failing the test fataly if
// construction errors. Every test in this package needs a freshly-built Parser
// and failing fast is the right response when New() returns an error —
// extracting this helper keeps that boilerplate out of each test body.
func newTestParser(t *testing.T) *Parser {
	t.Helper()
	p, err := New()
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	return p
}
