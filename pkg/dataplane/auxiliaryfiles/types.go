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

	// IsCaFile marks this general file as an SSL CA / trust bundle referenced by
	// the config as `ca-file <path>` (frontend client-cert verify or backend mTLS
	// server verify). When set, a CONTENT-only update can be applied to the live
	// worker via the runtime API (`add ssl ca-file` + commit, which replaces the
	// file with the payload) without a reload on DataPlane API v3.2+ — the
	// orchestrator's runtime fast path keys off this flag. It is metadata only:
	// GetContent (used for diffing) ignores it, so it never causes a spurious diff
	// against the content-keyed current state.
	IsCaFile bool
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
}

// GetIdentifier implements the FileItem interface.
func (c CRTListFile) GetIdentifier() string {
	return c.Path
}

// GetContent implements the FileItem interface.
func (c CRTListFile) GetContent() string {
	return c.Content
}

// FileDiff is the diff produced for general files. It is an alias of
// FileDiffGeneric[GeneralFile]; HasChanges and the underlying field set come
// from the generic type.
type FileDiff = FileDiffGeneric[GeneralFile]

// SSLCertificateDiff is the diff produced for SSL certificates. Alias of
// FileDiffGeneric[SSLCertificate].
type SSLCertificateDiff = FileDiffGeneric[SSLCertificate]

// MapFileDiff is the diff produced for map files. Alias of
// FileDiffGeneric[MapFile].
type MapFileDiff = FileDiffGeneric[MapFile]

// CRTListDiff is the diff produced for crt-list files. Alias of
// FileDiffGeneric[CRTListFile].
type CRTListDiff = FileDiffGeneric[CRTListFile]

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

// SSLCaFileDiff is the diff produced for SSL CA files. Alias of
// FileDiffGeneric[SSLCaFile].
type SSLCaFileDiff = FileDiffGeneric[SSLCaFile]
