package dataplane

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

func TestComputeContentChecksum_Deterministic(t *testing.T) {
	// Create auxiliary files in different orders to verify sorting produces same checksum
	config := "global\n    daemon\n"

	// Order 1: A, B, C
	auxFiles1 := &AuxiliaryFiles{
		GeneralFiles: []auxiliaryfiles.GeneralFile{
			{Filename: "a.http", Content: "content a"},
			{Filename: "b.http", Content: "content b"},
			{Filename: "c.http", Content: "content c"},
		},
		MapFiles: []auxiliaryfiles.MapFile{
			{Path: "/maps/a.map", Content: "key1 value1"},
			{Path: "/maps/b.map", Content: "key2 value2"},
		},
		SSLCertificates: []auxiliaryfiles.SSLCertificate{
			{Path: "/certs/a.pem", Content: "cert a"},
			{Path: "/certs/b.pem", Content: "cert b"},
		},
		CRTListFiles: []auxiliaryfiles.CRTListFile{
			{Path: "/crt-list/a.txt", Content: "list a"},
			{Path: "/crt-list/b.txt", Content: "list b"},
		},
	}

	// Order 2: C, A, B (different order, same content)
	auxFiles2 := &AuxiliaryFiles{
		GeneralFiles: []auxiliaryfiles.GeneralFile{
			{Filename: "c.http", Content: "content c"},
			{Filename: "a.http", Content: "content a"},
			{Filename: "b.http", Content: "content b"},
		},
		MapFiles: []auxiliaryfiles.MapFile{
			{Path: "/maps/b.map", Content: "key2 value2"},
			{Path: "/maps/a.map", Content: "key1 value1"},
		},
		SSLCertificates: []auxiliaryfiles.SSLCertificate{
			{Path: "/certs/b.pem", Content: "cert b"},
			{Path: "/certs/a.pem", Content: "cert a"},
		},
		CRTListFiles: []auxiliaryfiles.CRTListFile{
			{Path: "/crt-list/b.txt", Content: "list b"},
			{Path: "/crt-list/a.txt", Content: "list a"},
		},
	}

	checksum1 := ComputeContentChecksum(config, auxFiles1)
	checksum2 := ComputeContentChecksum(config, auxFiles2)

	assert.Equal(t, checksum1, checksum2, "Checksums should be identical regardless of file order")
}

func TestComputeContentChecksum_DifferentContent(t *testing.T) {
	config := "global\n    daemon\n"

	auxFiles1 := &AuxiliaryFiles{
		GeneralFiles: []auxiliaryfiles.GeneralFile{
			{Filename: "a.http", Content: "content a"},
		},
	}

	auxFiles2 := &AuxiliaryFiles{
		GeneralFiles: []auxiliaryfiles.GeneralFile{
			{Filename: "a.http", Content: "content b"}, // Different content
		},
	}

	checksum1 := ComputeContentChecksum(config, auxFiles1)
	checksum2 := ComputeContentChecksum(config, auxFiles2)

	assert.NotEqual(t, checksum1, checksum2, "Checksums should differ when content differs")
}

func TestComputeContentChecksum_NilAuxFiles(t *testing.T) {
	config := "global\n    daemon\n"

	checksum := ComputeContentChecksum(config, nil)

	require.NotEmpty(t, checksum)
	assert.Len(t, checksum, 16, "Checksum should be 16 hex characters (8 bytes)")
}

func TestComputeContentChecksum_EmptyAuxFiles(t *testing.T) {
	config := "global\n    daemon\n"

	auxFiles := &AuxiliaryFiles{}

	checksum := ComputeContentChecksum(config, auxFiles)

	require.NotEmpty(t, checksum)
	assert.Len(t, checksum, 16, "Checksum should be 16 hex characters (8 bytes)")
}

func TestComputeContentChecksum_ConfigChange(t *testing.T) {
	auxFiles := &AuxiliaryFiles{
		GeneralFiles: []auxiliaryfiles.GeneralFile{
			{Filename: "a.http", Content: "content"},
		},
	}

	checksum1 := ComputeContentChecksum("config1", auxFiles)
	checksum2 := ComputeContentChecksum("config2", auxFiles)

	assert.NotEqual(t, checksum1, checksum2, "Checksums should differ when config differs")
}

func TestComputeContentChecksum_StableAcrossMultipleCalls(t *testing.T) {
	config := "global\n    daemon\n"
	auxFiles := &AuxiliaryFiles{
		GeneralFiles: []auxiliaryfiles.GeneralFile{
			{Filename: "error.http", Content: "HTTP/1.0 500 Error"},
		},
		MapFiles: []auxiliaryfiles.MapFile{
			{Path: "/maps/hosts.map", Content: "example.com backend1"},
		},
	}

	// Call multiple times to ensure stability
	checksums := make([]string, 10)
	for i := 0; i < 10; i++ {
		checksums[i] = ComputeContentChecksum(config, auxFiles)
	}

	// All checksums should be identical
	for i := 1; i < 10; i++ {
		assert.Equal(t, checksums[0], checksums[i], "Checksum should be stable across multiple calls")
	}
}
