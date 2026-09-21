package orderedset

import (
	"errors"
	"maps"
	"slices"
)

// Update applies membership assignments without copying a packed root per value.
func (r Root) Update(authority *Authority, scope Scope, membership map[string]bool) (Root, error) {
	if err := r.ValidateOwnership(authority, scope); err != nil {
		return Root{}, err
	}
	if _, exists := membership[""]; exists {
		return Root{}, errors.New("ordered set value is empty")
	}
	if len(membership) == 0 {
		return r, nil
	}
	keys := slices.Sorted(maps.Keys(membership))
	if len(r.state.packed) != 0 {
		return r.updatePacked(authority, scope, keys, membership), nil
	}
	for _, key := range keys {
		var err error
		if membership[key] {
			r, _, err = r.Add(authority, scope, key)
		} else {
			r, _, err = r.Delete(authority, scope, key)
		}
		if err != nil {
			return Root{}, err
		}
	}
	return r, nil
}

func (r Root) updatePacked(authority *Authority, scope Scope, keys []string, membership map[string]bool) Root {
	size := len(r.state.packed)
	changed := false
	for _, key := range keys {
		_, present := slices.BinarySearch(r.state.packed, key)
		if present == membership[key] {
			continue
		}
		changed = true
		if present {
			size--
		} else {
			size++
		}
	}
	if !changed {
		return r
	}
	if size == 0 {
		return authority.Empty()
	}
	values := make([]string, 0, size)
	previous := r.state.packed
	for _, key := range keys {
		index, present := slices.BinarySearch(previous, key)
		values = append(values, previous[:index]...)
		if membership[key] {
			values = append(values, key)
		}
		if present {
			index++
		}
		previous = previous[index:]
	}
	values = append(values, previous...)
	return Root{state: newPackedRootState(authority, scope, values)}
}
