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

//go:build agentdocker

package agent

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// defaultHAProxyVersion matches the bracket the docker job runs; HAPROXY_VERSION
// overrides it.
const defaultHAProxyVersion = "3.4"

// errAgentUnavailable means this build has no agent to drive — the suite skips
// rather than fails, so the client MR is mergeable before the server MR lands.
var errAgentUnavailable = errors.New("no haptic agent to drive")

var (
	imageOnce sync.Once
	imageName string
	imageErr  error
)

func haproxyVersion() string {
	if version := os.Getenv("HAPROXY_VERSION"); version != "" {
		return version
	}
	return defaultHAProxyVersion
}

func haproxyImage() string {
	return "haproxytech/haproxy-debian:" + haproxyVersion()
}

// requireAgentImage returns an image that is the HAProxy image plus the haptic
// binary: the agent then runs as its own container against the same mounts,
// which is the chart's topology.
func requireAgentImage(t *testing.T) string {
	t.Helper()
	if err := dockerUsable(); err != nil {
		t.Skipf("tests/agent needs a working docker: %v", err)
	}
	imageOnce.Do(func() { imageName, imageErr = buildAgentImage() })
	switch {
	case errors.Is(imageErr, errAgentUnavailable):
		t.Skipf("tests/agent skipped: %v", imageErr)
	case imageErr != nil:
		t.Fatalf("building the agent image: %v", imageErr)
	}
	return imageName
}

func buildAgentImage() (string, error) {
	binary, err := hapticBinary()
	if err != nil {
		return "", err
	}
	tarball, err := buildContext(binary)
	if err != nil {
		return "", err
	}
	tag := fmt.Sprintf("haptic-agent-test:%s-%d", haproxyVersion(), os.Getpid())
	if out, err := runDocker(bytes.NewReader(tarball), "build", "-t", tag, "-"); err != nil {
		return "", fmt.Errorf("docker build: %v\n%s", err, out)
	}
	if out, err := runDocker(nil, "run", "--rm", "--entrypoint", "/usr/local/bin/haptic", tag, "agent", "--help"); err != nil {
		removeImage(tag)
		return "", fmt.Errorf("%w: `haptic agent --help` failed — the agent subcommand is not in this build: %v\n%s",
			errAgentUnavailable, err, out)
	}
	return tag, nil
}

func removeImage(tag string) {
	if tag == "" {
		return
	}
	_, _ = runDocker(nil, "image", "rm", "-f", tag)
}

// buildContext is a two-entry docker build context: the binary and a Dockerfile
// that lays it into the HAProxy image.
func buildContext(binary string) ([]byte, error) {
	content, err := os.ReadFile(binary)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", binary, err)
	}
	dockerfile := fmt.Sprintf("FROM %s\nCOPY haptic /usr/local/bin/haptic\n", haproxyImage())

	var buf bytes.Buffer
	archive := tar.NewWriter(&buf)
	entries := []struct {
		name string
		mode int64
		body []byte
	}{
		{"Dockerfile", 0o644, []byte(dockerfile)},
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

// hapticBinary resolves a linux binary to run in the container: an explicit
// override, then `make build`'s output while it is newer than every Go source,
// then a fresh build.
func hapticBinary() (string, error) {
	if override := os.Getenv("HAPTIC_BINARY"); override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("HAPTIC_BINARY=%s: %w", override, err)
		}
		return override, nil
	}
	root, err := repoRoot()
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
	target := filepath.Join(os.TempDir(), fmt.Sprintf("haptic-agent-test-%d", os.Getpid()))
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
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

func repoRoot() (string, error) {
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
