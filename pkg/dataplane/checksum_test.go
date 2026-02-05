package dataplane

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

func TestAuxiliaryFiles_Sort(t *testing.T) {
	aux := &AuxiliaryFiles{
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

	aux.Sort()

	// Verify sorted order
	assert.Equal(t, "a.http", aux.GeneralFiles[0].Filename)
	assert.Equal(t, "b.http", aux.GeneralFiles[1].Filename)
	assert.Equal(t, "c.http", aux.GeneralFiles[2].Filename)
	assert.Equal(t, "/maps/a.map", aux.MapFiles[0].Path)
	assert.Equal(t, "/maps/b.map", aux.MapFiles[1].Path)
	assert.Equal(t, "/certs/a.pem", aux.SSLCertificates[0].Path)
	assert.Equal(t, "/certs/b.pem", aux.SSLCertificates[1].Path)
	assert.Equal(t, "/crt-list/a.txt", aux.CRTListFiles[0].Path)
	assert.Equal(t, "/crt-list/b.txt", aux.CRTListFiles[1].Path)
}

func TestComputeContentChecksum_DeterministicAfterSort(t *testing.T) {
	config := "global\n    daemon\n"

	// Create two AuxiliaryFiles in different insertion orders, then Sort both.
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

	auxFiles1.Sort()
	auxFiles2.Sort()

	checksum1 := ComputeContentChecksum(config, auxFiles1)
	checksum2 := ComputeContentChecksum(config, auxFiles2)

	assert.Equal(t, checksum1, checksum2, "Checksums should be identical after Sort regardless of original order")
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
	auxFiles.Sort()

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
