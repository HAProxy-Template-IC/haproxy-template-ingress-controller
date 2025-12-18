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

package templating

import "fmt"

// FailFunction is the standard "fail" template function that causes template
// rendering to abort with an error message. This function should be registered
// as a global function when creating template engines.
//
// Usage in templates:
//
//	{{ fail("Service not found") }}
//	{% if not service %}{{ fail("Service is required") }}{% endif %}
//
// The error message from fail() is extracted by dataplane.SimplifyRenderingError()
// to provide user-friendly feedback in admission webhooks and validation tests.
func FailFunction(args ...interface{}) (interface{}, error) {
	// Validate arguments
	if len(args) != 1 {
		return nil, fmt.Errorf("fail() requires exactly one string argument, got %d arguments", len(args))
	}

	message, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("fail() argument must be a string, got %T", args[0])
	}

	// Return error with the custom message
	// This will cause template rendering to fail and propagate the error
	// through the validation webhook to the user
	return nil, fmt.Errorf("%s", message)
}
