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

package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// FrontendConnections sums the cumulative accepted-connection counter of every
// frontend except the ignored ones. It is the drain's signal: the sum stops
// moving once nothing routes new connections to the worker any more.
func (c *Client) FrontendConnections(ignore map[string]bool) (uint64, error) {
	raw, err := c.worker.ExecuteRaw("show stat -1 1 -1")
	if err != nil {
		return 0, fmt.Errorf("show stat: %w", err)
	}
	return parseFrontendConnections(raw, ignore)
}

// parseFrontendConnections reads the CSV of `show stat` (type 1 keeps the
// frontend rows) and sums conn_tot, located by the header rather than by
// position so a column added by a newer HAProxy cannot shift it.
func parseFrontendConnections(raw string, ignore map[string]bool) (uint64, error) {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "# ") {
		return 0, errors.New("show stat: missing CSV header")
	}
	header := strings.Split(strings.TrimPrefix(lines[0], "# "), ",")
	column := -1
	for i, name := range header {
		if name == "conn_tot" {
			column = i
			break
		}
	}
	if column < 0 {
		return 0, errors.New("show stat: no conn_tot column")
	}
	var total uint64
	for _, line := range lines[1:] {
		fields := strings.Split(line, ",")
		if len(fields) <= column || fields[1] != "FRONTEND" || ignore[fields[0]] {
			continue
		}
		if fields[column] == "" {
			continue
		}
		value, err := strconv.ParseUint(fields[column], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("show stat: frontend %s conn_tot %q: %w", fields[0], fields[column], err)
		}
		total += value
	}
	return total, nil
}
