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
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir(), slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	return s
}

// The haproxytech images ship /etc/haproxy as a symlink to
// /usr/local/etc/haproxy; the store must own the target, not refuse the link.
func TestNewStoreFollowsASymlinkedBaseDir(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "etc-haproxy")
	require.NoError(t, os.Symlink(target, link))

	s, err := NewStore(link, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	resolved, err := filepath.EvalSymlinks(target)
	require.NoError(t, err)
	assert.Equal(t, resolved, s.BaseDir())
	assert.NotEmpty(t, s.Mounts())
}

func stage(t *testing.T, s *Store, rel, content string) *Staged {
	t.Helper()
	staged, err := s.Stage(rel, strings.NewReader(content), renderplan.DigestString(content), int64(len(content)))
	require.NoError(t, err)
	return staged
}

func readFile(t *testing.T, s *Store, rel string) string {
	t.Helper()
	abs, err := s.Abs(rel)
	require.NoError(t, err)
	b, err := os.ReadFile(abs)
	require.NoError(t, err)
	return string(b)
}

func TestValidatePath(t *testing.T) {
	tests := []struct {
		name string
		rel  string
		ok   bool
	}{
		{"plain file", "haproxy.cfg", true},
		{"nested", "maps/host.map", true},
		{"empty", "", false},
		{"absolute", "/etc/haproxy/haproxy.cfg", false},
		{"escape", "../secret", false},
		{"embedded escape", "maps/../../secret", false},
		{"non canonical", "./haproxy.cfg", false},
		{"trailing slash", "maps/", false},
		{"double slash", "maps//a.map", false},
		{"agent state file", ".haptic-agent.json", false},
		{"lkg dir", ".haptic-lkg/x", false},
		{"dot component", "maps/.hidden/a.map", false},
		{"nul", "map\x00s", false},
		{"too long", strings.Repeat("a", 256), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePath(tc.rel)
			if tc.ok {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, ErrInvalidPath)
		})
	}
}

func TestStageVerifiesDigestAndSize(t *testing.T) {
	s := newTestStore(t)

	_, err := s.Stage("a.map", strings.NewReader("hello"), renderplan.DigestString("goodbye"), 5)
	assert.ErrorIs(t, err, ErrDigestMismatch)

	_, err = s.Stage("a.map", strings.NewReader("hello"), renderplan.DigestString("hello"), 4)
	assert.ErrorIs(t, err, ErrDigestMismatch)

	_, err = s.Stage("../escape", strings.NewReader("x"), renderplan.DigestString("x"), 1)
	assert.ErrorIs(t, err, ErrInvalidPath)
}

func TestStageLeavesNoTempFileBehind(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Stage("a.map", strings.NewReader("hello"), renderplan.DigestString("nope"), 5)
	require.Error(t, err)

	entries, err := os.ReadDir(filepath.Join(s.Mounts()[0].Root, TempDirName))
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestTransactionWritesConfigLast(t *testing.T) {
	s := newTestStore(t)
	var order []string
	s.rename = func(oldpath, newpath string) error {
		order = append(order, filepath.Base(newpath))
		return os.Rename(oldpath, newpath)
	}

	j := &Journal{}
	tx := s.Begin(j, "haproxy.cfg")
	tx.Install(stage(t, s, "haproxy.cfg", "global\n"))
	tx.Install(stage(t, s, "maps/host.map", "a b\n"))
	require.NoError(t, tx.Backup())
	require.NoError(t, tx.Write())

	assert.Equal(t, []string{"host.map", "haproxy.cfg"}, order)
	assert.Equal(t, "global\n", readFile(t, s, "haproxy.cfg"))
}

func TestJournalKinds(t *testing.T) {
	s := newTestStore(t)
	j := &Journal{}

	first := s.Begin(j, "haproxy.cfg")
	first.Install(stage(t, s, "haproxy.cfg", "v1\n"))
	first.Install(stage(t, s, "maps/gone.map", "old\n"))
	require.NoError(t, first.Backup())
	require.NoError(t, first.Write())
	assert.Equal(t, []Entry{
		{Path: "haproxy.cfg", Kind: KindCreated},
		{Path: "maps/gone.map", Kind: KindCreated},
	}, j.Entries)

	require.NoError(t, s.ClearJournal(j))
	assert.True(t, j.Empty())

	second := s.Begin(j, "haproxy.cfg")
	second.Install(stage(t, s, "haproxy.cfg", "v2\n"))
	second.Install(stage(t, s, "maps/new.map", "new\n"))
	second.Delete("maps/gone.map")
	require.NoError(t, second.Backup())
	require.NoError(t, second.Write())

	kinds := map[string]EntryKind{}
	for _, e := range j.Entries {
		kinds[e.Path] = e.Kind
	}
	assert.Equal(t, map[string]EntryKind{
		"haproxy.cfg":   KindModified,
		"maps/new.map":  KindCreated,
		"maps/gone.map": KindDeleted,
	}, kinds)
}

func TestRestoreReturnsTheLastKnownGoodSet(t *testing.T) {
	s := newTestStore(t)
	j := &Journal{}

	good := s.Begin(j, "haproxy.cfg")
	good.Install(stage(t, s, "haproxy.cfg", "good\n"))
	good.Install(stage(t, s, "maps/keep.map", "keep\n"))
	require.NoError(t, good.Backup())
	require.NoError(t, good.Write())
	require.NoError(t, s.ClearJournal(j))

	bad := s.Begin(j, "haproxy.cfg")
	bad.Install(stage(t, s, "haproxy.cfg", "bad\n"))
	bad.Install(stage(t, s, "maps/extra.map", "extra\n"))
	bad.Delete("maps/keep.map")
	require.NoError(t, bad.Backup())
	require.NoError(t, bad.Write())

	require.NoError(t, s.Restore(j, "haproxy.cfg"))

	assert.Equal(t, "good\n", readFile(t, s, "haproxy.cfg"))
	assert.Equal(t, "keep\n", readFile(t, s, "maps/keep.map"))
	extra, err := s.Abs("maps/extra.map")
	require.NoError(t, err)
	_, err = os.Lstat(extra)
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

// A restore whose backup is gone must leave the path as it is. Removing it
// first and failing to link would leave the tree neither the desired set nor
// the last known good one.
func TestRestoreLeavesThePathWhenTheBackupIsGone(t *testing.T) {
	s := newTestStore(t)
	j := &Journal{}

	good := s.Begin(j, "haproxy.cfg")
	good.Install(stage(t, s, "haproxy.cfg", "good\n"))
	require.NoError(t, good.Backup())
	require.NoError(t, good.Write())
	require.NoError(t, s.ClearJournal(j))

	bad := s.Begin(j, "haproxy.cfg")
	bad.Install(stage(t, s, "haproxy.cfg", "bad\n"))
	require.NoError(t, bad.Backup())
	require.NoError(t, bad.Write())
	require.Len(t, j.Entries, 1)
	require.NoError(t, os.Remove(j.Entries[0].Backup))

	require.Error(t, s.Restore(j, "haproxy.cfg"))
	assert.Equal(t, "bad\n", readFile(t, s, "haproxy.cfg"), "a failed restore must not delete the path")
}

// Two paths whose digests collide are backed up separately: the journal
// position, not the hash, is what makes a backup name unique.
func TestCollidingPathsKeepSeparateBackups(t *testing.T) {
	s := newTestStore(t)
	j := &Journal{}
	// Both digest to 9ff5793cac578118 under xxhash64.
	first, second := "maps/11c714b2cc3f873f.map", "maps/71b06949baa8f2ff.map"
	require.Equal(t, renderplan.DigestString(first), renderplan.DigestString(second),
		"the fixture no longer collides; pick another pair")

	seed := s.Begin(j, "haproxy.cfg")
	seed.Install(stage(t, s, first, "one\n"))
	seed.Install(stage(t, s, second, "two\n"))
	require.NoError(t, seed.Backup())
	require.NoError(t, seed.Write())
	require.NoError(t, s.ClearJournal(j))

	change := s.Begin(j, "haproxy.cfg")
	change.Install(stage(t, s, first, "one-changed\n"))
	change.Install(stage(t, s, second, "two-changed\n"))
	require.NoError(t, change.Backup())
	require.NoError(t, change.Write())
	require.NoError(t, s.Restore(j, "haproxy.cfg"))

	assert.Equal(t, "one\n", readFile(t, s, first))
	assert.Equal(t, "two\n", readFile(t, s, second))
}

// The manifest owns files, not the sockets the agent itself talks to: writing
// a regular file over the worker socket would cut the agent off from HAProxy.
func TestAReservedPathIsRefused(t *testing.T) {
	base := t.TempDir()
	s, err := NewStore(base, slog.New(slog.DiscardHandler), filepath.Join(base, "haproxy-worker.sock"))
	require.NoError(t, err)

	_, err = s.Abs("haproxy-worker.sock")
	assert.ErrorIs(t, err, ErrInvalidPath)
	_, err = s.Abs("haproxy.cfg")
	assert.NoError(t, err)
}

// Anything that is not a regular file at a manifest path belongs to something
// else, so the agent refuses the write instead of replacing it.
func TestInstallRefusesANonRegularPath(t *testing.T) {
	s := newTestStore(t)
	abs, err := s.Abs("general/socket")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	listener, err := net.Listen("unix", abs)
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()

	tx := s.Begin(&Journal{}, "haproxy.cfg")
	tx.Install(stage(t, s, "general/socket", "content\n"))
	require.NoError(t, tx.Backup())

	assert.ErrorIs(t, tx.Write(), ErrInvalidPath)
	info, err := os.Lstat(abs)
	require.NoError(t, err)
	assert.Equal(t, fs.ModeSocket, info.Mode()&fs.ModeType, "the socket is still a socket")
}

func TestFirstJournalEntryPerPathWins(t *testing.T) {
	s := newTestStore(t)
	j := &Journal{}

	initial := s.Begin(j, "haproxy.cfg")
	initial.Install(stage(t, s, "haproxy.cfg", "lkg\n"))
	require.NoError(t, initial.Backup())
	require.NoError(t, initial.Write())
	require.NoError(t, s.ClearJournal(j))

	for _, content := range []string{"v2\n", "v3\n"} {
		tx := s.Begin(j, "haproxy.cfg")
		tx.Install(stage(t, s, "haproxy.cfg", content))
		require.NoError(t, tx.Backup())
		require.NoError(t, tx.Write())
	}
	require.Len(t, j.Entries, 1)

	require.NoError(t, s.Restore(j, "haproxy.cfg"))
	assert.Equal(t, "lkg\n", readFile(t, s, "haproxy.cfg"))
}

func TestPerMountTempAndBackupDirectories(t *testing.T) {
	base := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base, "general"), 0o755))
	s, err := NewStore(base, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	// One tmpfs makes every directory the same device; forcing the split is the
	// only way to exercise the per-mount rule without a second filesystem.
	s.mounts = []Mount{
		{Root: filepath.Join(base, "general"), Device: 2},
		{Root: base, Device: 1},
	}
	for _, m := range s.mounts {
		require.NoError(t, os.MkdirAll(filepath.Join(m.Root, TempDirName), 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(m.Root, LKGDirName), 0o755))
	}

	staged := stage(t, s, "general/error.http", "HTTP/1.0 503\n")
	assert.Equal(t, filepath.Join(base, "general", TempDirName), filepath.Dir(staged.tmp))

	j := &Journal{}
	tx := s.Begin(j, "haproxy.cfg")
	tx.Install(staged)
	require.NoError(t, tx.Backup())
	require.NoError(t, tx.Write())
	require.NoError(t, s.ClearJournal(j))

	second := s.Begin(j, "haproxy.cfg")
	second.Install(stage(t, s, "general/error.http", "HTTP/1.0 500\n"))
	require.NoError(t, second.Backup())
	require.NoError(t, second.Write())
	require.Len(t, j.Entries, 1)
	assert.Equal(t, filepath.Join(base, "general", LKGDirName), filepath.Dir(j.Entries[0].Backup))
}

func TestCrossDeviceRenameFallsBackToCopy(t *testing.T) {
	s := newTestStore(t)
	s.rename = func(_, _ string) error {
		return &os.LinkError{Op: "rename", Err: syscall.EXDEV}
	}

	j := &Journal{}
	tx := s.Begin(j, "haproxy.cfg")
	tx.Install(stage(t, s, "haproxy.cfg", "global\n"))
	require.NoError(t, tx.Backup())
	require.NoError(t, tx.Write())

	assert.Equal(t, "global\n", readFile(t, s, "haproxy.cfg"))
	assert.Equal(t, uint64(1), s.CrossDeviceCopies())
}

func TestHashTreeReportsOnlyExistingPaths(t *testing.T) {
	s := newTestStore(t)
	j := &Journal{}
	tx := s.Begin(j, "haproxy.cfg")
	tx.Install(stage(t, s, "haproxy.cfg", "global\n"))
	require.NoError(t, tx.Backup())
	require.NoError(t, tx.Write())

	tree, err := s.HashTree([]string{"haproxy.cfg", "maps/absent.map"})
	require.NoError(t, err)
	require.Contains(t, tree, "haproxy.cfg")
	assert.Equal(t, renderplan.DigestString("global\n"), tree["haproxy.cfg"].Digest)
	assert.Equal(t, int64(7), tree["haproxy.cfg"].Size)
	assert.NotContains(t, tree, "maps/absent.map")
}

func TestHashTreeSkipsWhatIsNotAFileTheAgentWrote(t *testing.T) {
	s := newTestStore(t)
	abs, err := s.Abs("maps")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(abs, 0o755))

	tree, err := s.HashTree([]string{"maps"})
	require.NoError(t, err)
	assert.NotContains(t, tree, "maps")

	_, err = s.Digest("maps")
	assert.ErrorIs(t, err, ErrInvalidPath, "a single-path read still names the problem")
}

func TestSweepTempRemovesCrashLeftovers(t *testing.T) {
	s := newTestStore(t)
	stray := filepath.Join(s.Mounts()[0].Root, TempDirName, "part-stray")
	require.NoError(t, os.WriteFile(stray, []byte("x"), 0o600))

	require.NoError(t, s.SweepTemp())
	_, err := os.Lstat(stray)
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestStageRejectsOversizedPart(t *testing.T) {
	s := newTestStore(t)
	body := strings.Repeat("x", 32)
	_, err := s.Stage("a.map", strings.NewReader(body), renderplan.DigestString(body[:16]), 16)
	assert.ErrorIs(t, err, ErrDigestMismatch)
}

func TestDiscardLeavesTheTreeUntouched(t *testing.T) {
	s := newTestStore(t)
	j := &Journal{}
	tx := s.Begin(j, "haproxy.cfg")
	tx.Install(stage(t, s, "haproxy.cfg", "global\n"))
	tx.Discard()

	_, err := s.Digest("haproxy.cfg")
	assert.ErrorIs(t, err, fs.ErrNotExist)
	assert.True(t, j.Empty())
}

func TestStageReportsReadFailure(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Stage("a.map", failingReader{}, renderplan.DigestString("x"), 1)
	require.Error(t, err)
	assert.ErrorIs(t, err, errReader)
}

var errReader = errors.New("reader blew up")

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errReader }

var _ io.Reader = failingReader{}
