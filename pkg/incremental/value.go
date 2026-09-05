package incremental

import "errors"

type exactValueAuthority struct {
	seal *exactValueAuthority
}

type exactValueExecution struct {
	authority *exactValueAuthority
	key       QueryKey
	seal      *exactValueExecution
}

type exactValueStorage struct {
	value     string
	owner     *exactValue
	execution *exactValueExecution
}

type exactValue struct {
	storage   exactValueStorage
	authority *exactValueAuthority
	key       QueryKey
	execution *exactValueExecution
	length    int
	seal      *exactValue
}

// ExactValueRoot is an authenticated immutable query value handle.
type ExactValueRoot struct {
	value *exactValue
}

func newExactValueAuthority() *exactValueAuthority {
	authority := &exactValueAuthority{}
	authority.seal = authority
	return authority
}

func newExactValueExecution(authority *exactValueAuthority, key QueryKey) *exactValueExecution {
	execution := &exactValueExecution{authority: authority, key: key}
	execution.seal = execution
	return execution
}

func newExactValueRoot(
	authority *exactValueAuthority,
	key QueryKey,
	value string,
) ExactValueRoot {
	return newExactValueRootForExecution(authority, key, value, nil)
}

func newExactValueRootForExecution(
	authority *exactValueAuthority,
	key QueryKey,
	value string,
	execution *exactValueExecution,
) ExactValueRoot {
	exact := &exactValue{}
	return initializeExactValueRoot(exact, authority, key, value, execution)
}

func initializeExactValueRoot(
	exact *exactValue,
	authority *exactValueAuthority,
	key QueryKey,
	value string,
	execution *exactValueExecution,
) ExactValueRoot {
	*exact = exactValue{
		authority: authority,
		key:       key,
		execution: execution,
		length:    len(value),
	}
	exact.storage = exactValueStorage{value: value, owner: exact, execution: execution}
	exact.seal = exact
	return ExactValueRoot{value: exact}
}

// ValidateAuthentication verifies the root's private ownership chain in O(1).
func (r ExactValueRoot) ValidateAuthentication() error {
	if r.value == nil || r.value.seal != r.value || r.value.storage.owner != r.value ||
		r.value.storage.execution != r.value.execution ||
		r.value.authority == nil || r.value.authority.seal != r.value.authority ||
		!validQueryKey(r.value.key) || r.value.length != len(r.value.storage.value) {
		return errors.New("incremental exact value has invalid provenance")
	}
	if r.value.execution != nil && !r.value.execution.valid(r.value.authority, r.value.key) {
		return errors.New("incremental exact value has invalid provenance")
	}
	return nil
}

func (r ExactValueRoot) validateOwned(authority *exactValueAuthority, key QueryKey) error {
	if err := r.ValidateAuthentication(); err != nil {
		return err
	}
	if authority == nil || r.value.authority != authority || r.value.key != key {
		return errors.New("incremental exact value belongs to another query")
	}
	return nil
}

func (r ExactValueRoot) validateExecution(execution *exactValueExecution) error {
	if err := r.ValidateAuthentication(); err != nil {
		return err
	}
	if execution == nil || !execution.valid(r.value.authority, r.value.key) ||
		r.value.execution != execution {
		return errors.New("incremental exact value belongs to another query execution")
	}
	return nil
}

func (e *exactValueExecution) valid(authority *exactValueAuthority, key QueryKey) bool {
	return e != nil && e.seal == e && e.authority == authority && e.key == key
}

// String returns the immutable value without copying its payload.
func (r ExactValueRoot) String() (string, error) {
	if err := r.ValidateAuthentication(); err != nil {
		return "", err
	}
	return r.value.storage.value, nil
}

// Bytes returns a detached mutable copy of the immutable value.
func (r ExactValueRoot) Bytes() ([]byte, error) {
	value, err := r.String()
	if err != nil {
		return nil, err
	}
	if value == "" {
		return nil, nil
	}
	return []byte(value), nil
}

// SameRoot reports authenticated root identity without comparing payload bytes.
func (r ExactValueRoot) SameRoot(other ExactValueRoot) (bool, error) {
	if err := r.ValidateAuthentication(); err != nil {
		return false, err
	}
	if err := other.ValidateAuthentication(); err != nil {
		return false, err
	}
	return r.value == other.value, nil
}

// ExactEqual compares immutable values exactly without trusting a digest.
func (r ExactValueRoot) ExactEqual(other ExactValueRoot) (bool, error) {
	if err := r.ValidateAuthentication(); err != nil {
		return false, err
	}
	if err := other.ValidateAuthentication(); err != nil {
		return false, err
	}
	return sameExactValue(r, other), nil
}

func sameExactValue(left, right ExactValueRoot) bool {
	return left.value == right.value || left.value.storage.value == right.value.storage.value
}
