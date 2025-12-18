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

// FilterFunc is a template filter function signature.
// Template filters transform values within template expressions:
//
//	{{ value | filter_name(arg1, arg2) }}
//
// FilterFunc receives:
//   - in: The value being filtered (left side of | operator)
//   - args: Additional filter arguments from the template
//
// Example:
//
//	func upperFilter(in interface{}, args ...interface{}) (interface{}, error) {
//	    str, ok := in.(string)
//	    if !ok {
//	        return nil, fmt.Errorf("upper filter requires string input")
//	    }
//	    return strings.ToUpper(str), nil
//	}
type FilterFunc func(in interface{}, args ...interface{}) (interface{}, error)

// GlobalFunc is a template global function signature.
// Global functions are called directly in templates:
//
//	{{ function_name(arg1, arg2) }}
//
// GlobalFunc receives variadic arguments from the template call.
//
// Example:
//
//	func mergeFunc(args ...interface{}) (interface{}, error) {
//	    if len(args) != 2 {
//	        return nil, fmt.Errorf("merge requires exactly 2 arguments")
//	    }
//	    // ... merge logic
//	}
type GlobalFunc func(args ...interface{}) (interface{}, error)

// IncludeStats represents timing statistics for template includes/renders.
// Used for performance profiling to identify slow templates.
type IncludeStats struct {
	// Name is the name of the included/rendered template.
	Name string

	// Count is the number of times this template was rendered.
	// Templates rendered multiple times (e.g., in loops) have count > 1.
	Count int

	// TotalMs is the cumulative time spent rendering this template in milliseconds.
	// For templates rendered multiple times, this is the sum of all renders.
	TotalMs float64

	// AvgMs is the average time per render in milliseconds (TotalMs / Count).
	AvgMs float64

	// MaxMs is the maximum time for a single render in milliseconds.
	MaxMs float64
}
