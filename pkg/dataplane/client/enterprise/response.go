package enterprise

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// decodeResponse decodes a JSON response body into the target type.
// Returns error on non-2xx status or decode failure.
// The caller must close resp.Body (typically via defer resp.Body.Close()).
func decodeResponse[T any](resp *http.Response, operation string) (*T, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: unexpected status %d", operation, resp.StatusCode)
	}

	var result T
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("%s: decoding response: %w", operation, err)
	}

	return &result, nil
}

// decodeResponseOr404 is like decodeResponse but returns ErrNotFound for 404 responses.
// The caller must close resp.Body (typically via defer resp.Body.Close()).
func decodeResponseOr404[T any](resp *http.Response, operation string) (*T, error) {
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}

	return decodeResponse[T](resp, operation)
}

// decodeSliceResponse decodes a JSON array response body into a slice.
// Returns error on non-2xx status or decode failure.
// The caller must close resp.Body (typically via defer resp.Body.Close()).
func decodeSliceResponse[T any](resp *http.Response, operation string) ([]T, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: unexpected status %d", operation, resp.StatusCode)
	}

	var result []T
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("%s: decoding response: %w", operation, err)
	}

	return result, nil
}

// checkResponseStatus checks the HTTP response status code.
// Returns error on non-2xx status.
// The caller must close resp.Body (typically via defer resp.Body.Close()).
func checkResponseStatus(resp *http.Response, operation string) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: unexpected status %d", operation, resp.StatusCode)
	}

	return nil
}
