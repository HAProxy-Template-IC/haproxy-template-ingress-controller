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
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"
)

// CheckResponse validates an HTTP response status code and logs failures with full context.
// It reads and logs the response body for debugging, then returns a user-friendly error.
//
// Usage:
//
//	resp, err := c.Dispatch(ctx, callFunc)
//	if err != nil {
//	    return fmt.Errorf("creating backend: %w", err)
//	}
//	defer resp.Body.Close()
//
//	if err := client.CheckResponse(resp, "create backend"); err != nil {
//	    return err
//	}
func CheckResponse(resp *http.Response, operation string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	// Read response body for detailed logging
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		slog.Error("Dataplane API request failed",
			"operation", operation,
			"status_code", resp.StatusCode,
			"body_read_error", readErr.Error(),
		)
		return fmt.Errorf("%s failed with status %d", operation, resp.StatusCode)
	}

	slog.Error("Dataplane API request failed",
		"operation", operation,
		"status_code", resp.StatusCode,
		"response_body", string(body),
	)

	// Include the (truncated) body in the returned error, not only the log:
	// callers and retry conditions (e.g. IsReloadInProgress) classify failures
	// from the error string. A 500 whose body says the HAProxy master socket is
	// "connection refused" during a reload is retryable; without the body the
	// caller only sees "failed with status 500" and can't tell.
	return fmt.Errorf("%s failed with status %d: %s", operation, resp.StatusCode, truncateForError(string(body)))
}

// truncateForError caps a response body embedded in an error message so a large
// validation dump doesn't bloat logs or webhook responses.
func truncateForError(s string) string {
	const maxLen = 512
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	// Back off to a valid UTF-8 boundary so a multi-byte rune split at the cut
	// doesn't leave invalid UTF-8 in the error (slog and other consumers can
	// mangle it). At most ~3 bytes are trimmed.
	trunc := s[:maxLen]
	for trunc != "" && !utf8.ValidString(trunc) {
		trunc = trunc[:len(trunc)-1]
	}
	return trunc + "…(truncated)"
}
