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
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// archiveLimits bounds what one archive may expand to. The input is
// attacker-influenced whenever it came off the network, and a few KB of gzip
// expands to gigabytes if nothing stops it.
type archiveLimits struct {
	maxEntries    int
	maxEntryBytes int64
	maxTotalBytes int64
}

// defaultArchiveLimits leaves ~40x headroom over the OWASP CRS release
// (48 files, 812 KB) — the archive this was written for — while keeping a
// decompression bomb to a bounded, recoverable allocation.
var defaultArchiveLimits = archiveLimits{
	maxEntries:    4096,
	maxEntryBytes: 8 << 20,
	maxTotalBytes: 32 << 20,
}

// scriggoUntarGz expands a gzip-compressed tar archive into a map of entry
// path to entry content.
//
// It reports failure through its error return and never panics, so a corrupt
// or hostile archive costs the caller a fallback rather than the whole render.
// Callers decide what to do:
//
//	{%- var files, err = untar_gz(archive) %}
//	{%- if err != nil %}
//	  {#- fall back to a known-good ruleset -#}
//	{%- end %}
//
// Extraction is all-or-nothing: any error returns a nil map, never the entries
// read so far. A partially expanded archive is the dangerous outcome — half a
// WAF ruleset renders, deploys and validates exactly like a whole one, so the
// caller must be able to tell "complete" from "as much as we could get".
//
// Entry paths are returned verbatim, so a release tarball's version directory
// is preserved (`coreruleset-4.25.0/rules/…`). Stripping it would make the
// filter's output depend on whether the archive happens to have a single root.
// Match with a glob instead — `*` does not cross `/`:
//
//	{%- for _, name := range keys(files) | glob_match("*/rules/*.conf") %}
//
// Only regular files are returned; directories, symlinks, hardlinks and
// devices carry no content the caller can use and are skipped.
func scriggoUntarGz(archive string) (map[string]string, error) {
	return untarGz(archive, defaultArchiveLimits)
}

// untarGz is scriggoUntarGz with injectable limits so tests can trip the
// guards without allocating the real ones.
func untarGz(archive string, lim archiveLimits) (map[string]string, error) {
	if archive == "" {
		// The empty string is what http.Fetch returns for a failed
		// non-critical fetch, so this is the common path, not an edge case.
		return nil, errors.New("untar_gz: empty archive")
	}

	gz, err := gzip.NewReader(strings.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("untar_gz: not a gzip stream: %w", err)
	}
	defer gz.Close()

	files := make(map[string]string)
	var total int64
	var examined int

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// Covers a truncated stream, which is where all-or-nothing earns
			// its keep: entries already read are discarded with the error.
			return nil, fmt.Errorf("untar_gz: reading archive: %w", err)
		}

		// Counted before the entry type is considered. Bounding only the
		// entries we keep would leave the loop itself unbounded: headers for
		// directories and links carry no content, so an archive made of
		// millions of them adds nothing to the map or to `total` while still
		// costing a parse each. They compress to almost nothing, which is the
		// same shape as a decompression bomb — it just spends CPU on the
		// render path instead of memory.
		examined++
		if examined > lim.maxEntries {
			return nil, fmt.Errorf("untar_gz: archive has more than %d entries", lim.maxEntries)
		}

		if header.Typeflag != tar.TypeReg {
			continue
		}

		name, err := archiveEntryPath(header.Name)
		if err != nil {
			return nil, err
		}
		if _, seen := files[name]; seen {
			return nil, fmt.Errorf("untar_gz: archive contains %q twice; which one wins is undefined", name)
		}

		content, err := readArchiveEntry(tr, name, lim.maxEntryBytes)
		if err != nil {
			return nil, err
		}
		total += int64(len(content))
		if total > lim.maxTotalBytes {
			return nil, fmt.Errorf("untar_gz: archive expands to more than %d bytes", lim.maxTotalBytes)
		}

		files[name] = content
	}

	return files, nil
}

// archiveEntryPath validates one entry name and returns it cleaned.
//
// A traversal or absolute path fails the whole archive rather than being
// skipped or sanitised: callers write these entries to disk under a name the
// archive chose, and an archive that tries to escape is not one to take the
// rest of on trust.
func archiveEntryPath(name string) (string, error) {
	if name == "" {
		return "", errors.New("untar_gz: archive contains an entry with an empty name")
	}
	if path.IsAbs(name) || strings.HasPrefix(name, `\`) || strings.Contains(name, `:\`) {
		return "", fmt.Errorf("untar_gz: archive entry %q is an absolute path", name)
	}

	cleaned := path.Clean(name)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("untar_gz: archive entry %q escapes the archive root", name)
	}
	return cleaned, nil
}

// readArchiveEntry reads one entry, refusing to allocate past the limit.
// The reader is bounded rather than the header's Size trusted — Size is
// attacker-controlled and need not match the bytes that follow.
func readArchiveEntry(r io.Reader, name string, maxBytes int64) (string, error) {
	var buf strings.Builder
	written, err := io.Copy(&buf, io.LimitReader(r, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("untar_gz: reading entry %q: %w", name, err)
	}
	if written > maxBytes {
		return "", fmt.Errorf("untar_gz: entry %q is larger than %d bytes", name, maxBytes)
	}
	return buf.String(), nil
}
