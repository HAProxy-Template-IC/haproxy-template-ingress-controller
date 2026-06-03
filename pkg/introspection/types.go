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

package introspection

// Func is a Var that computes its value on-demand by calling a function.
//
// This is useful for values that are expensive to compute or change frequently,
// as they are only calculated when actually queried.
//
// Example:
//
//	startTime := time.Now()
//	registry.Publish("uptime", Func(func() (any, error) {
//	    return time.Since(startTime).String(), nil
//	}))
type Func func() (any, error)

// Get implements the Var interface by calling the function.
func (f Func) Get() (any, error) {
	return f()
}
