package renderer

import (
	"fmt"
	"path/filepath"
	"sync"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/templating"
)

// FileRegistry allows templates to dynamically register auxiliary files
// (certs, maps, general files) during rendering. This is used for cases
// where file content comes from dynamic sources (e.g., certificates from secrets)
// rather than pre-declared templates.
//
// Usage in templates:
//
//	{% set ca_content = secret.data["ca.crt"] | b64decode %}
//	{% set ca_path = file_registry.Register("cert", "my-backend-ca.pem", ca_content) %}
//	server backend:443 ssl ca-file {{ ca_path }} verify required
//
// The Registry method is called on the FileRegistry object in the template
// rendering context, not as a standalone filter.
type FileRegistry struct {
	mu           sync.Mutex
	registered   map[string]registeredFile
	pathResolver *templating.PathResolver
}

// registeredFile tracks a dynamically-registered file.
type registeredFile struct {
	Type     string // "cert", "map", "file", or "crt-list"
	Filename string // Base filename
	Content  string // File content
	Path     string // Predicted full path
}

// NewFileRegistry creates a new FileRegistry with the given path resolver.
// The path resolver is used to compute full paths for registered files,
// ensuring they match the paths used by pathResolver.GetPath() method.
func NewFileRegistry(pathResolver *templating.PathResolver) *FileRegistry {
	return &FileRegistry{
		registered:   make(map[string]registeredFile),
		pathResolver: pathResolver,
	}
}

// Register registers a new auxiliary file to be created and returns its predicted path.
// This method is called from templates as file_registry.Register(type, filename, content).
//
// Parameters:
//   - fileType: "cert", "map", "file", or "crt-list"
//   - filename: Base filename (e.g., "ca.pem", "domains.map", "certificate-list.txt")
//   - content: File content as a string
//
// Returns:
//   - Predicted absolute path where the file will be located
//   - Error if validation fails or content conflict detected
//
// Conflict Detection:
//   - If the same filename is registered multiple times with different content, returns error
//   - If the same filename is registered with identical content, no error (idempotent)
func (r *FileRegistry) Register(args ...interface{}) (string, error) {
	// Validate argument count
	if len(args) != 3 {
		return "", fmt.Errorf("file_registry.Register requires 3 arguments (type, filename, content), got %d", len(args))
	}

	// Extract and validate file type
	fileType, ok := args[0].(string)
	if !ok {
		return "", fmt.Errorf("file_registry.Register: type must be a string, got %T", args[0])
	}

	// Extract and validate filename
	filename, ok := args[1].(string)
	if !ok {
		return "", fmt.Errorf("file_registry.Register: filename must be a string, got %T", args[1])
	}

	// Extract and validate content
	content, ok := args[2].(string)
	if !ok {
		return "", fmt.Errorf("file_registry.Register: content must be a string, got %T", args[2])
	}

	// Validate file type
	switch fileType {
	case "cert", "map", "file", "crt-list":
		// Valid types
	default:
		return "", fmt.Errorf("file_registry.Register: invalid file type %q, must be \"cert\", \"map\", \"file\", or \"crt-list\"", fileType)
	}

	// Compute predicted path using path resolver (same logic as pathResolver.GetPath() method)
	pathInterface, err := r.pathResolver.GetPath(filename, fileType)
	if err != nil {
		return "", fmt.Errorf("file_registry.Register: failed to compute path: %w", err)
	}

	path, ok := pathInterface.(string)
	if !ok {
		return "", fmt.Errorf("file_registry.Register: path resolver returned unexpected type %T", pathInterface)
	}

	// Thread-safe registration
	r.mu.Lock()
	defer r.mu.Unlock()

	// Create lookup key (type:filename)
	key := fileType + ":" + filename

	// Check for conflicts
	if existing, exists := r.registered[key]; exists {
		if existing.Content != content {
			return "", fmt.Errorf(
				"file_registry.Register: content conflict for %s %q - already registered with different content (existing size: %d, new size: %d)",
				fileType, filename, len(existing.Content), len(content),
			)
		}

		// Same content - idempotent, return existing path
		return existing.Path, nil
	}

	// Register new file
	r.registered[key] = registeredFile{
		Type:     fileType,
		Filename: filename,
		Content:  content,
		Path:     path,
	}

	return path, nil
}

// GetFiles converts all registered files to dataplane AuxiliaryFiles structure.
// This is called by the renderer after template rendering completes to merge
// dynamic files with pre-declared auxiliary files.
func (r *FileRegistry) GetFiles() *dataplane.AuxiliaryFiles {
	r.mu.Lock()
	defer r.mu.Unlock()

	files := &dataplane.AuxiliaryFiles{}

	for _, reg := range r.registered {
		switch reg.Type {
		case "cert":
			files.SSLCertificates = append(files.SSLCertificates, auxiliaryfiles.SSLCertificate{
				Path:    reg.Path,
				Content: reg.Content,
			})

		case "map":
			files.MapFiles = append(files.MapFiles, auxiliaryfiles.MapFile{
				Path:    reg.Path,
				Content: reg.Content,
			})

		case "crt-list":
			files.CRTListFiles = append(files.CRTListFiles, auxiliaryfiles.CRTListFile{
				Path:    reg.Path,
				Content: reg.Content,
			})

		case "file":
			// GeneralFile uses Filename (base name) not Path
			files.GeneralFiles = append(files.GeneralFiles, auxiliaryfiles.GeneralFile{
				Filename: filepath.Base(reg.Path),
				Content:  reg.Content,
			})
		}
	}

	return files
}
