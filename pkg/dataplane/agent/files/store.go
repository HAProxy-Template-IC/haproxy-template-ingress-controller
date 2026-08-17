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

package files

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// Directory names the agent reserves inside every mount it writes to.
const (
	TempDirName = ".haptic-tmp"
	LKGDirName  = ".haptic-lkg"
)

// dirPerm and filePerm keep the tree readable by the HAProxy container, which
// runs as a different user than the agent in Enterprise images.
const (
	dirPerm  fs.FileMode = 0o755
	filePerm fs.FileMode = 0o644
)

// maxProbeDirs bounds the startup mount walk; a manifest can name at most
// api.MaxFiles paths, so a deeper tree than that is not a tree the agent owns.
const maxProbeDirs = api.MaxFiles

// Mount is one filesystem under the base directory. Hardlinks and renames are
// confined to a single mount, so temp and LKG directories exist per mount.
type Mount struct {
	Root   string
	Device uint64
}

// Store owns the manifest-managed tree under a base directory.
type Store struct {
	baseDir string
	mounts  []Mount
	logger  *slog.Logger

	// rename is os.Rename in production; tests replace it to exercise the
	// cross-device fallback that the mount probe is supposed to make dead code.
	rename          func(oldpath, newpath string) error
	crossDeviceCopy atomic.Uint64
}

// NewStore probes the mounts under baseDir and prepares each one's temp and
// LKG directory.
func NewStore(baseDir string, logger *slog.Logger) (*Store, error) {
	abs, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("resolve base dir %q: %w", baseDir, err)
	}
	mounts, err := probeMounts(abs)
	if err != nil {
		return nil, err
	}
	s := &Store{baseDir: abs, mounts: mounts, logger: logger, rename: os.Rename}
	for _, m := range mounts {
		for _, name := range []string{TempDirName, LKGDirName} {
			if err := os.MkdirAll(filepath.Join(m.Root, name), dirPerm); err != nil {
				return nil, fmt.Errorf("prepare %s in %s: %w", name, m.Root, err)
			}
		}
	}
	return s, nil
}

// BaseDir is the absolute root of the managed tree.
func (s *Store) BaseDir() string { return s.baseDir }

// Mounts lists the probed mounts, deepest root first.
func (s *Store) Mounts() []Mount { return s.mounts }

// CrossDeviceCopies counts renames that fell back to a copy. The mount probe
// is supposed to keep this at zero; the server reports a non-zero count as an
// invariant violation.
func (s *Store) CrossDeviceCopies() uint64 { return s.crossDeviceCopy.Load() }

// Abs turns a validated manifest path into an absolute path inside the tree.
func (s *Store) Abs(rel string) (string, error) {
	if err := ValidatePath(rel); err != nil {
		return "", err
	}
	return filepath.Join(s.baseDir, filepath.FromSlash(rel)), nil
}

// mountFor returns the mount that holds abs, which is the deepest probed root
// that is a prefix of it.
func (s *Store) mountFor(abs string) Mount {
	for _, m := range s.mounts {
		if abs == m.Root || strings.HasPrefix(abs, m.Root+string(os.PathSeparator)) {
			return m
		}
	}
	return s.mounts[len(s.mounts)-1]
}

// Digest hashes one file of the tree.
func (s *Store) Digest(rel string) (api.FileAt, error) {
	abs, err := s.Abs(rel)
	if err != nil {
		return api.FileAt{}, err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return api.FileAt{}, err
	}
	if !info.Mode().IsRegular() {
		return api.FileAt{}, fmt.Errorf("%w: %q is not a regular file", ErrInvalidPath, rel)
	}
	content, err := os.ReadFile(filepath.Clean(abs))
	if err != nil {
		return api.FileAt{}, err
	}
	return api.FileAt{Digest: renderplan.Digest(content), Size: int64(len(content))}, nil
}

// HashTree observes the ownership set on disk. A path that is absent, or that
// something else turned into a directory or a symlink, is not in the result:
// it is not a file the agent wrote, and its absence is what makes a plan id
// unknown after a container restart put the bootstrap config back.
func (s *Store) HashTree(paths []string) (map[string]api.FileAt, error) {
	if len(paths) > api.MaxFiles {
		return nil, fmt.Errorf("ownership set of %d paths exceeds the %d-file limit", len(paths), api.MaxFiles)
	}
	out := make(map[string]api.FileAt, len(paths))
	for _, rel := range paths {
		at, err := s.Digest(rel)
		switch {
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, ErrInvalidPath):
			continue
		case err != nil:
			return nil, fmt.Errorf("hash %q: %w", rel, err)
		}
		out[rel] = at
	}
	return out, nil
}

// probeMounts records one Mount per distinct st_dev under root, keeping the
// shallowest directory of each device as its root.
func probeMounts(root string) ([]Mount, error) {
	byDevice := map[uint64]string{}
	seen := 0
	walk := func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if p != root && strings.HasPrefix(d.Name(), ".") {
			return fs.SkipDir
		}
		seen++
		if seen > maxProbeDirs {
			return fmt.Errorf("mount probe found more than %d directories under %s", maxProbeDirs, root)
		}
		dev, err := deviceOf(p)
		if err != nil {
			return err
		}
		if _, ok := byDevice[dev]; !ok {
			byDevice[dev] = p
		}
		return nil
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		return nil, fmt.Errorf("probe mounts under %s: %w", root, err)
	}
	if len(byDevice) == 0 {
		return nil, fmt.Errorf("base dir %s does not exist", root)
	}
	mounts := make([]Mount, 0, len(byDevice))
	for dev, dir := range byDevice {
		mounts = append(mounts, Mount{Root: dir, Device: dev})
	}
	sort.Slice(mounts, func(i, j int) bool { return len(mounts[i].Root) > len(mounts[j].Root) })
	return mounts, nil
}
