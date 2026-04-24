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
		hashFileItems(h, auxFiles.GeneralFiles)
		hashFileItems(h, auxFiles.MapFiles)
		hashFileItems(h, auxFiles.SSLCertificates)
		hashFileItems(h, auxFiles.SSLCaFiles)
		hashFileItems(h, auxFiles.CRTListFiles)
	}

	checksum := h.Sum(nil)
	return hex.EncodeToString(checksum[:8]) // First 8 bytes for brevity
}

// hashFileItems writes each item's identifier and content to h in slice order.
// Slices must be pre-sorted by the caller (typically via AuxiliaryFiles.Sort)
// to keep results deterministic.
func hashFileItems[T auxiliaryfiles.FileItem](h hash.Hash, items []T) {
	for _, item := range items {
		h.Write([]byte(item.GetIdentifier()))
		h.Write([]byte(item.GetContent()))
	}
}
