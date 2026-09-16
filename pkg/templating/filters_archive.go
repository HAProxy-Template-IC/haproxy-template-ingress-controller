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

type archiveLimits struct {
	maxEntries     int
	maxEntryBytes  int64
	maxTotalBytes  int64
	maxStreamBytes int64
}

func defaultArchiveLimits() archiveLimits {
	return archiveLimits{
		maxEntries:     4096,
		maxEntryBytes:  8 << 20,
		maxTotalBytes:  32 << 20,
		maxStreamBytes: 64 << 20,
	}
}

// scriggoUntarGz returns regular files only after the complete archive passes validation.
func scriggoUntarGz(archive string) (map[string]string, error) {
	return untarGz(archive, defaultArchiveLimits())
}

func untarGz(archive string, lim archiveLimits) (map[string]string, error) {
	if archive == "" {
		return nil, errors.New("untar_gz: empty archive")
	}

	gz, err := gzip.NewReader(strings.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("untar_gz: not a gzip stream: %w", err)
	}
	defer gz.Close()

	stream := &io.LimitedReader{R: gz, N: lim.maxStreamBytes + 1}
	files, err := readTarFiles(stream, lim)
	if err == nil {
		// tar EOF can precede the gzip checksum and trailing compressed members.
		if _, drainErr := io.Copy(io.Discard, stream); drainErr != nil {
			err = fmt.Errorf("untar_gz: invalid gzip stream: %w", drainErr)
		}
	}
	if stream.N == 0 {
		return nil, fmt.Errorf("untar_gz: decompressed stream exceeds %d bytes; use a smaller archive", lim.maxStreamBytes)
	}
	if err != nil {
		return nil, err
	}
	return files, nil
}

func readTarFiles(stream io.Reader, lim archiveLimits) (map[string]string, error) {
	files := make(map[string]string)
	var total int64
	var examined int

	tr := tar.NewReader(stream)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("untar_gz: reading archive: %w", err)
		}

		// Skipped entries consume parsing work too.
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
	if err := rejectUncontainedPath("untar_gz: archive entry", name); err != nil {
		return "", err
	}
	return path.Clean(name), nil
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
