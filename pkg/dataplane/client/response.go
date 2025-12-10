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
)

// CheckResponse validates an HTTP response status code and logs failures with full context.
// It reads and logs the response body for debugging, then returns a user-friendly error.
//
// Usage:
//
//	resp, err := c.Dispatch(ctx, callFunc)
//	if err != nil {
//	    return fmt.Errorf("failed to create backend: %w", err)
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
	} else {
		slog.Error("Dataplane API request failed",
			"operation", operation,
			"status_code", resp.StatusCode,
			"response_body", string(body),
		)
	}

	return fmt.Errorf("%s failed with status %d", operation, resp.StatusCode)
}
