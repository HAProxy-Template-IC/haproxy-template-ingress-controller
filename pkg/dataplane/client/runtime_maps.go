package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	v30 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30"
	v30ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30ee"
	v31 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31"
	v31ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31ee"
	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
	v33 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v33"
)

// mapEntry is a version-neutral key/value map entry, parsed from a map file
// body or read back from the runtime.
type mapEntry struct {
	key   string
	value string
}

type mapOpKind int

const (
	opSet mapOpKind = iota // set map <key> <value> — atomic in-place replace
	opDel                  // del map <key>         — removes all values for the key
	opAdd                  // add map <key> <value>
)

// mapOp is one runtime map mutation in the computed delta.
type mapOp struct {
	kind  mapOpKind
	key   string
	value string
}

// ReplaceRuntimeMap makes the live (in-memory) contents of an existing runtime
// map equal the entries parsed from desiredContent, by applying the minimal
// per-entry delta (add/delete) against the current runtime state. It applies
// WITHOUT a reload and is available on all DataPlane API versions (v3.0+).
//
// It deliberately does NOT use a bulk "add payload" (which appends rather than
// replaces, leaving stale and duplicate entries) nor a clear+repopulate (which
// would briefly empty the whole map). A changed single-value key is updated
// in-place with `set map` (atomic, no gap); only multi-value keys and removals
// use del(+add). Unchanged keys are untouched, so the map is always valid.
//
// force_sync is intentionally NOT set: the orchestrator's pre-config phase
// already wrote the desired content to the on-disk map file (skip_reload), so
// disk durability (reload convergence) is handled there. This call only updates
// the live worker's memory, and must not push memory back to disk.
//
// name is the map's identifier as HAProxy reports it (the path used in the
// config); the map must already exist and be referenced by the running config.
func (c *DataplaneClient) ReplaceRuntimeMap(ctx context.Context, name, desiredContent string) error {
	current, err := c.showRuntimeMapEntries(ctx, name)
	if err != nil {
		return err
	}

	for _, op := range mapEntryDelta(current, parseMapEntries(desiredContent)) {
		switch op.kind {
		case opSet:
			err = c.setRuntimeMapEntry(ctx, name, op.key, op.value)
		case opDel:
			err = c.deleteRuntimeMapEntry(ctx, name, op.key)
		case opAdd:
			err = c.addRuntimeMapEntry(ctx, name, op.key, op.value)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// mapEntryDelta computes the minimal per-entry mutations to make the current
// runtime map equal desired. A key whose single value changed becomes one
// in-place `set map` (atomic — no window where the key is unmapped); a key
// whose value multiset changed in any other way, or that was removed, becomes
// `del map` then re-adds; a new key becomes `add map`. Unchanged keys produce
// nothing. It is a pure function (no I/O) so the delta logic is unit-tested.
func mapEntryDelta(current, desired []mapEntry) []mapOp {
	curByKey := make(map[string][]string, len(current))
	for _, e := range current {
		curByKey[e.key] = append(curByKey[e.key], e.value)
	}
	desByKey := make(map[string][]string, len(desired))
	for _, e := range desired {
		desByKey[e.key] = append(desByKey[e.key], e.value)
	}

	var ops []mapOp
	// Keys present in the current map: in-place set when both sides are a single
	// value (the common host/path/weight re-point), otherwise del + re-add.
	for key, curVals := range curByKey {
		desVals := desByKey[key]
		if sameMultiset(curVals, desVals) {
			continue
		}
		if len(curVals) == 1 && len(desVals) == 1 {
			ops = append(ops, mapOp{kind: opSet, key: key, value: desVals[0]})
			continue
		}
		ops = append(ops, mapOp{kind: opDel, key: key})
		for _, v := range desVals {
			ops = append(ops, mapOp{kind: opAdd, key: key, value: v})
		}
	}
	// Keys only in the desired map: pure additions.
	for _, e := range desired {
		if _, existed := curByKey[e.key]; existed {
			continue
		}
		ops = append(ops, mapOp{kind: opAdd, key: e.key, value: e.value})
	}
	return ops
}

// GetRuntimeMapEntries returns the live (in-memory) entries of a runtime map as
// a key→value map (last value wins on duplicate keys). Exposed for tests that
// need to assert the worker's in-memory map state, not just the on-disk file.
func (c *DataplaneClient) GetRuntimeMapEntries(ctx context.Context, name string) (map[string]string, error) {
	entries, err := c.showRuntimeMapEntries(ctx, name)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		out[e.key] = e.value
	}
	return out, nil
}

// sameMultiset reports whether a and b contain the same values (order-insensitive).
func sameMultiset(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// showRuntimeMapEntries fetches the live entries of a runtime map. A 404 (map
// not loaded yet) yields an empty slice rather than an error.
func (c *DataplaneClient) showRuntimeMapEntries(ctx context.Context, name string) ([]mapEntry, error) {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V33:   func(c *v33.Client) (*http.Response, error) { return c.ShowRuntimeMap(ctx, name) },
		V32:   func(c *v32.Client) (*http.Response, error) { return c.ShowRuntimeMap(ctx, name) },
		V31:   func(c *v31.Client) (*http.Response, error) { return c.ShowRuntimeMap(ctx, name) },
		V30:   func(c *v30.Client) (*http.Response, error) { return c.ShowRuntimeMap(ctx, name) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.ShowRuntimeMap(ctx, name) },
		V31EE: func(c *v31ee.Client) (*http.Response, error) { return c.ShowRuntimeMap(ctx, name) },
		V30EE: func(c *v30ee.Client) (*http.Response, error) { return c.ShowRuntimeMap(ctx, name) },
	})
	if err != nil {
		return nil, fmt.Errorf("reading runtime map '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("reading runtime map '%s' failed with status %d: %s", name, resp.StatusCode, string(body))
	}

	var raw []struct {
		Key   *string `json:"key"`
		Value *string `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding runtime map '%s' entries: %w", name, err)
	}
	entries := make([]mapEntry, 0, len(raw))
	for _, e := range raw {
		entries = append(entries, mapEntry{
			key:   derefStr(e.Key),
			value: derefStr(e.Value),
		})
	}
	return entries, nil
}

// addRuntimeMapEntry adds one key/value entry to the live map.
func (c *DataplaneClient) addRuntimeMapEntry(ctx context.Context, name, key, value string) error {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.AddMapEntry(ctx, name, &v33.AddMapEntryParams{}, v33.MapEntry{Key: &key, Value: &value})
		},
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.AddMapEntry(ctx, name, &v32.AddMapEntryParams{}, v32.MapEntry{Key: &key, Value: &value})
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.AddMapEntry(ctx, name, &v31.AddMapEntryParams{}, v31.MapEntry{Key: &key, Value: &value})
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return c.AddMapEntry(ctx, name, &v30.AddMapEntryParams{}, v30.MapEntry{Key: &key, Value: &value})
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.AddMapEntry(ctx, name, &v32ee.AddMapEntryParams{}, v32ee.MapEntry{Key: &key, Value: &value})
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.AddMapEntry(ctx, name, &v31ee.AddMapEntryParams{}, v31ee.MapEntry{Key: &key, Value: &value})
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.AddMapEntry(ctx, name, &v30ee.AddMapEntryParams{}, v30ee.MapEntry{Key: &key, Value: &value})
		},
	})
	if err != nil {
		return fmt.Errorf("adding runtime map '%s' entry '%s': %w", name, key, err)
	}
	defer resp.Body.Close()
	if _, err := checkCreateResponse(resp, "runtime map entry", key); err != nil {
		return err
	}
	return nil
}

// setRuntimeMapEntry replaces the value of an existing key in-place via
// `set map <name> <key> <value>`. Atomic: the key never loses its mapping
// (unlike del+add), so a host re-point or weight change can't transiently
// drop traffic. The {id} path segment carries the key, same as deletion.
func (c *DataplaneClient) setRuntimeMapEntry(ctx context.Context, name, key, value string) error {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.ReplaceRuntimeMapEntry(ctx, name, key, &v33.ReplaceRuntimeMapEntryParams{}, v33.ReplaceRuntimeMapEntryJSONRequestBody{Value: value})
		},
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.ReplaceRuntimeMapEntry(ctx, name, key, &v32.ReplaceRuntimeMapEntryParams{}, v32.ReplaceRuntimeMapEntryJSONRequestBody{Value: value})
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.ReplaceRuntimeMapEntry(ctx, name, key, &v31.ReplaceRuntimeMapEntryParams{}, v31.ReplaceRuntimeMapEntryJSONRequestBody{Value: value})
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return c.ReplaceRuntimeMapEntry(ctx, name, key, &v30.ReplaceRuntimeMapEntryParams{}, v30.ReplaceRuntimeMapEntryJSONRequestBody{Value: value})
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.ReplaceRuntimeMapEntry(ctx, name, key, &v32ee.ReplaceRuntimeMapEntryParams{}, v32ee.ReplaceRuntimeMapEntryJSONRequestBody{Value: value})
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.ReplaceRuntimeMapEntry(ctx, name, key, &v31ee.ReplaceRuntimeMapEntryParams{}, v31ee.ReplaceRuntimeMapEntryJSONRequestBody{Value: value})
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.ReplaceRuntimeMapEntry(ctx, name, key, &v30ee.ReplaceRuntimeMapEntryParams{}, v30ee.ReplaceRuntimeMapEntryJSONRequestBody{Value: value})
		},
	})
	if err != nil {
		return fmt.Errorf("setting runtime map '%s' entry '%s': %w", name, key, err)
	}
	defer resp.Body.Close()
	if _, err := checkUpdateResponse(resp, "runtime map entry", key); err != nil {
		return err
	}
	return nil
}

// deleteRuntimeMapEntry deletes entries from the live map by key (HAProxy's
// `del map <name> <key>` removes every entry matching the key). The DataPlane
// API's {id} path segment is passed the key, not a numeric/pointer id — the
// runtime accepts a key there and a pointer id is rejected as a missing key.
func (c *DataplaneClient) deleteRuntimeMapEntry(ctx context.Context, name, key string) error {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.DeleteRuntimeMapEntry(ctx, name, key, &v33.DeleteRuntimeMapEntryParams{})
		},
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.DeleteRuntimeMapEntry(ctx, name, key, &v32.DeleteRuntimeMapEntryParams{})
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.DeleteRuntimeMapEntry(ctx, name, key, &v31.DeleteRuntimeMapEntryParams{})
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return c.DeleteRuntimeMapEntry(ctx, name, key, &v30.DeleteRuntimeMapEntryParams{})
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.DeleteRuntimeMapEntry(ctx, name, key, &v32ee.DeleteRuntimeMapEntryParams{})
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.DeleteRuntimeMapEntry(ctx, name, key, &v31ee.DeleteRuntimeMapEntryParams{})
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.DeleteRuntimeMapEntry(ctx, name, key, &v30ee.DeleteRuntimeMapEntryParams{})
		},
	})
	if err != nil {
		return fmt.Errorf("deleting runtime map '%s' entries for key '%s': %w", name, key, err)
	}
	defer resp.Body.Close()
	return checkDeleteResponse(resp, "runtime map entry", key)
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// parseMapEntries parses an HAProxy map file body ("key value" per line) into
// version-neutral entries. Blank lines and comment lines (first non-space rune
// is '#') are skipped. The key is the first whitespace-delimited token; the
// value is the remainder of the line, trimmed. A line with only a key yields an
// empty value.
func parseMapEntries(content string) []mapEntry {
	lines := strings.Split(content, "\n")
	entries := make([]mapEntry, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key := trimmed
		value := ""
		if i := strings.IndexAny(trimmed, " \t"); i >= 0 {
			key = trimmed[:i]
			value = strings.TrimSpace(trimmed[i+1:])
		}
		entries = append(entries, mapEntry{key: key, value: value})
	}
	return entries
}
