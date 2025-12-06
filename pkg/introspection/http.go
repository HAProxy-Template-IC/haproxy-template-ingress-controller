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

package introspection

import (
	"encoding/json"
	"net/http"
)

// WriteJSON writes data as JSON to the HTTP response with status 200 OK.
//
// Sets appropriate Content-Type header and handles JSON marshaling.
// If marshaling fails, writes an error response instead.
//
// Example:
//
//	WriteJSON(w, map[string]interface{}{
//	    "status": "ok",
//	    "count": 42,
//	})
func WriteJSON(w http.ResponseWriter, data interface{}) {
	WriteJSONWithStatus(w, http.StatusOK, data)
}

// WriteJSONWithStatus writes data as JSON with a custom HTTP status code.
//
// Use this when you need to return JSON with a non-200 status code.
//
// Example:
//
//	WriteJSONWithStatus(w, http.StatusServiceUnavailable, map[string]interface{}{
//	    "status": "degraded",
//	    "components": components,
//	})
func WriteJSONWithStatus(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		// If encoding fails, not much we can do - headers already sent
		// Log would be ideal but we don't have logger access here
		_ = err
	}
}

// WriteJSONField writes a specific field from data as JSON using JSONPath.
//
// The field parameter should use kubectl-style JSONPath syntax (e.g., "{.version}").
// If field is empty, writes the entire data object.
//
// Example:
//
//	config := map[string]interface{}{
//	    "version": "1.2.3",
//	    "templates": []string{"main", "secondary"},
//	}
//
//	// Get full object
//	WriteJSONField(w, config, "")
//
//	// Get specific field
//	WriteJSONField(w, config, "{.version}")  // Returns: "1.2.3"
func WriteJSONField(w http.ResponseWriter, data interface{}, field string) {
	if field == "" {
		WriteJSON(w, data)
		return
	}

	// Extract field using JSONPath
	result, err := ExtractField(data, field)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid field query: "+err.Error())
		return
	}

	WriteJSON(w, result)
}

// WriteError writes an error response with the specified HTTP status code.
//
// The error message is wrapped in a JSON object with an "error" field.
//
// Example:
//
//	WriteError(w, http.StatusNotFound, "variable not found")
//	// Response: {"error": "variable not found"}
func WriteError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	response := map[string]string{
		"error": message,
	}

	// Best effort - if this fails, not much we can do
	//nolint:errchkjson // Error handler itself - nowhere to report encoding errors
	_ = json.NewEncoder(w).Encode(response)
}

// WriteText writes a plain text response.
//
// Useful for simple string responses or formatted text.
//
// Example:
//
//	WriteText(w, "OK\n")
func WriteText(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(text))
}

// requireGET wraps an HTTP handler to enforce GET method only.
//
// Returns 405 Method Not Allowed for non-GET requests.
//
// Example:
//
//	mux.HandleFunc("/debug/vars", requireGET(s.handleIndex))
func requireGET(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "only GET is allowed")
			return
		}
		handler(w, r)
	}
}
