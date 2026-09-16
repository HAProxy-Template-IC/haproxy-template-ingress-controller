// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package templating

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUntarGzChecksTrailer(t *testing.T) {
	good := makeTarGz(t, tarEntry{name: "rules.conf", content: "complete"})
	corrupt := []byte(good)
	corrupt[len(corrupt)-8] ^= 0xff
	for name, archive := range map[string]string{
		"checksum":          string(corrupt),
		"missing trailer":   good[:len(good)-8],
		"truncated trailer": good[:len(good)-1],
	} {
		t.Run(name, func(t *testing.T) {
			files, err := scriggoUntarGz(archive)
			require.Error(t, err)
			assert.Nil(t, files)
		})
	}
}

func TestUntarGzBoundsSkippedContent(t *testing.T) {
	archive := makeTarGz(t,
		tarEntry{name: "rules.conf", content: "complete"},
		tarEntry{name: "ignored", content: strings.Repeat("x", 4096), typeflag: 'Z'},
	)
	limits := defaultArchiveLimits()
	limits.maxStreamBytes = 2048
	files, err := untarGz(archive, limits)
	require.ErrorContains(t, err, "decompressed stream")
	assert.Nil(t, files)
}

func TestUntarGzBoundsExtendedHeaders(t *testing.T) {
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: "rules.conf", Mode: 0o600, Size: 1,
		PAXRecords: map[string]string{"comment": strings.Repeat("x", 4096)},
		Format:     tar.FormatPAX,
	}))
	_, err := tw.Write([]byte("x"))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	limits := defaultArchiveLimits()
	limits.maxStreamBytes = 2048
	files, err := untarGz(archive.String(), limits)
	require.ErrorContains(t, err, "decompressed stream")
	assert.Nil(t, files)
}

func TestUntarGzStreamSizeBoundary(t *testing.T) {
	archive := makeTarGz(t, tarEntry{name: "rules.conf", content: "complete"})
	gz, err := gzip.NewReader(strings.NewReader(archive))
	require.NoError(t, err)
	raw, err := io.ReadAll(gz)
	require.NoError(t, err)
	require.NoError(t, gz.Close())

	limits := defaultArchiveLimits()
	limits.maxStreamBytes = int64(len(raw))
	files, err := untarGz(archive, limits)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"rules.conf": "complete"}, files)

	limits.maxStreamBytes--
	files, err = untarGz(archive, limits)
	require.ErrorContains(t, err, "decompressed stream")
	assert.Nil(t, files)
}

func TestUntarGzBoundsDataAfterTarEnd(t *testing.T) {
	archive := makeTarGz(t, tarEntry{name: "rules.conf", content: "complete"})
	var trailing bytes.Buffer
	gz := gzip.NewWriter(&trailing)
	_, err := gz.Write([]byte(strings.Repeat("x", 4096)))
	require.NoError(t, err)
	require.NoError(t, gz.Close())

	limits := defaultArchiveLimits()
	limits.maxStreamBytes = 4096
	files, err := untarGz(archive+trailing.String(), limits)
	require.ErrorContains(t, err, "decompressed stream")
	assert.Nil(t, files)
}
