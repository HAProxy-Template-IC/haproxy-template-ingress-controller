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

import "time"

// CertResourceChangedEvent is published when the webhook certificate Secret changes.
//
// This event is published by the resource watcher when the Secret resource
// is created, updated, or modified.
type CertResourceChangedEvent struct {
	Resource interface{} // *unstructured.Unstructured

	timestamp time.Time
}

// NewCertResourceChangedEvent creates a new CertResourceChangedEvent.
func NewCertResourceChangedEvent(resource interface{}) *CertResourceChangedEvent {
	return &CertResourceChangedEvent{
		Resource:  resource,
		timestamp: time.Now(),
	}
}

func (e *CertResourceChangedEvent) EventType() string    { return EventTypeCertResourceChanged }
func (e *CertResourceChangedEvent) Timestamp() time.Time { return e.timestamp }

// CertParsedEvent is published when webhook certificates are successfully extracted and parsed.
//
// The controller will use these certificates to initialize the webhook server.
type CertParsedEvent struct {
	CertPEM []byte
	KeyPEM  []byte
	Version string // Secret resourceVersion

	timestamp time.Time
}

// NewCertParsedEvent creates a new CertParsedEvent.
func NewCertParsedEvent(certPEM, keyPEM []byte, version string) *CertParsedEvent {
	// Defensive copy of byte slices
	certCopy := make([]byte, len(certPEM))
	copy(certCopy, certPEM)

	keyCopy := make([]byte, len(keyPEM))
	copy(keyCopy, keyPEM)

	return &CertParsedEvent{
		CertPEM:   certCopy,
		KeyPEM:    keyCopy,
		Version:   version,
		timestamp: time.Now(),
	}
}

func (e *CertParsedEvent) EventType() string    { return EventTypeCertParsed }
func (e *CertParsedEvent) Timestamp() time.Time { return e.timestamp }
