package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
)

// CRTListEntry represents an entry within a CRT-list file.
// Each entry references a certificate file with optional SNI filters and SSL bind options.
type CRTListEntry struct {
	// File is the path to the certificate file.
	File string `json:"file,omitempty"`

	// LineNumber is the line number in the crt-list file (read-only, set by HAProxy).
	LineNumber int `json:"line_number,omitempty"`

	// SNIFilter is a list of SNI patterns for this certificate.
	SNIFilter []string `json:"sni_filter,omitempty"`

	// SSLBindConfig contains SSL bind configuration options for this certificate.
	SSLBindConfig string `json:"ssl_bind_config,omitempty"`
}

// GetCRTListEntries retrieves all entries from a specific CRT-list file.
// CRT-list entries are only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) GetCRTListEntries(ctx context.Context, crtListName string) ([]CRTListEntry, error) {
	// Sanitize the name for the API
	sanitizedName := sanitizeCRTListName(crtListName)

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.GetOneStorageSSLCrtListFile(ctx, sanitizedName)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetOneStorageSSLCrtListFile(ctx, sanitizedName)
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsCrtList {
			return fmt.Errorf("crt-list entries require DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get crt-list entries for '%s': %w", crtListName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get crt-list entries failed with status %d", resp.StatusCode)
	}

	// The response contains entries in the crt-list format
	// Parse as array of entry objects
	var apiEntries []struct {
		File          *string   `json:"file"`
		LineNumber    *int      `json:"line_number"`
		SNIFilter     *[]string `json:"sni_filter"`
		SSLBindConfig *string   `json:"ssl_bind_config"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiEntries); err != nil {
		return nil, fmt.Errorf("failed to decode crt-list entries response: %w", err)
	}

	entries := make([]CRTListEntry, 0, len(apiEntries))
	for _, apiEntry := range apiEntries {
		entry := CRTListEntry{}
		if apiEntry.File != nil {
			entry.File = *apiEntry.File
		}
		if apiEntry.LineNumber != nil {
			entry.LineNumber = *apiEntry.LineNumber
		}
		if apiEntry.SNIFilter != nil {
			entry.SNIFilter = *apiEntry.SNIFilter
		}
		if apiEntry.SSLBindConfig != nil {
			entry.SSLBindConfig = *apiEntry.SSLBindConfig
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// AddCRTListEntry adds a new entry to a CRT-list file.
// CRT-list entries are only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) AddCRTListEntry(ctx context.Context, crtListName string, entry *CRTListEntry) error {
	if !c.clientset.Capabilities().SupportsCrtList {
		return fmt.Errorf("crt-list entries are not supported by DataPlane API version %s (requires v3.2+)", c.clientset.DetectedVersion())
	}

	// Sanitize the name for the API
	sanitizedName := sanitizeCRTListName(crtListName)

	jsonData, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal crt-list entry: %w", err)
	}

	body := bytes.NewReader(jsonData)

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			params := &v32.CreateStorageSSLCrtListEntryParams{}
			return c.CreateStorageSSLCrtListEntryWithBody(ctx, sanitizedName, params, "application/json", body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.CreateStorageSSLCrtListEntryParams{}
			return c.CreateStorageSSLCrtListEntryWithBody(ctx, sanitizedName, params, "application/json", body)
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsCrtList {
			return fmt.Errorf("crt-list entries require DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to add crt-list entry to '%s': %w", crtListName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("add crt-list entry failed with status %d", resp.StatusCode)
	}

	return nil
}

// DeleteCRTListEntry deletes an entry from a CRT-list file by certificate name and line number.
// CRT-list entries are only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) DeleteCRTListEntry(ctx context.Context, crtListName, certificate string, lineNumber int) error {
	// Sanitize the name for the API
	sanitizedName := sanitizeCRTListName(crtListName)

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			params := &v32.DeleteStorageSSLCrtListEntryParams{
				Certificate: certificate,
				LineNumber:  lineNumber,
			}
			return c.DeleteStorageSSLCrtListEntry(ctx, sanitizedName, params)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteStorageSSLCrtListEntryParams{
				Certificate: certificate,
				LineNumber:  lineNumber,
			}
			return c.DeleteStorageSSLCrtListEntry(ctx, sanitizedName, params)
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsCrtList {
			return fmt.Errorf("crt-list entries require DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to delete crt-list entry from '%s' (cert: %s, line: %d): %w", crtListName, certificate, lineNumber, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Already deleted, not an error
		return nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete crt-list entry failed with status %d", resp.StatusCode)
	}

	return nil
}
