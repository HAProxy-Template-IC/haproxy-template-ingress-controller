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

package events

// CertResourceChangedEvent is published when the webhook certificate Secret changes.
//
// This event is published by the resource watcher when the Secret resource
// is created, updated, or modified.
type CertResourceChangedEvent struct {
	Resource any // *unstructured.Unstructured

	timestamped
}

// NewCertResourceChangedEvent creates a new CertResourceChangedEvent.
func NewCertResourceChangedEvent(resource any) *CertResourceChangedEvent {
	return &CertResourceChangedEvent{
		Resource:    resource,
		timestamped: newTimestamped(),
	}
}

func (e *CertResourceChangedEvent) EventType() string { return EventTypeCertResourceChanged }

// CertParsedEvent is published when webhook certificates are successfully extracted and parsed.
//
// The controller will use these certificates to initialize the webhook server.
type CertParsedEvent struct {
	CertPEM []byte
	KeyPEM  []byte
	Version string // Secret resourceVersion

	timestamped
}

// NewCertParsedEvent creates a new CertParsedEvent.
func NewCertParsedEvent(certPEM, keyPEM []byte, version string) *CertParsedEvent {
	return &CertParsedEvent{
		CertPEM:     copySlice(certPEM),
		KeyPEM:      copySlice(keyPEM),
		Version:     version,
		timestamped: newTimestamped(),
	}
}

func (e *CertParsedEvent) EventType() string { return EventTypeCertParsed }
