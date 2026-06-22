package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"strings"
)

// ReloadIDHeader is the HTTP header name used by HAProxy Data Plane API
// to return the reload ID when an operation triggers a reload.
const ReloadIDHeader = "Reload-Id"

// multipartField represents an additional form field to include in a multipart payload.
type multipartField struct {
	name  string
	value string
}

// buildMultipartFilePayload creates multipart form-data for file upload.
// Additional form fields can be included via the fields parameter.
// Returns the body buffer and content-type header.
func buildMultipartFilePayload(filename, content string, fields ...multipartField) (*bytes.Buffer, string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add file content as a form file field
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file_upload"; filename=%q`, filename))
	h.Set("Content-Type", "application/octet-stream")

	part, err := writer.CreatePart(h)
	if err != nil {
		return nil, "", fmt.Errorf("creating multipart part: %w", err)
	}

	if _, err := part.Write([]byte(content)); err != nil {
		return nil, "", fmt.Errorf("writing file content: %w", err)
	}

	// Add additional form fields
	for _, f := range fields {
		if err := writer.WriteField(f.name, f.value); err != nil {
			return nil, "", fmt.Errorf("writing %s field: %w", f.name, err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("closing multipart writer: %w", err)
	}

	return body, writer.FormDataContentType(), nil
}

// checkCreateResponse validates a Create operation response and extracts the reload ID.
// Returns the reload ID (empty string if no reload triggered) and any error.
// Handles: 409 Conflict, expects 201/200/202.
func checkCreateResponse(resp *http.Response, resourceType, name string) (string, error) {
	if resp.StatusCode == http.StatusConflict {
		return "", fmt.Errorf("%s '%s' already exists", resourceType, name)
	}

	// Accept 201 (Created), 200 (OK), and 202 (Accepted) as success
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		return "", nil // No reload triggered
	case http.StatusAccepted:
		return resp.Header.Get(ReloadIDHeader), nil // Reload triggered
	default:
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create %s '%s' failed with status %d: %s", resourceType, name, resp.StatusCode, string(bodyBytes))
	}
}

// checkUpdateResponse validates an Update operation response and extracts the reload ID.
// Returns the reload ID (empty string if no reload triggered) and any error.
// Handles: 404 NotFound, expects 200/202/204.
//
// 204 No Content is returned by the dataplane API when the caller requested
// skip_reload=true: there is no reload-id to communicate back, so the API
// drops the body entirely. All Update* aux-file callers now send
// skip_reload=true (see UpdateGeneralFile etc.), so 204 is the common case.
func checkUpdateResponse(resp *http.Response, resourceType, name string) (string, error) {
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("%s '%s' not found", resourceType, name)
	}

	// Accept 200 (OK), 202 (Accepted, reload triggered), and 204 (No Content,
	// returned when skip_reload=true is set) as success.
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		return "", nil // No reload triggered
	case http.StatusAccepted:
		return resp.Header.Get(ReloadIDHeader), nil // Reload triggered
	default:
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("update %s '%s' failed with status %d: %s", resourceType, name, resp.StatusCode, string(bodyBytes))
	}
}

// checkDeleteResponse validates a Delete operation response.
// Handles: 404 NotFound, expects 200/202/204.
func checkDeleteResponse(resp *http.Response, resourceType, name string) error {
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s '%s' not found", resourceType, name)
	}

	// Accept 200 (OK), 202 (Accepted), and 204 (No Content) as success
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("delete %s '%s' failed with status %d", resourceType, name, resp.StatusCode)
	}

	return nil
}

// readRawStorageContent reads response body as string for GetContent operations.
// Handles: 404 NotFound, expects 200.
func readRawStorageContent(resp *http.Response, resourceType, name string) (string, error) {
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("%s '%s' not found", resourceType, name)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("get %s '%s' failed with status %d", resourceType, name, resp.StatusCode)
	}

	var sb strings.Builder
	if resp.ContentLength > 0 {
		sb.Grow(int(resp.ContentLength))
	}
	if _, err := io.Copy(&sb, resp.Body); err != nil {
		return "", fmt.Errorf("reading response body for %s '%s': %w", resourceType, name, err)
	}

	return sb.String(), nil
}

// storageItem represents a single item in a storage listing response.
// The API returns different fields depending on the storage type, but all
// include storage_name as the identifier.
type storageItem struct {
	StorageName *string `json:"storage_name"`
}

// decodeStorageNameList decodes a JSON response body containing an array of storage items
// and extracts the storage_name values into a string slice. This is the shared response
// parsing pattern used by all GetAll* storage methods.
func decodeStorageNameList(resp *http.Response, resourceType string) ([]string, error) {
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get all %s failed with status %d", resourceType, resp.StatusCode)
	}

	var items []storageItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decoding %s response: %w", resourceType, err)
	}

	names := make([]string, 0, len(items))
	for _, item := range items {
		if item.StorageName != nil {
			names = append(names, *item.StorageName)
		}
	}

	return names, nil
}

// storageItemWithID extends storageItem with an ID fallback field, used by
// general files where the API may populate either storage_name or id.
type storageItemWithID struct {
	StorageName *string `json:"storage_name"`
	ID          *string `json:"id"`
}

// decodeStorageNameListWithFallback is like decodeStorageNameList but falls back to the
// "id" field when "storage_name" is not present. This is needed for general files where
// the API may populate either field.
func decodeStorageNameListWithFallback(resp *http.Response, resourceType string) ([]string, error) {
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get all %s failed with status %d", resourceType, resp.StatusCode)
	}

	var items []storageItemWithID
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decoding %s response: %w", resourceType, err)
	}

	names := make([]string, 0, len(items))
	for _, item := range items {
		if item.StorageName != nil {
			names = append(names, *item.StorageName)
		} else if item.ID != nil {
			names = append(names, *item.ID)
		}
	}

	return names, nil
}

// SanitizeStorageName sanitizes a filename for HAProxy storage.
// The API replaces dots in the filename (excluding the extension) with underscores.
// Example: "example.com.pem" becomes "example_com.pem".
func SanitizeStorageName(name string) string {
	ext := filepath.Ext(name)
	if ext == "" {
		// No extension, replace all dots
		return strings.ReplaceAll(name, ".", "_")
	}

	// Get the base name without extension
	base := strings.TrimSuffix(name, ext)

	// Replace dots in the base name with underscores
	sanitizedBase := strings.ReplaceAll(base, ".", "_")

	return sanitizedBase + ext
}

// UnsanitizeStorageName reverses sanitization (best-effort).
// Converts underscores back to dots in the basename.
// Example: "example_com.pem" becomes "example.com.pem".
// Note: This may not be perfect for filenames that originally contained underscores.
func UnsanitizeStorageName(name string) string {
	ext := filepath.Ext(name)
	if ext == "" {
		// No extension, can't reliably unsanitize
		return name
	}

	// Get the base name without extension
	base := strings.TrimSuffix(name, ext)

	// Replace underscores with dots in the base name
	unsanitizedBase := strings.ReplaceAll(base, "_", ".")

	return unsanitizedBase + ext
}
