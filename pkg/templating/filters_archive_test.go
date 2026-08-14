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

// tarEntry is one file to place in a synthetic archive.
type tarEntry struct {
	name     string
	content  string
	typeflag byte
}

// makeTarGz builds a gzip-compressed tar in memory. A zero typeflag means a
// regular file.
func makeTarGz(t *testing.T, entries ...tarEntry) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		flag := e.typeflag
		if flag == 0 {
			flag = tar.TypeReg
		}
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     e.name,
			Mode:     0o600,
			Size:     int64(len(e.content)),
			Typeflag: flag,
		}))
		_, err := tw.Write([]byte(e.content))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.String()
}

func TestUntarGz_ExpandsEntries(t *testing.T) {
	archive := makeTarGz(t,
		tarEntry{name: "coreruleset-4.25.0/crs-setup.conf.example", content: "SecAction id:900000\n"},
		tarEntry{name: "coreruleset-4.25.0/rules/REQUEST-901-INIT.conf", content: "SecAction id:901000\n"},
		tarEntry{name: "coreruleset-4.25.0/rules/lfi-os-files.data", content: "/etc/passwd\n"},
	)

	files, err := scriggoUntarGz(archive)

	require.NoError(t, err)
	require.Len(t, files, 3)
	assert.Equal(t, "SecAction id:901000\n", files["coreruleset-4.25.0/rules/REQUEST-901-INIT.conf"])
	// The .data files CRS rules reference via @pmFromFile must survive
	// alongside the .conf files — concatenating rules would drop them.
	assert.Equal(t, "/etc/passwd\n", files["coreruleset-4.25.0/rules/lfi-os-files.data"])
}

// Keys keep the release tarball's version directory, so a glob is what selects
// entries. Pins the documented interop with keys() + glob_match().
func TestUntarGz_KeysAreVerbatimAndGlobbable(t *testing.T) {
	archive := makeTarGz(t,
		tarEntry{name: "coreruleset-4.25.0/README.md", content: "readme"},
		tarEntry{name: "coreruleset-4.25.0/rules/REQUEST-901-INIT.conf", content: "a"},
		tarEntry{name: "coreruleset-4.25.0/rules/REQUEST-905-COMMON.conf", content: "b"},
	)

	files, err := scriggoUntarGz(archive)
	require.NoError(t, err)

	names := globMatchStrings(scriggoKeys(files), "*/rules/*.conf")

	// Sorted by keys(), and CRS's numeric prefixes make that the load order.
	assert.Equal(t, []string{
		"coreruleset-4.25.0/rules/REQUEST-901-INIT.conf",
		"coreruleset-4.25.0/rules/REQUEST-905-COMMON.conf",
	}, names)
}

func TestUntarGz_SkipsNonRegularEntries(t *testing.T) {
	archive := makeTarGz(t,
		tarEntry{name: "root/", typeflag: tar.TypeDir},
		tarEntry{name: "root/link", content: "", typeflag: tar.TypeSymlink},
		tarEntry{name: "root/real.conf", content: "SecRuleEngine On\n"},
	)

	files, err := scriggoUntarGz(archive)

	require.NoError(t, err)
	require.Len(t, files, 1, "only regular files carry content a caller can use")
	assert.Contains(t, files, "root/real.conf")
}

// Every failure must yield a nil map, never the entries read so far: a
// partially expanded ruleset renders, deploys and validates exactly like a
// complete one, so the caller has to be able to tell them apart.
func TestUntarGz_RejectsBadArchives(t *testing.T) {
	good := makeTarGz(t, tarEntry{name: "a.conf", content: "x"})

	tests := []struct {
		name    string
		archive string
		wantErr string
	}{
		{
			name:    "empty input (a failed non-critical http.Fetch)",
			archive: "",
			wantErr: "empty archive",
		},
		{
			name:    "not gzip at all",
			archive: "SecRuleEngine On\n",
			wantErr: "not a gzip stream",
		},
		{
			name:    "truncated gzip stream",
			archive: good[:len(good)/2],
			wantErr: "untar_gz:",
		},
		{
			name:    "path traversal",
			archive: makeTarGz(t, tarEntry{name: "../../etc/passwd", content: "x"}),
			wantErr: "escapes the base directory",
		},
		{
			name:    "traversal hidden mid-path",
			archive: makeTarGz(t, tarEntry{name: "rules/../../../etc/passwd", content: "x"}),
			wantErr: "escapes the base directory",
		},
		{
			name:    "absolute path",
			archive: makeTarGz(t, tarEntry{name: "/etc/passwd", content: "x"}),
			wantErr: "absolute path",
		},
		{
			name:    "empty entry name",
			archive: makeTarGz(t, tarEntry{name: "", content: "x"}),
			wantErr: "empty name",
		},
		{
			name: "duplicate entry",
			archive: makeTarGz(t,
				tarEntry{name: "rules/a.conf", content: "first"},
				tarEntry{name: "rules/a.conf", content: "second"},
			),
			wantErr: "twice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, err := scriggoUntarGz(tt.archive)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Nil(t, files, "a failed expansion must return no entries, not a partial set")
		})
	}
}

// The guards bound what an attacker-influenced archive can allocate. Driven
// through untarGz with tiny limits so the test doesn't have to build a real
// decompression bomb.
func TestUntarGz_EnforcesLimits(t *testing.T) {
	tests := []struct {
		name    string
		archive string
		limits  archiveLimits
		wantErr string
	}{
		{
			name: "too many entries",
			archive: makeTarGz(t,
				tarEntry{name: "a", content: "1"},
				tarEntry{name: "b", content: "2"},
				tarEntry{name: "c", content: "3"},
			),
			limits:  archiveLimits{maxEntries: 2, maxEntryBytes: 1024, maxTotalBytes: 1024},
			wantErr: "more than 2 entries",
		},
		{
			// The entry budget has to bound the LOOP, not just the map. Headers
			// for links and directories carry no content, so counting only what
			// is kept lets an archive of millions of them spin the parser while
			// adding nothing to the map or the byte total — and they compress to
			// almost nothing, so the input stays small. Same bomb, spent on CPU.
			name: "too many non-regular entries",
			archive: makeTarGz(t,
				tarEntry{name: "d1/", typeflag: tar.TypeDir},
				tarEntry{name: "d2/", typeflag: tar.TypeDir},
				tarEntry{name: "l1", typeflag: tar.TypeSymlink},
				tarEntry{name: "l2", typeflag: tar.TypeSymlink},
			),
			limits:  archiveLimits{maxEntries: 2, maxEntryBytes: 1024, maxTotalBytes: 1024},
			wantErr: "more than 2 entries",
		},
		{
			name:    "single entry too large",
			archive: makeTarGz(t, tarEntry{name: "big", content: strings.Repeat("x", 100)}),
			limits:  archiveLimits{maxEntries: 10, maxEntryBytes: 50, maxTotalBytes: 1024},
			wantErr: "larger than 50 bytes",
		},
		{
			name: "expanded total too large",
			archive: makeTarGz(t,
				tarEntry{name: "a", content: strings.Repeat("x", 40)},
				tarEntry{name: "b", content: strings.Repeat("y", 40)},
			),
			limits:  archiveLimits{maxEntries: 10, maxEntryBytes: 50, maxTotalBytes: 60},
			wantErr: "expands to more than 60 bytes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, err := untarGz(tt.archive, tt.limits)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Nil(t, files)
		})
	}
}

// A header claiming a smaller size than the bytes that follow must not get
// past the per-entry guard: Size is attacker-controlled, the reader is not.
func TestUntarGz_LyingHeaderSizeCannotBypassLimit(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := strings.Repeat("x", 100)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "big",
		Mode:     0o600,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}))
	_, err := tw.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())

	files, err := untarGz(buf.String(), archiveLimits{maxEntries: 10, maxEntryBytes: 10, maxTotalBytes: 1024})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "larger than 10 bytes")
	assert.Nil(t, files)
}

// countingReader records how many bytes were actually pulled from the source.
type countingReader struct {
	r    io.Reader
	read int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += int64(n)
	return n, err
}

// The per-entry guard has to bound the READ, not just reject the result. A
// version that copies the whole entry and then compares its length returns the
// same error while allocating everything a decompression bomb asked it to —
// which is the failure the limit exists to prevent, and it looks identical
// from the outside.
func TestReadArchiveEntry_StopsReadingAtTheLimit(t *testing.T) {
	const limit = 10
	src := &countingReader{r: strings.NewReader(strings.Repeat("x", 1<<20))}

	_, err := readArchiveEntry(src, "big", limit)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "larger than 10 bytes")
	assert.LessOrEqual(t, src.read, int64(limit+1),
		"the entry must be read through a bounded reader; reading it all and checking the length afterwards allocates exactly what the limit is meant to refuse")
}

// The whole point of the error return: a template can recover instead of the
// render dying. Pins that the function is reachable from a template and that a
// bad archive leaves the render intact.
func TestUntarGz_TemplateRecoversFromBadArchive(t *testing.T) {
	engine, err := New(map[string]string{
		"test": `{%- var files, err = untar_gz("not a gzip") %}` +
			`{%- if err != nil %}fallback{%- else %}{{ len(files) }}{%- end %}`,
	}, nil)
	require.NoError(t, err)

	out, err := engine.Render(t.Context(), "test", map[string]any{})

	require.NoError(t, err, "a bad archive must not fail the render")
	assert.Equal(t, "fallback", strings.TrimSpace(out))
}

func TestUntarGz_TemplateExpandsGoodArchive(t *testing.T) {
	archive := makeTarGz(t,
		tarEntry{name: "rules/a.conf", content: "SecAction id:1\n"},
		tarEntry{name: "rules/b.conf", content: "SecAction id:2\n"},
	)

	engine, err := New(map[string]string{
		"test": `{%- var files, err = untar_gz(archive) %}` +
			`{%- if err != nil %}ERR{%- end %}` +
			`{%- for _, n := range keys(files) | glob_match("rules/*.conf") %}{{ files[n] }}{%- end %}`,
	}, &Options{Declarations: map[string]any{"archive": (*string)(nil)}})
	require.NoError(t, err)

	out, err := engine.Render(t.Context(), "test", map[string]any{"archive": archive})

	require.NoError(t, err)
	assert.Equal(t, "SecAction id:1\nSecAction id:2", strings.TrimSpace(out))
}
