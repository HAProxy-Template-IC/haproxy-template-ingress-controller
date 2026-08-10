// Package storetest provides shared mock implementations of stores.Store for testing.
package storetest

import "gitlab.com/haproxy-haptic/haptic/pkg/stores"

// MockStore is a configurable mock implementation of stores.Store for testing.
// Configure behavior by setting fields before use:
//
//	store := &storetest.MockStore{Items: []any{item1, item2}}
//	store := &storetest.MockStore{ListErr: errors.New("fail")}
//	store := &storetest.MockStore{} // no-op store
type MockStore struct {
	Items   []any
	ListErr error
	GetErr  error
}

func (m *MockStore) List() ([]any, error) {
	if m.ListErr != nil {
		return nil, m.ListErr
	}
	return m.Items, nil
}

func (m *MockStore) Get(_ ...string) ([]any, error) {
	if m.GetErr != nil {
		return nil, m.GetErr
	}
	return m.Items, nil
}

func (m *MockStore) Add(resource any, _ []string) error {
	m.Items = append(m.Items, resource)
	return nil
}

func (m *MockStore) Update(_ any, _ []string) error {
	return nil
}

func (m *MockStore) Delete(_, _ string, _ []string) error {
	return nil
}

func (m *MockStore) Clear() error {
	m.Items = nil
	return nil
}

// Verify interface compliance at compile time.
var _ stores.Store = (*MockStore)(nil)
