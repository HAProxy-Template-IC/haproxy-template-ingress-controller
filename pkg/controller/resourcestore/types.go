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

// Package resourcestore provides centralized management of resource stores
// with memory-efficient overlay support for dry-run validation.
//
// This package enables webhook validation to simulate resource changes without
// copying entire stores, using a shadow/overlay pattern that only tracks deltas.
package resourcestore

import (
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// Store is the interface that resource stores must implement.
// This is an alias to the k8s.Store interface for convenience.
type Store = types.Store

// Operation represents the type of resource operation in an overlay.
type Operation string

const (
	// OperationCreate indicates a new resource is being added.
	OperationCreate Operation = "CREATE"

	// OperationUpdate indicates an existing resource is being modified.
	OperationUpdate Operation = "UPDATE"

	// OperationDelete indicates a resource is being removed.
	OperationDelete Operation = "DELETE"
)
