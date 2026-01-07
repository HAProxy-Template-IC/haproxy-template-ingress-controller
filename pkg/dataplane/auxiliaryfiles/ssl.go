package auxiliaryfiles

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

// calculateSHA256Fingerprint calculates the SHA256 hash of content.
func calculateSHA256Fingerprint(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

// calculateCertIdentifier returns a unique identifier for certificate comparison.
// When the content is a valid PEM certificate, it returns a "cert:serial:XXX:issuers:YYY"
// format to match the API response fallback (used when sha256_finger_print is unavailable).
// For non-certificate content (keys, combined files), it falls back to SHA256 fingerprint.
//
// The HAProxy DataPlane API returns "issuers" as a comma-separated list of issuer CNs
// from ALL certificates in the PEM chain (e.g., "R12, ISRG Root X1" for Let's Encrypt certs).
// This function parses all certificates in the chain to match that format exactly.
//
// This is a workaround for https://github.com/haproxytech/dataplaneapi/pull/396
// Once the upstream fix is released, the API will return sha256_finger_print and
// this fallback format won't be used (both sides will use fingerprint directly).
func calculateCertIdentifier(content string) string {
	// Parse all certificates in the PEM content
	var leafCert *x509.Certificate
	var issuerCNs []string

	data := []byte(content)
	for {
		block, rest := pem.Decode(data)
		if block == nil {
			break
		}

		if block.Type == "CERTIFICATE" {
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				// Invalid certificate - use SHA256 fingerprint
				return calculateSHA256Fingerprint(content)
			}

			// First certificate is the leaf (used for serial number)
			if leafCert == nil {
				leafCert = cert
			}

			// Collect issuer CN from each certificate
			issuerCN := cert.Issuer.CommonName
			if issuerCN == "" {
				// Fallback for self-signed certs where issuer may be empty but subject has CN
				issuerCN = cert.Subject.CommonName
			}
			if issuerCN != "" {
				issuerCNs = append(issuerCNs, issuerCN)
			}
		}

		data = rest
	}

	// No valid certificates found - use SHA256 fingerprint
	if leafCert == nil {
		return calculateSHA256Fingerprint(content)
	}

	// Return format matching HAProxy API: "cert:serial:XXX:issuers:YYY"
	// Sort issuers alphabetically for deterministic comparison.
	// The API stores issuers in a Go map with undefined iteration order,
	// so both sides must normalize by sorting to ensure consistent matching.
	sort.Strings(issuerCNs)
	issuersStr := strings.Join(issuerCNs, ", ")
	return fmt.Sprintf("cert:serial:%s:issuers:%s", leafCert.SerialNumber.String(), issuersStr)
}

// sslCertificateOps implements FileOperations for SSLCertificate.
type sslCertificateOps struct {
	client *client.DataplaneClient
}

func (o *sslCertificateOps) GetAll(ctx context.Context) ([]string, error) {
	// NOTE: API returns filenames only (e.g., "cert.pem"), not absolute paths.
	// Comparison logic in CompareSSLCertificates() handles path normalization.
	return o.client.GetAllSSLCertificates(ctx)
}

func (o *sslCertificateOps) GetContent(ctx context.Context, id string) (string, error) {
	// Extract filename from path (API expects filename only)
	filename := filepath.Base(id)
	return o.client.GetSSLCertificateContent(ctx, filename)
}

func (o *sslCertificateOps) Create(ctx context.Context, id, content string) (string, error) {
	// Extract filename from path (API expects filename only)
	filename := filepath.Base(id)
	reloadID, err := o.client.CreateSSLCertificate(ctx, filename, content)
	if err != nil && strings.Contains(err.Error(), "already exists") {
		// Certificate already exists, fall back to update instead of failing.
		// This handles the case where a previous deployment partially succeeded
		// or where path normalization causes comparison mismatches.
		return o.Update(ctx, id, content)
	}
	return reloadID, err
}

func (o *sslCertificateOps) Update(ctx context.Context, id, content string) (string, error) {
	// Extract filename from path (API expects filename only)
	filename := filepath.Base(id)
	return o.client.UpdateSSLCertificate(ctx, filename, content)
}

func (o *sslCertificateOps) Delete(ctx context.Context, id string) error {
	// Extract filename from path (API expects filename only)
	filename := filepath.Base(id)
	return o.client.DeleteSSLCertificate(ctx, filename)
}

// CompareSSLCertificates compares the current state of SSL certificates in HAProxy storage
// with the desired state, and returns a diff describing what needs to be created,
// updated, or deleted.
//
// This function uses identifier-based comparison using certificate serial+issuer format.
// Both the API side and controller side use the same format ("cert:serial:XXX:issuers:YYY"),
// ensuring consistent comparison across all HAProxy DataPlane API versions.
//
// Strategy:
//  1. Fetch current certificate names from the Dataplane API
//  2. Fetch identifiers for all current certificates (serial+issuer format)
//  3. Compare identifiers with desired certificates
//  4. Return diff with create, update, and delete operations
//
// Path normalization: The API returns filenames only (e.g., "cert.pem"), but SSLCertificate.Path
// may contain full paths (e.g., "/etc/haproxy/ssl/cert.pem"). We normalize using filepath.Base()
// for comparison.
func CompareSSLCertificates(ctx context.Context, c *client.DataplaneClient, desired []SSLCertificate) (*SSLCertificateDiff, error) {
	// Normalize desired certificates to use filenames for identifiers
	// and calculate unique identifiers for content comparison
	// IMPORTANT: Sanitize names to match HAProxy Dataplane API behavior
	// (dots in basename are replaced with underscores)
	normalizedDesired := make([]SSLCertificate, len(desired))
	for i, cert := range desired {
		normalizedDesired[i] = SSLCertificate{
			Path:    client.SanitizeSSLCertName(filepath.Base(cert.Path)),
			Content: calculateCertIdentifier(cert.Content),
		}
	}

	ops := &sslCertificateOps{client: c}

	// Use generic Compare function with identifier-based comparison
	genericDiff, err := Compare[SSLCertificate](
		ctx,
		ops,
		normalizedDesired,
		func(id, fingerprint string) SSLCertificate {
			return SSLCertificate{
				Path:    id,
				Content: fingerprint,
			}
		},
	)
	if err != nil {
		return nil, err
	}

	// Convert generic diff to SSL certificate diff
	// Note: For SSL certificates, we need to use original desired certificates (with full paths)
	// for Create/Update operations, but use normalized paths for Delete operations
	desiredMap := make(map[string]SSLCertificate)
	for _, cert := range desired {
		// Use sanitized basename as key to match normalized paths from genericDiff
		desiredMap[client.SanitizeSSLCertName(filepath.Base(cert.Path))] = cert
	}

	diff := &SSLCertificateDiff{
		ToCreate: make([]SSLCertificate, 0, len(genericDiff.ToCreate)),
		ToUpdate: make([]SSLCertificate, 0, len(genericDiff.ToUpdate)),
		ToDelete: genericDiff.ToDelete,
	}

	// Restore original paths for create operations
	for _, cert := range genericDiff.ToCreate {
		if original, exists := desiredMap[cert.Path]; exists {
			diff.ToCreate = append(diff.ToCreate, original)
		}
	}

	// Restore original paths for update operations
	for _, cert := range genericDiff.ToUpdate {
		if original, exists := desiredMap[cert.Path]; exists {
			diff.ToUpdate = append(diff.ToUpdate, original)
		}
	}

	return diff, nil
}

// SyncSSLCertificates synchronizes SSL certificates to the desired state by applying
// the provided diff. This function should be called in two phases:
//   - Phase 1 (pre-config): Call with diff containing ToCreate and ToUpdate
//   - Phase 2 (post-config): Call with diff containing ToDelete
//
// The caller is responsible for splitting the diff into these phases.
// Returns reload IDs from create/update operations that triggered reloads.
func SyncSSLCertificates(ctx context.Context, c *client.DataplaneClient, diff *SSLCertificateDiff) ([]string, error) {
	if diff == nil {
		return nil, nil
	}

	ops := &sslCertificateOps{client: c}

	// Convert SSLCertificateDiff to generic diff
	genericDiff := &FileDiffGeneric[SSLCertificate]{
		ToCreate: diff.ToCreate,
		ToUpdate: diff.ToUpdate,
		ToDelete: diff.ToDelete,
	}

	// Use generic Sync function
	return Sync[SSLCertificate](ctx, ops, genericDiff)
}
