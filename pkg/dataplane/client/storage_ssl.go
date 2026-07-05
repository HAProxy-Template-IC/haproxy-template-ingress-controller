package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	v30 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30"
	v30ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30ee"
	v31 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31"
	v31ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31ee"
	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
	v33 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v33"
)

// SanitizeSSLCertName sanitizes a certificate name for HAProxy Data Plane API storage.
// The API replaces dots in the filename (excluding the extension) with underscores.
// For example: "example.com.pem" becomes "example_com.pem".
// This function is exported for use in tests to compare certificate names.
func SanitizeSSLCertName(name string) string {
	return SanitizeStorageName(name)
}

// GetAllSSLCertificates retrieves all SSL certificate names from the storage.
// Note: This returns only certificate names, not the certificate contents.
// Use GetSSLCertificateContent to retrieve the actual certificate contents.
// The returned names are unsanitized (dots restored) for user convenience.
// Works with all HAProxy DataPlane API versions (v3.0+).
func (c *DataplaneClient) GetAllSSLCertificates(ctx context.Context) ([]string, error) {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V33:   func(c *v33.Client) (*http.Response, error) { return c.GetAllStorageSSLCertificates(ctx) },
		V32:   func(c *v32.Client) (*http.Response, error) { return c.GetAllStorageSSLCertificates(ctx) },
		V31:   func(c *v31.Client) (*http.Response, error) { return c.GetAllStorageSSLCertificates(ctx) },
		V30:   func(c *v30.Client) (*http.Response, error) { return c.GetAllStorageSSLCertificates(ctx) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.GetAllStorageSSLCertificates(ctx) },
		V31EE: func(c *v31ee.Client) (*http.Response, error) { return c.GetAllStorageSSLCertificates(ctx) },
		V30EE: func(c *v30ee.Client) (*http.Response, error) { return c.GetAllStorageSSLCertificates(ctx) },
	})

	if err != nil {
		return nil, fmt.Errorf("getting all SSL certificates: %w", err)
	}
	defer resp.Body.Close()

	return decodeStorageNameList(resp, "SSL certificates")
}

// GetSSLCertificateContent retrieves the SHA256 fingerprint for a specific SSL certificate by name.
//
// This function returns the sha256_finger_print field from the HAProxy Data Plane API,
// which serves as a unique identifier for the certificate content. This allows content-based
// comparison without needing to download the actual PEM data.
//
// The API provides rich metadata including:
//   - sha256_finger_print: SHA-256 hash of certificate content (returned by this function)
//   - serial: Certificate serial number
//   - issuers: Certificate issuer information
//   - subject: Certificate subject information
//   - not_after, not_before: Certificate validity period
//
// The name parameter can use dots (e.g., "example.com.pem"), which will be sanitized
// automatically before calling the API.
//
// Works with all HAProxy DataPlane API versions (v3.0+).
func (c *DataplaneClient) GetSSLCertificateContent(ctx context.Context, name string) (string, error) {
	// Sanitize the name for the API (e.g., "example.com.pem" -> "example_com.pem")
	sanitizedName := SanitizeSSLCertName(name)

	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) { return c.GetOneStorageSSLCertificate(ctx, sanitizedName) },
		V32: func(c *v32.Client) (*http.Response, error) { return c.GetOneStorageSSLCertificate(ctx, sanitizedName) },
		V31: func(c *v31.Client) (*http.Response, error) { return c.GetOneStorageSSLCertificate(ctx, sanitizedName) },
		V30: func(c *v30.Client) (*http.Response, error) { return c.GetOneStorageSSLCertificate(ctx, sanitizedName) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.GetOneStorageSSLCertificate(ctx, sanitizedName)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.GetOneStorageSSLCertificate(ctx, sanitizedName)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.GetOneStorageSSLCertificate(ctx, sanitizedName)
		},
	})

	if err != nil {
		return "", fmt.Errorf("getting SSL certificate '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("SSL certificate '%s' not found", name)
	}

	if err := CheckResponse(resp, fmt.Sprintf("get SSL certificate '%s'", name)); err != nil {
		return "", err
	}

	// Read entire response body first to handle empty responses
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response body for SSL certificate '%s': %w", name, err)
	}

	// Check if body is empty (can happen for empty certificates)
	if len(bodyBytes) == 0 {
		// Empty response - treat as empty certificate content
		return "", nil
	}

	// Parse response body - include sha256_finger_print field and fallback fields
	// Try both underscore and dash versions as field name may vary by API version
	var apiCert struct {
		StorageName        *string `json:"storage_name"`
		File               *string `json:"file"`
		Description        *string `json:"description"`
		SHA256Fingerprint  *string `json:"sha256_finger_print"`
		SHA256Fingerprint2 *string `json:"sha256-finger-print"` // Try dash version
		Serial             *string `json:"serial"`              // For fallback identification
		Issuers            *string `json:"issuers"`             // For fallback identification (API returns string, not array)
	}

	if err := json.Unmarshal(bodyBytes, &apiCert); err != nil {
		// Include response body in error for debugging
		bodySnippet := string(bodyBytes)
		if len(bodySnippet) > 200 {
			bodySnippet = bodySnippet[:200] + "..."
		}
		return "", fmt.Errorf("decoding SSL certificate response (body: %s): %w", bodySnippet, err)
	}

	// Always use serial+issuers format for certificate identification.
	// This is more reliable than sha256_finger_print because:
	// 1. Serial and issuers are always populated by the API (all versions)
	// 2. Our controller calculates the same format, ensuring consistent comparison
	// 3. Avoids format detection complexity between API versions
	//
	// A certificate is uniquely identified by its serial number within a CA (issuer).
	if apiCert.Serial != nil && *apiCert.Serial != "" {
		issuersStr := ""
		if apiCert.Issuers != nil && *apiCert.Issuers != "" {
			// Sort issuers alphabetically for deterministic comparison.
			// The API stores issuers in a Go map with undefined iteration order,
			// so both sides must normalize by sorting to ensure consistent matching.
			issuers := strings.Split(*apiCert.Issuers, ", ")
			slices.Sort(issuers)
			issuersStr = strings.Join(issuers, ", ")
		}
		return fmt.Sprintf("cert:serial:%s:issuers:%s", *apiCert.Serial, issuersStr), nil
	}

	// Serial not available - this should not happen in practice for valid certificates.
	// Return placeholder that will trigger UPDATE operations.
	return "__NO_FINGERPRINT__", nil
}

// CreateSSLCertificate creates a new SSL certificate using multipart form-data,
// always sending skip_reload=true. Symmetric with UpdateSSLCertificate's
// existing skip_reload plumbing (added in 77c760c2 "skip_reload=true on
// aux-file UPDATEs"); the asymmetry on CREATE was an oversight, since the
// DPAPI's POST /storage/ssl_certificates endpoint declares skip_reload as a
// query parameter (unlike POST /storage/maps and /storage/general, where the
// spec doesn't expose it).
//
// Without this, every cert CREATE during PhasePreConfig triggers a DPAPI
// auto-reload that validates the CURRENT haproxy.cfg against the new on-disk
// cert. The reload normally succeeds (the new cert is just-written real
// content), but a parallel reconciliation cycle landing between the cert
// CREATE's reload and the orchestrator's PhaseConfig push can race against
// stale-cfg state — same shape as the UPDATE bug the May fix closed.
//
// Returns the reload ID if a reload was triggered (always empty under
// skip_reload=true) and any error. The name parameter can use dots (e.g.,
// "example.com.pem"), which will be sanitized automatically before calling
// the API. Works with all HAProxy DataPlane API versions (v3.0+).
func (c *DataplaneClient) CreateSSLCertificate(ctx context.Context, name, content string) (string, error) {
	// Sanitize the name for the API (e.g., "example.com.pem" -> "example_com.pem")
	sanitizedName := SanitizeSSLCertName(name)

	body, contentType, err := buildMultipartFilePayload(sanitizedName, content)
	if err != nil {
		return "", fmt.Errorf("building payload for SSL certificate '%s': %w", name, err)
	}

	skipReload := true
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.CreateStorageSSLCertificateWithBody(ctx, &v33.CreateStorageSSLCertificateParams{SkipReload: &skipReload}, contentType, body)
		},
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.CreateStorageSSLCertificateWithBody(ctx, &v32.CreateStorageSSLCertificateParams{SkipReload: &skipReload}, contentType, body)
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.CreateStorageSSLCertificateWithBody(ctx, &v31.CreateStorageSSLCertificateParams{SkipReload: &skipReload}, contentType, body)
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return c.CreateStorageSSLCertificateWithBody(ctx, &v30.CreateStorageSSLCertificateParams{SkipReload: &skipReload}, contentType, body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.CreateStorageSSLCertificateWithBody(ctx, &v32ee.CreateStorageSSLCertificateParams{SkipReload: &skipReload}, contentType, body)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.CreateStorageSSLCertificateWithBody(ctx, &v31ee.CreateStorageSSLCertificateParams{SkipReload: &skipReload}, contentType, body)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.CreateStorageSSLCertificateWithBody(ctx, &v30ee.CreateStorageSSLCertificateParams{SkipReload: &skipReload}, contentType, body)
		},
	})

	if err != nil {
		return "", fmt.Errorf("creating SSL certificate '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return checkCreateResponse(resp, "SSL certificate", name)
}

// UpdateSSLCertificate updates an existing SSL certificate using text/plain content.
// Always sends skip_reload=true; see UpdateGeneralFile for the rationale. The new
// PEM is written to disk but HAProxy keeps serving the old cert in memory until the
// next reload, which the orchestrator triggers explicitly when only aux files
// changed (and the main config sync's commit triggers it otherwise).
// Returns the reload ID if a reload was triggered (always empty under
// skip_reload=true) and any error. The name parameter can use dots (e.g.,
// "example.com.pem"), which will be sanitized automatically before calling the API.
// Works with all HAProxy DataPlane API versions (v3.0+).
func (c *DataplaneClient) UpdateSSLCertificate(ctx context.Context, name, content string) (string, error) {
	// Sanitize the name for the API (e.g., "example.com.pem" -> "example_com.pem")
	sanitizedName := SanitizeSSLCertName(name)

	// Send certificate content as text/plain (per API spec: postHAProxyConfigurationData)
	body := bytes.NewBufferString(content)

	skipReload := true
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.ReplaceStorageSSLCertificateWithBody(ctx, sanitizedName, &v33.ReplaceStorageSSLCertificateParams{SkipReload: &skipReload}, "text/plain", body)
		},
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.ReplaceStorageSSLCertificateWithBody(ctx, sanitizedName, &v32.ReplaceStorageSSLCertificateParams{SkipReload: &skipReload}, "text/plain", body)
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.ReplaceStorageSSLCertificateWithBody(ctx, sanitizedName, &v31.ReplaceStorageSSLCertificateParams{SkipReload: &skipReload}, "text/plain", body)
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return c.ReplaceStorageSSLCertificateWithBody(ctx, sanitizedName, &v30.ReplaceStorageSSLCertificateParams{SkipReload: &skipReload}, "text/plain", body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.ReplaceStorageSSLCertificateWithBody(ctx, sanitizedName, &v32ee.ReplaceStorageSSLCertificateParams{SkipReload: &skipReload}, "text/plain", body)
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.ReplaceStorageSSLCertificateWithBody(ctx, sanitizedName, &v31ee.ReplaceStorageSSLCertificateParams{SkipReload: &skipReload}, "text/plain", body)
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.ReplaceStorageSSLCertificateWithBody(ctx, sanitizedName, &v30ee.ReplaceStorageSSLCertificateParams{SkipReload: &skipReload}, "text/plain", body)
		},
	})

	if err != nil {
		return "", fmt.Errorf("updating SSL certificate '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return checkUpdateResponse(resp, "SSL certificate", name)
}

// DeleteSSLCertificate deletes an SSL certificate by name, always sending
// skip_reload=true. This closes the last gap in the skip_reload family
// (UPDATE got it in 77c760c2, CREATE followed): without it, the DPAPI
// answers 202 and its reload agent schedules a SECOND, uncoordinated reload
// shortly after the deploy's own force_reload config push (deletes run
// post-config, so the deleted cert is already unreferenced by the live
// config and running workers keep their in-memory copy — nothing needs a
// reload). That stray reload blacks out the master CLI socket mid-rollout,
// which is exactly the window issue #67 captured: endpoint fast-path
// `set server` pushes fail against the re-executing master while the
// outgoing worker drains with a stale server list, turning a routine
// single-replica rollout into a 503.
//
// The name parameter can use dots (e.g., "example.com.pem"), which will be
// sanitized automatically before calling the API. Works with all HAProxy
// DataPlane API versions (v3.0+).
func (c *DataplaneClient) DeleteSSLCertificate(ctx context.Context, name string) error {
	// Sanitize the name for the API (e.g., "example.com.pem" -> "example_com.pem")
	sanitizedName := SanitizeSSLCertName(name)

	skipReload := true
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.DeleteStorageSSLCertificate(ctx, sanitizedName, &v33.DeleteStorageSSLCertificateParams{SkipReload: &skipReload})
		},
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.DeleteStorageSSLCertificate(ctx, sanitizedName, &v32.DeleteStorageSSLCertificateParams{SkipReload: &skipReload})
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.DeleteStorageSSLCertificate(ctx, sanitizedName, &v31.DeleteStorageSSLCertificateParams{SkipReload: &skipReload})
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return c.DeleteStorageSSLCertificate(ctx, sanitizedName, &v30.DeleteStorageSSLCertificateParams{SkipReload: &skipReload})
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.DeleteStorageSSLCertificate(ctx, sanitizedName, &v32ee.DeleteStorageSSLCertificateParams{SkipReload: &skipReload})
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.DeleteStorageSSLCertificate(ctx, sanitizedName, &v31ee.DeleteStorageSSLCertificateParams{SkipReload: &skipReload})
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.DeleteStorageSSLCertificate(ctx, sanitizedName, &v30ee.DeleteStorageSSLCertificateParams{SkipReload: &skipReload})
		},
	})

	if err != nil {
		return fmt.Errorf("deleting SSL certificate '%s': %w", name, err)
	}
	defer resp.Body.Close()

	return checkDeleteResponse(resp, "SSL certificate", name)
}
