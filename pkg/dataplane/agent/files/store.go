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
	"slices"
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
	// reserved are absolute paths inside the tree that no manifest may name:
	// the HAProxy runtime sockets, which a write would replace with a file.
	reserved map[string]struct{}

	// rename is os.Rename in production; tests replace it to exercise the
	// cross-device fallback that the mount probe is supposed to make dead code.
	rename          func(oldpath, newpath string) error
	crossDeviceCopy atomic.Uint64
}

// NewStore probes the mounts under baseDir and prepares each one's temp and
// LKG directory. reserved names the paths inside the tree that belong to
// something other than the manifest, which is the HAProxy sockets.
func NewStore(baseDir string, logger *slog.Logger, reserved ...string) (*Store, error) {
	// The haproxytech images ship /etc/haproxy as a symlink; walk the target.
	abs, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		return nil, fmt.Errorf("resolve base dir %q: %w", baseDir, err)
	}
	if abs, err = filepath.Abs(abs); err != nil {
		return nil, fmt.Errorf("resolve base dir %q: %w", baseDir, err)
	}
	mounts, err := probeMounts(abs)
	if err != nil {
		return nil, err
	}
	s := &Store{
		baseDir:  abs,
		mounts:   mounts,
		logger:   logger,
		reserved: resolveAll(reserved),
		rename:   os.Rename,
	}
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
	abs := filepath.Join(s.baseDir, filepath.FromSlash(rel))
	if _, taken := s.reserved[abs]; taken {
		return "", fmt.Errorf("%w: %q is a reserved path", ErrInvalidPath, rel)
	}
	return abs, nil
}

// resolveAll canonicalises the reserved paths the same way the base directory
// is, so a symlinked mount cannot spell its way past them. The paths need not
// exist: HAProxy binds its sockets after the agent starts.
func resolveAll(paths []string) map[string]struct{} {
	out := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		dir, err := filepath.EvalSymlinks(filepath.Dir(abs))
		if err == nil {
			abs = filepath.Join(dir, filepath.Base(abs))
		}
		out[abs] = struct{}{}
	}
	return out
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

// probeMounts records the base directory and every mount point below it as
// a Mount. Mount points, not devices, are what confine hardlinks and renames:
// two bind mounts of one filesystem share st_dev and still refuse link(2).
func probeMounts(root string) ([]Mount, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("base dir %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("base dir %s is not a directory", root)
	}
	points, err := mountPointsUnder(root)
	if err != nil {
		return nil, fmt.Errorf("probe mounts under %s: %w", root, err)
	}
	roots := append([]string{root}, points...)
	slices.Sort(roots)
	roots = slices.Compact(roots)
	mounts := make([]Mount, 0, len(roots))
	for _, dir := range roots {
		dev, err := deviceOf(dir)
		if err != nil {
			return nil, fmt.Errorf("probe mounts under %s: %w", root, err)
		}
		mounts = append(mounts, Mount{Root: dir, Device: dev})
	}
	sort.Slice(mounts, func(i, j int) bool { return len(mounts[i].Root) > len(mounts[j].Root) })
	return mounts, nil
}
