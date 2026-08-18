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
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// ErrDigestMismatch is what a received part failing its manifest digest wraps;
// it is a refusal, never a retry.
var ErrDigestMismatch = errors.New("part does not match its manifest digest")

// Staged is a verified part waiting in its target mount's temp directory.
type Staged struct {
	Rel  string
	tmp  string
	size int64
}

// Size is the verified byte count of the staged content.
func (s *Staged) Size() int64 { return s.size }

// Stage streams a received part into the temp directory of the mount that will
// hold it and verifies it against the manifest digest and size before the
// content can ever reach the tree.
func (s *Store) Stage(rel string, r io.Reader, digest string, size int64) (*Staged, error) {
	abs, err := s.Abs(rel)
	if err != nil {
		return nil, err
	}
	m := s.mountFor(abs)
	f, err := os.CreateTemp(filepath.Join(m.Root, TempDirName), "part-")
	if err != nil {
		return nil, fmt.Errorf("stage %q: %w", rel, err)
	}
	written, copyErr := io.Copy(f, io.LimitReader(r, size+1))
	closeErr := f.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		_ = os.Remove(f.Name())
		return nil, fmt.Errorf("stage %q: %w", rel, err)
	}
	staged := &Staged{Rel: rel, tmp: f.Name(), size: written}
	if err := s.verify(staged, digest, size); err != nil {
		staged.Discard()
		return nil, err
	}
	if err := os.Chmod(staged.tmp, filePerm); err != nil {
		staged.Discard()
		return nil, fmt.Errorf("stage %q: %w", rel, err)
	}
	return staged, nil
}

func (s *Store) verify(staged *Staged, digest string, size int64) error {
	if staged.size != size {
		return fmt.Errorf("%w: %q is %d bytes, manifest says %d", ErrDigestMismatch, staged.Rel, staged.size, size)
	}
	content, err := os.ReadFile(filepath.Clean(staged.tmp))
	if err != nil {
		return fmt.Errorf("verify %q: %w", staged.Rel, err)
	}
	if got := renderplan.Digest(content); got != digest {
		return fmt.Errorf("%w: %q hashes to %s, manifest says %s", ErrDigestMismatch, staged.Rel, got, digest)
	}
	return nil
}

// Discard drops a staged part without touching the tree.
func (s *Staged) Discard() {
	if s.tmp != "" {
		_ = os.Remove(s.tmp)
		s.tmp = ""
	}
}

// install moves a staged part onto its manifest path.
func (s *Store) install(staged *Staged) error {
	abs, err := s.Abs(staged.Rel)
	if err != nil {
		return err
	}
	if err := refuseNonRegular(abs); err != nil {
		return fmt.Errorf("install %q: %w", staged.Rel, err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), dirPerm); err != nil {
		return fmt.Errorf("install %q: %w", staged.Rel, err)
	}
	err = s.rename(staged.tmp, abs)
	if errors.Is(err, syscall.EXDEV) {
		s.crossDeviceCopy.Add(1)
		s.logger.Warn("staged part crossed a mount boundary", "path", staged.Rel)
		err = s.copyOnto(staged.tmp, abs)
	}
	if err != nil {
		return fmt.Errorf("install %q: %w", staged.Rel, err)
	}
	staged.tmp = ""
	return nil
}

// copyOnto writes src's content to a sibling of dst and renames it, so a
// reader never observes a partially written file even on the fallback path.
func (s *Store) copyOnto(src, dst string) error {
	content, err := os.ReadFile(filepath.Clean(src))
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".copy-")
	if err != nil {
		return err
	}
	_, writeErr := tmp.Write(content)
	err = errors.Join(writeErr, tmp.Chmod(filePerm), tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Remove(src)
}

// backup records the last-known-good version of rel in the journal. The first
// entry per path wins, so a path changed twice since the LKG keeps the version
// that was good, not the one the previous apply left.
func (s *Store) backup(rel string, j *Journal) error {
	if j.Has(rel) {
		return nil
	}
	abs, err := s.Abs(rel)
	if err != nil {
		return err
	}
	switch _, err := os.Lstat(abs); {
	case errors.Is(err, fs.ErrNotExist):
		j.add(Entry{Path: rel, Kind: KindCreated})
		return nil
	case err != nil:
		return fmt.Errorf("back up %q: %w", rel, err)
	}
	link, err := s.linkAside(rel, abs, j)
	if err != nil {
		return err
	}
	j.add(Entry{Path: rel, Kind: KindModified, Backup: link})
	return nil
}

// backupDeleted keeps the content of a path the manifest no longer names, so a
// rollback can link it back.
func (s *Store) backupDeleted(rel string, j *Journal) error {
	if j.Has(rel) {
		return nil
	}
	abs, err := s.Abs(rel)
	if err != nil {
		return err
	}
	switch _, err := os.Lstat(abs); {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("back up %q: %w", rel, err)
	}
	link, err := s.linkAside(rel, abs, j)
	if err != nil {
		return err
	}
	j.add(Entry{Path: rel, Kind: KindDeleted, Backup: link})
	return nil
}

// unlink drops a path the manifest no longer names.
func (s *Store) unlink(rel string) error {
	abs, err := s.Abs(rel)
	if err != nil {
		return err
	}
	if err := refuseNonRegular(abs); err != nil {
		return fmt.Errorf("delete %q: %w", rel, err)
	}
	if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete %q: %w", rel, err)
	}
	return nil
}

// refuseNonRegular keeps the tree to the files the agent may own. A socket, a
// directory or a symlink at a manifest path belongs to something else, and
// replacing it would take that something else away.
func refuseNonRegular(abs string) error {
	info, err := os.Lstat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %q is not a regular file", ErrInvalidPath, abs)
	}
	return nil
}

// linkAside hardlinks a file into its own mount's LKG directory. The journal
// position makes the name unique within the journal, so two paths that hash
// alike keep their own backup; only a leftover from a cleared journal can be
// at that name, and the caller's j.Has check keeps a live one out of reach.
func (s *Store) linkAside(rel, abs string, j *Journal) (string, error) {
	m := s.mountFor(abs)
	name := fmt.Sprintf("%d-%s.bak", len(j.Entries), renderplan.DigestString(rel))
	link := filepath.Join(m.Root, LKGDirName, name)
	if err := os.Remove(link); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("back up %q: %w", rel, err)
	}
	if err := os.Link(abs, link); err != nil {
		return "", fmt.Errorf("back up %q: %w", rel, err)
	}
	return link, nil
}

// Restore puts the last-known-good version of every journalled path back. The
// config file goes last so HAProxy is never told to read a config whose
// auxiliary files have not been restored yet.
func (s *Store) Restore(j *Journal, configRel string) error {
	var deferred *Entry
	var errs []error
	for i := range j.Entries {
		e := j.Entries[i]
		if e.Path == configRel {
			deferred = &e
			continue
		}
		errs = append(errs, s.restoreEntry(e))
	}
	if deferred != nil {
		errs = append(errs, s.restoreEntry(*deferred))
	}
	return errors.Join(errs...)
}

// restoreEntry puts one path back. The backup is linked beside the path and
// renamed onto it, so a restore that fails leaves the old content in place
// instead of leaving the path missing.
func (s *Store) restoreEntry(e Entry) error {
	abs, err := s.Abs(e.Path)
	if err != nil {
		return err
	}
	if e.Kind == KindCreated {
		if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("restore %q: %w", e.Path, err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), dirPerm); err != nil {
		return fmt.Errorf("restore %q: %w", e.Path, err)
	}
	staging := filepath.Join(filepath.Dir(abs), ".haptic-restore-"+filepath.Base(abs))
	if err := os.Remove(staging); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("restore %q: %w", e.Path, err)
	}
	if err := os.Link(e.Backup, staging); err != nil {
		return fmt.Errorf("restore %q: %w", e.Path, err)
	}
	if err := os.Rename(staging, abs); err != nil {
		_ = os.Remove(staging)
		return fmt.Errorf("restore %q: %w", e.Path, err)
	}
	return nil
}

// ClearJournal drops the backups of a set that has become the last known good.
func (s *Store) ClearJournal(j *Journal) error {
	var errs []error
	for _, e := range j.Entries {
		if e.Backup == "" {
			continue
		}
		if err := os.Remove(e.Backup); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("clear backup of %q: %w", e.Path, err))
		}
	}
	j.Entries = nil
	return errors.Join(errs...)
}

// SweepTemp removes temp files a crash left behind. Bounded by the directory
// listing it reads.
func (s *Store) SweepTemp() error {
	var errs []error
	for _, m := range s.mounts {
		dir := filepath.Join(m.Root, TempDirName)
		entries, err := os.ReadDir(dir)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if len(entries) > api.MaxFiles {
			entries = entries[:api.MaxFiles]
		}
		for _, e := range entries {
			errs = append(errs, os.Remove(filepath.Join(dir, e.Name())))
		}
	}
	return errors.Join(errs...)
}
