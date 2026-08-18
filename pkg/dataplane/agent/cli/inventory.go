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

package cli

import (
	"errors"
	"fmt"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

// ErrTooManyEntries means a listing is above the agent's limit, which is a
// refusal to read it, not a statement about what the worker holds.
var ErrTooManyEntries = errors.New("runtime listing exceeds the entry limit")

// Inventory reads what the running worker has loaded. The controller needs it
// to know whether a certificate or map it wants to touch at runtime exists at
// all; a path that is not in here is structural.
func (c *Client) Inventory(generation uint64) (api.Inventory, error) {
	maps, mapsErr := c.list("show map", parenthesised)
	certs, certsErr := c.list("show ssl cert", storeName)
	cas, casErr := c.list("show ssl ca-file", storeName)
	crls, crlsErr := c.list("show ssl crl-file", storeName)
	lists, listsErr := c.list("show ssl crt-list", storeName)
	if err := errors.Join(mapsErr, certsErr, casErr, crlsErr, listsErr); err != nil {
		return api.Inventory{}, err
	}
	return api.Inventory{
		Generation: generation,
		Maps:       maps,
		Certs:      certs,
		CAFiles:    cas,
		CRLFiles:   crls,
		CRTLists:   lists,
	}, nil
}

// MapEntries reads back one map file's runtime contents, keyed by map key.
// Duplicate keys keep every value, in insertion order, because that is what
// decides which one a lookup wins.
func (c *Client) MapEntries(path string) (map[string][]string, error) {
	if err := validateToken("map path", path); err != nil {
		return nil, err
	}
	raw, err := c.Raw("show map " + path)
	if err != nil {
		return nil, fmt.Errorf("show map %s: %w", path, err)
	}
	entries := map[string][]string{}
	for i, line := range dataLines(raw) {
		if i >= api.MaxMapEntries {
			return nil, fmt.Errorf("%w: show map %s returned more than %d entries",
				ErrTooManyEntries, path, api.MaxMapEntries)
		}
		// `show map <name>` prints `<entry address> <key> <value>` per line;
		// the value is the rest of the line (verified on 3.0 and 3.4).
		_, rest, found := strings.Cut(line, " ")
		if !found {
			continue
		}
		key, value, _ := strings.Cut(rest, " ")
		entries[key] = append(entries[key], value)
	}
	return entries, nil
}

// ServerNames reads the servers the running worker holds for a backend.
func (c *Client) ServerNames(backend string) (map[string]struct{}, error) {
	if err := validateToken("backend", backend); err != nil {
		return nil, err
	}
	raw, err := c.Raw("show servers state " + backend)
	if err != nil {
		return nil, fmt.Errorf("show servers state %s: %w", backend, err)
	}
	names := map[string]struct{}{}
	for i, line := range dataLines(raw) {
		if i >= api.MaxInventoryEntries {
			return nil, fmt.Errorf("%w: show servers state %s returned more than %d rows",
				ErrTooManyEntries, backend, api.MaxInventoryEntries)
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		names[fields[3]] = struct{}{}
	}
	return names, nil
}

func (c *Client) list(command string, extract func(string) string) ([]string, error) {
	raw, err := c.Raw(command)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", command, err)
	}
	var out []string
	for i, line := range dataLines(raw) {
		if i >= api.MaxInventoryEntries {
			return nil, fmt.Errorf("%w: %s returned more than %d entries",
				ErrTooManyEntries, command, api.MaxInventoryEntries)
		}
		if value := extract(line); value != "" {
			out = append(out, value)
		}
	}
	return out, nil
}

// dataLines drops the comment header and the blank lines every `show` answer
// carries.
func dataLines(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// parenthesised pulls the file path out of a `show map` row.
func parenthesised(line string) string {
	_, rest, found := strings.Cut(line, "(")
	if !found {
		return ""
	}
	path, _, found := strings.Cut(rest, ")")
	if !found {
		return ""
	}
	return path
}

// systemCA is HAProxy's built-in CA store. It is not a file, so no op can name
// it and it never belongs in the inventory.
const systemCA = "@system-ca"

// storeName reads the runtime name out of a `show ssl …` row: the name is the
// first field, an open transaction prefixes it with '*', and the CA listing
// appends " - N certificate(s)".
func storeName(line string) string {
	name, _, _ := strings.Cut(line, " ")
	name = strings.TrimPrefix(name, "*")
	if name == systemCA {
		return ""
	}
	return name
}
