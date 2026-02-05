package dataplane

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

// ComputeContentChecksum generates a SHA256 checksum covering the main HAProxy config
// and all auxiliary files (general files, map files, SSL certificates, CRT-list files).
//
// The checksum is used for content deduplication to skip redundant processing when
// config content hasn't changed. Auxiliary file slices must be pre-sorted (by
// AuxiliaryFiles.Sort()) to ensure deterministic results regardless of insertion order.
//
// Returns a hex-encoded 8-byte (16 character) checksum for brevity.
func ComputeContentChecksum(haproxyConfig string, auxFiles *AuxiliaryFiles) string {
	h := sha256.New()

	// Hash main config
	h.Write([]byte(haproxyConfig))

	// Hash auxiliary files (slices are pre-sorted by AuxiliaryFiles.Sort)
	if auxFiles != nil {
		hashGeneralFiles(h, auxFiles.GeneralFiles)
		hashMapFiles(h, auxFiles.MapFiles)
		hashSSLCertificates(h, auxFiles.SSLCertificates)
		hashCRTListFiles(h, auxFiles.CRTListFiles)
	}

	checksum := h.Sum(nil)
	return hex.EncodeToString(checksum[:8]) // First 8 bytes for brevity
}

// hashGeneralFiles hashes general files in order (must be pre-sorted by Filename).
func hashGeneralFiles(h hash.Hash, files []auxiliaryfiles.GeneralFile) {
	for _, f := range files {
		h.Write([]byte(f.Filename))
		h.Write([]byte(f.Content))
	}
}

// hashMapFiles hashes map files in order (must be pre-sorted by Path).
func hashMapFiles(h hash.Hash, files []auxiliaryfiles.MapFile) {
	for _, f := range files {
		h.Write([]byte(f.Path))
		h.Write([]byte(f.Content))
	}
}

// hashSSLCertificates hashes SSL certificates in order (must be pre-sorted by Path).
func hashSSLCertificates(h hash.Hash, files []auxiliaryfiles.SSLCertificate) {
	for _, f := range files {
		h.Write([]byte(f.Path))
		h.Write([]byte(f.Content))
	}
}

// hashCRTListFiles hashes CRT-list files in order (must be pre-sorted by Path).
func hashCRTListFiles(h hash.Hash, files []auxiliaryfiles.CRTListFile) {
	for _, f := range files {
		h.Write([]byte(f.Path))
		h.Write([]byte(f.Content))
	}
}
