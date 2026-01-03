// Package auxiliaryfiles provides functionality for synchronizing auxiliary files
// (general files, SSL certificates, map files, crt-lists) with the HAProxy Dataplane API.
//
// Auxiliary files are supplementary files that HAProxy needs but are not part of the
// main configuration file, such as:
//   - General files: Error pages, custom response files, ACL files
//   - SSL certificates: TLS/SSL certificate and key files
//   - Map files: Dynamic key-value mappings
//   - CRT-list files: SSL certificate lists with per-certificate options
package auxiliaryfiles

// GeneralFile represents a general-purpose file (error files, custom response files, etc.).
// These files are uploaded to the Dataplane API storage and can be referenced in the
// HAProxy configuration (e.g., in http-errors sections).
type GeneralFile struct {
	// Filename is the base file name (used as API 'id').
	// Example: "400.http"
	Filename string

	// Path is the absolute file path where the file is stored.
	// This is computed from the configured GeneralStorageDir + Filename.
	// Example: "/etc/haproxy/general/400.http"
	Path string

	// Content is the file contents as a string. This maps to the 'file' field in
	// multipart form uploads to the Dataplane API.
	Content string
}

// GetIdentifier implements the FileItem interface.
func (g GeneralFile) GetIdentifier() string {
	return g.Filename
}

// GetContent implements the FileItem interface.
func (g GeneralFile) GetContent() string {
	return g.Content
}

// SSLCertificate represents an SSL/TLS certificate file containing certificates and keys.
// These files are used for HTTPS termination and client certificate authentication.
type SSLCertificate struct {
	// Path is the absolute file path to the certificate.
	// Example: "/etc/haproxy/certs/example.com.pem"
	Path string

	// Content is the PEM-encoded certificate and key data.
	Content string

	// Description is an optional human-readable description of the certificate.
	Description string

	// Future fields that might be added:
	// - Expiry time
	// - Certificate metadata (issuer, subject, etc.)
	// - Certificate chain information
}

// GetIdentifier implements the FileItem interface.
func (s SSLCertificate) GetIdentifier() string {
	return s.Path
}

// GetContent implements the FileItem interface.
func (s SSLCertificate) GetContent() string {
	return s.Content
}

// MapFile represents a HAProxy map file for dynamic key-value lookups.
// Map files enable runtime configuration changes without reloading HAProxy.
type MapFile struct {
	// Path is the absolute file path to the map file.
	// Example: "/etc/haproxy/maps/domains.map"
	Path string

	// Content is the map file contents (one key-value pair per line).
	Content string

	// Future fields that might be added:
	// - Map type/format
	// - Validation rules
	// - Update frequency hints
}

// GetIdentifier implements the FileItem interface.
func (m MapFile) GetIdentifier() string {
	return m.Path
}

// GetContent implements the FileItem interface.
func (m MapFile) GetContent() string {
	return m.Content
}

// CRTListFile represents a HAProxy crt-list file for SSL certificate lists with per-certificate options.
// CRT-list files allow specifying multiple certificates with individual SSL options and SNI filters.
type CRTListFile struct {
	// Path is the absolute file path to the crt-list file.
	// Example: "/etc/haproxy/crt-lists/crt-list.txt"
	Path string

	// Content is the crt-list file contents (one certificate entry per line).
	// Format: <cert-path> [ssl-options] [sni-filter]
	// Example: "/etc/haproxy/ssl/cert.pem [ocsp-update on] example.com"
	Content string

	// Future fields that might be added:
	// - Parsed entries for validation
	// - Certificate metadata
	// - OCSP stapling configuration
}

// GetIdentifier implements the FileItem interface.
func (c CRTListFile) GetIdentifier() string {
	return c.Path
}

// GetContent implements the FileItem interface.
func (c CRTListFile) GetContent() string {
	return c.Content
}

// FileDiff represents the differences between current and desired file states.
// It contains lists of files that need to be created, updated, or deleted.
type FileDiff struct {
	// ToCreate contains files that exist in the desired state but not in the current state.
	ToCreate []GeneralFile

	// ToUpdate contains files that exist in both states but have different content.
	ToUpdate []GeneralFile

	// ToDelete contains paths of files that exist in the current state but not in the desired state.
	// These are file paths (not full GeneralFile structs) since we only need the path to delete.
	ToDelete []string
}

// HasChanges returns true if there are any changes to general files.
func (d *FileDiff) HasChanges() bool {
	return len(d.ToCreate) > 0 || len(d.ToUpdate) > 0 || len(d.ToDelete) > 0
}

// SSLCertificateDiff represents the differences between current and desired SSL certificate states.
// It contains lists of certificates that need to be created, updated, or deleted.
type SSLCertificateDiff struct {
	// ToCreate contains certificates that exist in the desired state but not in the current state.
	ToCreate []SSLCertificate

	// ToUpdate contains certificates that exist in both states but have different content.
	ToUpdate []SSLCertificate

	// ToDelete contains certificate names that exist in the current state but not in the desired state.
	// These are certificate names (not full SSLCertificate structs) since we only need the name to delete.
	ToDelete []string
}

// HasChanges returns true if there are any changes to SSL certificates.
func (d *SSLCertificateDiff) HasChanges() bool {
	return len(d.ToCreate) > 0 || len(d.ToUpdate) > 0 || len(d.ToDelete) > 0
}

// MapFileDiff represents the differences between current and desired map file states.
// It contains lists of map files that need to be created, updated, or deleted.
type MapFileDiff struct {
	// ToCreate contains map files that exist in the desired state but not in the current state.
	ToCreate []MapFile

	// ToUpdate contains map files that exist in both states but have different content.
	ToUpdate []MapFile

	// ToDelete contains map file paths that exist in the current state but not in the desired state.
	// These are file paths (not full MapFile structs) since we only need the path to delete.
	ToDelete []string
}

// HasChanges returns true if there are any changes to map files.
func (d *MapFileDiff) HasChanges() bool {
	return len(d.ToCreate) > 0 || len(d.ToUpdate) > 0 || len(d.ToDelete) > 0
}

// CRTListDiff represents the differences between current and desired crt-list file states.
// It contains lists of crt-list files that need to be created, updated, or deleted.
type CRTListDiff struct {
	// ToCreate contains crt-list files that exist in the desired state but not in the current state.
	ToCreate []CRTListFile

	// ToUpdate contains crt-list files that exist in both states but have different content.
	ToUpdate []CRTListFile

	// ToDelete contains crt-list file paths that exist in the current state but not in the desired state.
	// These are file paths (not full CRTListFile structs) since we only need the path to delete.
	ToDelete []string
}

// HasChanges returns true if there are any changes to crt-list files.
func (d *CRTListDiff) HasChanges() bool {
	return len(d.ToCreate) > 0 || len(d.ToUpdate) > 0 || len(d.ToDelete) > 0
}

// SSLCaFile represents an SSL CA certificate file containing trusted CA certificates.
// These files are used for client certificate verification and SSL chain validation.
// SSL CA file storage is only available in HAProxy DataPlane API v3.2+.
type SSLCaFile struct {
	// Path is the file path or name of the CA file.
	// Example: "ca-bundle.pem" or "/etc/haproxy/ssl/ca/trusted-cas.pem"
	Path string

	// Content is the PEM-encoded CA certificate data.
	// Can contain multiple CA certificates concatenated together.
	Content string
}

// GetIdentifier implements the FileItem interface.
func (s SSLCaFile) GetIdentifier() string {
	return s.Path
}

// GetContent implements the FileItem interface.
func (s SSLCaFile) GetContent() string {
	return s.Content
}

// SSLCaFileDiff represents the differences between current and desired SSL CA file states.
// It contains lists of CA files that need to be created, updated, or deleted.
type SSLCaFileDiff struct {
	// ToCreate contains CA files that exist in the desired state but not in the current state.
	ToCreate []SSLCaFile

	// ToUpdate contains CA files that exist in both states but have different content.
	ToUpdate []SSLCaFile

	// ToDelete contains CA file names that exist in the current state but not in the desired state.
	ToDelete []string
}

// HasChanges returns true if there are any changes to SSL CA files.
func (d *SSLCaFileDiff) HasChanges() bool {
	return len(d.ToCreate) > 0 || len(d.ToUpdate) > 0 || len(d.ToDelete) > 0
}

// SSLCrlFile represents an SSL Certificate Revocation List (CRL) file.
// These files contain lists of revoked certificates and are used to validate
// that client certificates have not been revoked.
// SSL CRL file storage is only available in HAProxy DataPlane API v3.2+.
type SSLCrlFile struct {
	// Path is the file path or name of the CRL file.
	// Example: "revoked.crl" or "/etc/haproxy/ssl/crl/revoked.crl"
	Path string

	// Content is the PEM or DER-encoded CRL data.
	Content string
}

// GetIdentifier implements the FileItem interface.
func (s SSLCrlFile) GetIdentifier() string {
	return s.Path
}

// GetContent implements the FileItem interface.
func (s SSLCrlFile) GetContent() string {
	return s.Content
}

// SSLCrlFileDiff represents the differences between current and desired SSL CRL file states.
// It contains lists of CRL files that need to be created, updated, or deleted.
type SSLCrlFileDiff struct {
	// ToCreate contains CRL files that exist in the desired state but not in the current state.
	ToCreate []SSLCrlFile

	// ToUpdate contains CRL files that exist in both states but have different content.
	ToUpdate []SSLCrlFile

	// ToDelete contains CRL file names that exist in the current state but not in the desired state.
	ToDelete []string
}

// HasChanges returns true if there are any changes to SSL CRL files.
func (d *SSLCrlFileDiff) HasChanges() bool {
	return len(d.ToCreate) > 0 || len(d.ToUpdate) > 0 || len(d.ToDelete) > 0
}
