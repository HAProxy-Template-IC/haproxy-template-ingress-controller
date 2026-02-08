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

// Package timeouts provides shared timeout constants used across controller sub-packages.
package timeouts

import "time"

const (
	// KubernetesAPITimeout is the standard timeout for Kubernetes API calls
	// such as status updates and resource cleanup operations.
	KubernetesAPITimeout = 10 * time.Second

	// KubernetesAPILongTimeout is the timeout for longer Kubernetes API operations
	// such as publishing configurations or reconciling pod status.
	KubernetesAPILongTimeout = 30 * time.Second

	// HTTPServerTimeout is the read/write timeout for HTTP servers
	// such as the webhook admission server.
	HTTPServerTimeout = 10 * time.Second

	// InformerResyncPeriod is the resync interval for Kubernetes shared informers.
	InformerResyncPeriod = 30 * time.Second

	// TickerPollInterval is the interval for periodic polling operations
	// such as metrics collection and deployment timeout checks.
	TickerPollInterval = 5 * time.Second

	// GracefulStopDelay is the brief pause after stopping components
	// to allow in-flight operations to complete.
	GracefulStopDelay = 100 * time.Millisecond
)
