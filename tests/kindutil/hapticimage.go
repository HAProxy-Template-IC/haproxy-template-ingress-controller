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

package kindutil

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DockerTimeout bounds one docker invocation, including a build.
const DockerTimeout = 5 * time.Minute

// ErrAgentUnavailable means the built binary has no `haptic agent` subcommand,
// so a suite that drives one has nothing to drive.
var ErrAgentUnavailable = errors.New("no haptic agent to drive")

// RunDocker runs one docker command with stdin and returns its combined output.
func RunDocker(stdin io.Reader, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DockerTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = stdin
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// DockerUsable returns why docker cannot be driven from here, or nil.
func DockerUsable() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker CLI not on PATH: %w", err)
	}
	if out, err := RunDocker(nil, "version", "--format", "{{.Server.Version}}"); err != nil {
		return fmt.Errorf("docker daemon not reachable: %v (%s)", err, strings.TrimSpace(out))
	}
	return nil
}

// BuildHAProxyAgentImage tags an image that is baseImage plus the haptic
// binary, which lets the agent run as its own container against the same
// mounts — the chart's topology. It verifies `haptic agent --help` in the
// built image, so a binary without the subcommand fails here rather than as a
// crash-looping container.
func BuildHAProxyAgentImage(baseImage, tag string) error {
	binary, err := HapticBinary()
	if err != nil {
		return err
	}
	tarball, err := imageContext(baseImage, binary)
	if err != nil {
		return err
	}
	if out, err := RunDocker(bytes.NewReader(tarball), "build", "-t", tag, "-"); err != nil {
		return fmt.Errorf("docker build: %v\n%s", err, out)
	}
	out, err := RunDocker(nil, "run", "--rm", "--entrypoint", "/usr/local/bin/haptic", tag, "agent", "--help")
	if err != nil {
		_, _ = RunDocker(nil, "image", "rm", "-f", tag)
		return fmt.Errorf("%w: `haptic agent --help` failed — the agent subcommand is not in this build: %v\n%s",
			ErrAgentUnavailable, err, out)
	}
	return nil
}

// RemoveImage drops a tag built for one run.
func RemoveImage(tag string) {
	if tag == "" {
		return
	}
	_, _ = RunDocker(nil, "image", "rm", "-f", tag)
}

// imageContext is a two-entry docker build context: the binary and a
// Dockerfile that lays it into the base image.
func imageContext(baseImage, binary string) ([]byte, error) {
	content, err := os.ReadFile(binary)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", binary, err)
	}
	var buf bytes.Buffer
	archive := tar.NewWriter(&buf)
	entries := []struct {
		name string
		mode int64
		body []byte
	}{
		{"Dockerfile", 0o644, []byte(fmt.Sprintf("FROM %s\nCOPY haptic /usr/local/bin/haptic\n", baseImage))},
		{"haptic", 0o755, content},
	}
	for _, entry := range entries {
		header := &tar.Header{
			Name:    entry.name,
			Mode:    entry.mode,
			Size:    int64(len(entry.body)),
			ModTime: time.Unix(0, 0),
		}
		if err := archive.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err := archive.Write(entry.body); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// HapticBinary resolves a linux binary to run in a container: an explicit
// override, then `make build`'s output while it is newer than every Go source,
// then a fresh build.
func HapticBinary() (string, error) {
	if override := os.Getenv("HAPTIC_BINARY"); override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("HAPTIC_BINARY=%s: %w", override, err)
		}
		return override, nil
	}
	root, err := RepoRoot()
	if err != nil {
		return "", err
	}
	built := filepath.Join(root, "bin", "haptic")
	if fresh, err := isFresh(root, built); err == nil && fresh {
		return built, nil
	}
	return buildHaptic(root)
}

func buildHaptic(root string) (string, error) {
	target := filepath.Join(os.TempDir(), fmt.Sprintf("haptic-test-binary-%d", os.Getpid()))
	ctx, cancel := context.WithTimeout(context.Background(), DockerTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", target, "./cmd/haptic")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build ./cmd/haptic: %v\n%s", err, out)
	}
	return target, nil
}

// isFresh reports whether the built binary is newer than every Go source it is
// built from. A stale binary silently tests code that is not in the tree.
func isFresh(root, binary string) (bool, error) {
	info, err := os.Stat(binary)
	if err != nil {
		return false, err
	}
	built := info.ModTime()
	for _, dir := range []string{"cmd", "pkg"} {
		newer := false
		walkErr := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			source, err := entry.Info()
			if err != nil {
				return err
			}
			if source.ModTime().After(built) {
				newer = true
			}
			return nil
		})
		if walkErr != nil {
			return false, walkErr
		}
		if newer {
			return false, nil
		}
	}
	return true, nil
}

// RepoRoot walks up from the working directory to the module root.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod above the working directory")
		}
		dir = parent
	}
}
