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

package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"syscall"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

// maxErrorBodyBytes bounds what an error carries into a log line or an Event.
const maxErrorBodyBytes = 4 << 10

// ConflictError is a 409 whose body is the agent's actual baseline: the ops
// were composed against a state the agent no longer has, and it wrote nothing.
type ConflictError struct {
	Conflict api.Conflict
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("agent rejected the apply (%s): its applied plan is %q at epoch %d/seq %d",
		e.Conflict.Reason, e.Conflict.AppliedPlanID,
		e.Conflict.AppliedToken.LeaderEpoch, e.Conflict.AppliedToken.RenderSeq)
}

// MissingError is a 409 listing the file parts the agent does not hold:
// resend the apply with those contents.
type MissingError struct {
	Missing []string
}

func (e *MissingError) Error() string {
	return fmt.Sprintf("agent is missing %d file part(s): %s",
		len(e.Missing), strings.Join(e.Missing, ", "))
}

// HTTPError is any other non-200 answer.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("agent returned HTTP %d", e.Status)
	}
	return fmt.Sprintf("agent returned HTTP %d: %s", e.Status, e.Body)
}

// statusError classifies a non-200 answer. A 409 carries either a baseline
// conflict or a missing-parts list; the key that is present decides.
func statusError(status int, body []byte) error {
	if status == http.StatusConflict {
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(body, &keys); err == nil {
			if _, ok := keys["missing"]; ok {
				var missing api.Missing
				if err := json.Unmarshal(body, &missing); err == nil {
					return &MissingError{Missing: missing.Missing}
				}
			} else {
				var conflict api.Conflict
				if err := json.Unmarshal(body, &conflict); err == nil {
					return &ConflictError{Conflict: conflict}
				}
			}
		}
	}
	return &HTTPError{Status: status, Body: truncate(string(body), maxErrorBodyBytes)}
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

// isConnectError reports the two failures the master's re-exec window
// produces. Everything else — a timeout, a half-written body, an HTTP status —
// is reported to the caller unretried.
func isConnectError(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET)
}
