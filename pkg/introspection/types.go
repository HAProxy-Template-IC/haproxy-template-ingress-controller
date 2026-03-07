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

import (
	"maps"
	"sync"
	"sync/atomic"
)

// Func is a Var that computes its value on-demand by calling a function.
//
// This is useful for values that are expensive to compute or change frequently,
// as they are only calculated when actually queried.
//
// Example:
//
//	startTime := time.Now()
//	registry.Publish("uptime", Func(func() (interface{}, error) {
//	    return time.Since(startTime).String(), nil
//	}))
type Func func() (any, error)

// Get implements the Var interface by calling the function.
func (f Func) Get() (any, error) {
	return f()
}

// IntVar is a thread-safe 64-bit integer variable.
//
// Provides atomic operations for reading and modifying the value.
// Similar to expvar.Int but instance-based rather than global.
//
// Example:
//
//	counter := NewInt(0)
//	registry.Publish("request_count", counter)
//	counter.Add(1)  // Increment counter
type IntVar struct {
	value atomic.Int64
}

// NewInt creates a new IntVar with the specified initial value.
func NewInt(initial int64) *IntVar {
	v := &IntVar{}
	v.value.Store(initial)
	return v
}

// Get implements the Var interface.
func (v *IntVar) Get() (any, error) {
	return v.value.Load(), nil
}

// Set sets the value to the specified integer.
func (v *IntVar) Set(value int64) {
	v.value.Store(value)
}

// Add adds delta to the value.
func (v *IntVar) Add(delta int64) {
	v.value.Add(delta)
}

// Value returns the current value.
func (v *IntVar) Value() int64 {
	return v.value.Load()
}

// StringVar is a thread-safe string variable.
//
// Example:
//
//	status := NewString("initializing")
//	registry.Publish("status", status)
//	status.Set("running")
type StringVar struct {
	mu    sync.RWMutex
	value string
}

// NewString creates a new StringVar with the specified initial value.
func NewString(initial string) *StringVar {
	return &StringVar{value: initial}
}

// Get implements the Var interface.
func (v *StringVar) Get() (any, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.value, nil
}

// Set sets the value to the specified string.
func (v *StringVar) Set(value string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.value = value
}

// Value returns the current value.
func (v *StringVar) Value() string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.value
}

// FloatVar is a thread-safe float64 variable.
//
// Example:
//
//	temperature := NewFloat(0.0)
//	registry.Publish("cpu_temperature", temperature)
//	temperature.Set(45.2)
type FloatVar struct {
	mu    sync.RWMutex
	value float64
}

// NewFloat creates a new FloatVar with the specified initial value.
func NewFloat(initial float64) *FloatVar {
	return &FloatVar{value: initial}
}

// Get implements the Var interface.
func (v *FloatVar) Get() (any, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.value, nil
}

// Set sets the value to the specified float.
func (v *FloatVar) Set(value float64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.value = value
}

// Add adds delta to the value.
func (v *FloatVar) Add(delta float64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.value += delta
}

// Value returns the current value.
func (v *FloatVar) Value() float64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.value
}

// MapVar is a thread-safe map variable.
//
// Useful for publishing structured data that changes over time.
//
// Example:
//
//	stats := NewMap()
//	registry.Publish("stats", stats)
//	stats.Set("requests", 100)
//	stats.Set("errors", 5)
type MapVar struct {
	mu   sync.RWMutex
	data map[string]any
}

// NewMap creates a new MapVar.
func NewMap() *MapVar {
	return &MapVar{
		data: make(map[string]any),
	}
}

// Get implements the Var interface.
func (v *MapVar) Get() (any, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	// Return a copy to prevent external modification
	result := make(map[string]any, len(v.data))
	maps.Copy(result, v.data)

	return result, nil
}

// Set sets a key to the specified value.
func (v *MapVar) Set(key string, value any) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.data[key] = value
}

// Delete removes a key from the map.
func (v *MapVar) Delete(key string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.data, key)
}

// Len returns the number of entries in the map.
func (v *MapVar) Len() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.data)
}
