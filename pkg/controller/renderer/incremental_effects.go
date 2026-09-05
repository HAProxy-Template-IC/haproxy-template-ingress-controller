// Copyright 2026 Philipp Hossner
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

package renderer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

var (
	_ templating.IncrementalResourceDeriver = (*incrementalResourceDeriver)(nil)
	_ templating.IncrementalEventRecorder   = (*incrementalRecorder)(nil)
)

type incrementalResourceDeriver struct {
	mu       sync.Mutex
	lease    *incrementalBatchReaderLease
	identity rendercontext.DerivedResourceIdentity
	current  []byte
	view     *rendercontext.DerivedResourceView
}

type incrementalPreflightResourceDeriver struct {
	deriver *incrementalResourceDeriver
}

func newIncrementalResourceDeriver(
	source, namespace, name string,
	raw []byte,
) (*incrementalResourceDeriver, error) {
	identity := rendercontext.DerivedResourceIdentity{
		Resource:  source,
		Namespace: namespace,
		Name:      name,
	}
	if source == "" || name == "" {
		return nil, errors.New("incremental derived resource requires a source and name")
	}
	decoded, canonical, err := validateIncrementalDerivationSource(raw)
	if err != nil {
		return nil, err
	}
	actualNamespace, actualName, found := resourceIdentity(decoded)
	if !found {
		return nil, fmt.Errorf("incremental derived resource source %q has no metadata.name", source)
	}
	if actualNamespace != namespace || actualName != name {
		return nil, fmt.Errorf(
			"incremental derived resource source identity is %s/%s, expected %s/%s",
			actualNamespace,
			actualName,
			namespace,
			name,
		)
	}
	return &incrementalResourceDeriver{
		identity: identity,
		current:  canonical,
		view:     rendercontext.NewDerivedResourceView(),
	}, nil
}

func validateIncrementalDerivationSource(raw []byte) (decoded any, canonical []byte, err error) {
	if !json.Valid(raw) {
		return nil, nil, errors.New("incremental derived resource source is not valid JSON")
	}
	decoded, err = decodeResourceValue(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("decoding incremental derived resource source: %w", err)
	}
	canonical, err = encodeResourceValue(decoded)
	if err != nil {
		return nil, nil, err
	}
	if !bytes.Equal(raw, canonical) {
		return nil, nil, errors.New("incremental derived resource source is not canonical JSON")
	}
	return decoded, slices.Clone(canonical), nil
}

func (d *incrementalResourceDeriver) DeriveResource(resource string, item any, path string, value any) (any, error) {
	if d == nil {
		return nil, errors.New("incremental resource deriver is nil")
	}
	release, err := beginIncrementalCapability(d.lease, "deriveResource")
	if err != nil {
		return nil, err
	}
	defer release()
	return d.deriveResource(resource, item, path, value)
}

func (d *incrementalResourceDeriver) deriveResource(resource string, item any, path string, value any) (any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if resource != d.identity.Resource {
		return nil, fmt.Errorf(
			"incremental component is bound to source %q, not %q",
			d.identity.Resource,
			resource,
		)
	}
	if err := d.validateIdentity(item); err != nil {
		return nil, err
	}
	encoded, err := encodeResourceValue(item)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(encoded, d.current) {
		return nil, fmt.Errorf(
			"%w for %s %s/%s: transformation did not continue from the exact current value",
			rendercontext.ErrDerivedResourceStale,
			d.identity.Resource,
			d.identity.Namespace,
			d.identity.Name,
		)
	}
	derived, err := d.view.DeriveResource(resource, item, path, value)
	if err != nil {
		return nil, err
	}
	if err := d.validateIdentity(derived); err != nil {
		return nil, err
	}
	encoded, err = encodeResourceValue(derived)
	if err != nil {
		return nil, err
	}
	d.current = slices.Clone(encoded)
	return derived, nil
}

func (d *incrementalResourceDeriver) validateIdentity(value any) error {
	namespace, name, found := resourceIdentity(value)
	if !found {
		return fmt.Errorf("incremental source %q has an object without metadata.name", d.identity.Resource)
	}
	if namespace != d.identity.Namespace || name != d.identity.Name {
		return fmt.Errorf(
			"incremental component source identity is %s/%s, expected %s/%s",
			namespace,
			name,
			d.identity.Namespace,
			d.identity.Name,
		)
	}
	return nil
}

func (d *incrementalResourceDeriver) freeze() []rendercontext.DerivedResource {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.view.Freeze()
}

func (r *incrementalRecorder) RecordEvent(
	namespace, name, apiVersion, kind, eventType, reason, message string,
) error {
	if r == nil {
		return errors.New("incremental event recorder is nil")
	}
	release, err := beginIncrementalCapability(r.lease, "recordEvent")
	if err != nil {
		r.recordCapabilityViolation(err)
		return err
	}
	defer release()
	return r.recordEvent(namespace, name, apiVersion, kind, eventType, reason, message)
}

func (r *incrementalRecorder) recordEvent(
	namespace, name, apiVersion, kind, eventType, reason, message string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.events == nil {
		r.events = templating.NewEventCollector()
	}
	return r.events.Register(namespace, name, apiVersion, kind, eventType, reason, message)
}

func (d *incrementalPreflightResourceDeriver) DeriveResource(
	resource string,
	item any,
	path string,
	value any,
) (any, error) {
	return d.deriver.deriveResource(resource, item, path, value)
}

func (r *incrementalPreflightRecorder) RecordEvent(
	namespace, name, apiVersion, kind, eventType, reason, message string,
) error {
	return r.recorder.recordEvent(namespace, name, apiVersion, kind, eventType, reason, message)
}
