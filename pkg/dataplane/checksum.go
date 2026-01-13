package dataplane

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

// ComputeContentChecksum generates a SHA256 checksum covering the main HAProxy config
// and all auxiliary files (general files, map files, SSL certificates, CRT-list files).
//
// The checksum is used for content deduplication to skip redundant processing when
// config content hasn't changed. Files are sorted by path/identifier before hashing
// to ensure deterministic results regardless of collection order.
//
// Returns a hex-encoded 8-byte (16 character) checksum for brevity.
func ComputeContentChecksum(haproxyConfig string, auxFiles *AuxiliaryFiles) string {
	h := sha256.New()

	// Hash main config
	h.Write([]byte(haproxyConfig))

	// Hash auxiliary files in deterministic order
	if auxFiles != nil {
		hashGeneralFiles(h, auxFiles.GeneralFiles)
		hashMapFiles(h, auxFiles.MapFiles)
		hashSSLCertificates(h, auxFiles.SSLCertificates)
		hashCRTListFiles(h, auxFiles.CRTListFiles)
	}

	checksum := h.Sum(nil)
	return hex.EncodeToString(checksum[:8]) // First 8 bytes for brevity
}

// hashGeneralFiles hashes general files in deterministic order (sorted by Filename).
func hashGeneralFiles(h hash.Hash, files []auxiliaryfiles.GeneralFile) {
	sorted := slices.Clone(files)
	slices.SortFunc(sorted, func(a, b auxiliaryfiles.GeneralFile) int {
		return cmp.Compare(a.Filename, b.Filename)
	})
	for _, f := range sorted {
		h.Write([]byte(f.Filename))
		h.Write([]byte(f.Content))
	}
}

// hashMapFiles hashes map files in deterministic order (sorted by Path).
func hashMapFiles(h hash.Hash, files []auxiliaryfiles.MapFile) {
	sorted := slices.Clone(files)
	slices.SortFunc(sorted, func(a, b auxiliaryfiles.MapFile) int {
		return cmp.Compare(a.Path, b.Path)
	})
	for _, f := range sorted {
		h.Write([]byte(f.Path))
		h.Write([]byte(f.Content))
	}
}

// hashSSLCertificates hashes SSL certificates in deterministic order (sorted by Path).
func hashSSLCertificates(h hash.Hash, files []auxiliaryfiles.SSLCertificate) {
	sorted := slices.Clone(files)
	slices.SortFunc(sorted, func(a, b auxiliaryfiles.SSLCertificate) int {
		return cmp.Compare(a.Path, b.Path)
	})
	for _, f := range sorted {
		h.Write([]byte(f.Path))
		h.Write([]byte(f.Content))
	}
}

// hashCRTListFiles hashes CRT-list files in deterministic order (sorted by Path).
func hashCRTListFiles(h hash.Hash, files []auxiliaryfiles.CRTListFile) {
	sorted := slices.Clone(files)
	slices.SortFunc(sorted, func(a, b auxiliaryfiles.CRTListFile) int {
		return cmp.Compare(a.Path, b.Path)
	})
	for _, f := range sorted {
		h.Write([]byte(f.Path))
		h.Write([]byte(f.Content))
	}
}
