// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var chartVersionLine = regexp.MustCompile(`(?m)^version:.*$`)

func (g *gitops) prepareCharts(ctx context.Context) error {
	s := g.session
	provenance, err := g.imageProvenance(ctx)
	if err != nil {
		return err
	}
	repository := filepath.Join(s.Artifacts, "repository")
	if err := os.Mkdir(repository, 0o700); err != nil {
		return err
	}
	charts := map[string]any{}
	for number, phase := range []string{phaseInstalled, phaseUpgraded, phaseRejected, phaseRecovered} {
		entry, err := g.prepareChart(ctx, repository, phase, number+1)
		if err != nil {
			return err
		}
		charts[phase] = entry
	}
	provenance["charts"] = charts
	if err := s.Save("provenance.json", provenance); err != nil {
		return err
	}
	if _, err := s.Helm(ctx, "repo", "index", repository, "--url", gitopsRepository); err != nil {
		return err
	}
	return g.serveCharts(ctx, repository)
}

func (g *gitops) imageProvenance(ctx context.Context) (map[string]any, error) {
	s := g.session
	imageID, err := s.Run(ctx, "docker", "image", "inspect", g.options.Image, "--format", "{{.Id}}")
	if err != nil {
		return nil, err
	}
	version, err := s.Run(ctx, "docker", "run", "--rm", "--entrypoint", chartName, g.options.Image, "version")
	if err != nil {
		return nil, err
	}
	source, err := s.Run(ctx, filepath.Join(s.Root, "scripts", "source-hash.sh"))
	if err != nil {
		return nil, err
	}
	hash := strings.TrimSpace(source.Stdout)
	if hash == "" || !strings.Contains(version.Stdout, "Source Hash: "+hash) {
		return nil, fmt.Errorf("image does not match worktree source %s: %s", hash, version.Stdout)
	}
	commit, err := s.Run(ctx, "git", "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	dirty, err := s.Run(ctx, "git", "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	kubernetes, err := readJSON[map[string]any](ctx, s, "version")
	if err != nil {
		return nil, err
	}
	providerVersion := fluxVersion
	if g.options.Provider == providerArgo {
		providerVersion = argoVersion
	}
	return map[string]any{"commit": strings.TrimSpace(commit.Stdout), "dirty": strings.TrimSpace(dirty.Stdout) != "", "sourceHash": hash, "baseImageID": strings.TrimSpace(imageID.Stdout), "versionOutput": version.Stdout, "provider": g.options.Provider, "certificates": g.options.Certificates, "providerVersion": providerVersion, "kubernetes": kubernetes["serverVersion"]}, nil
}

func (g *gitops) prepareChart(ctx context.Context, repository, phase string, number int) (map[string]string, error) {
	s := g.session
	chart := filepath.Join(s.Artifacts, phase+"-chart")
	if err := os.CopyFS(chart, os.DirFS(filepath.Join(s.Root, "charts", chartName))); err != nil {
		return nil, err
	}
	version := fmt.Sprintf("0.0.0-gitops.%d", number)
	if err := rewriteChart(chart, version, phase == phaseRejected); err != nil {
		return nil, err
	}
	tag := s.Cluster.Name + "-" + phase
	image := "haptic:" + tag + "-haproxy" + s.HAProxyVersion
	dockerfile := "ARG BASE_IMAGE\nFROM ${BASE_IMAGE}\nUSER root\nRUN rm -rf /usr/share/haptic/chart\nCOPY . /usr/share/haptic/chart/\nUSER haproxy\n"
	if _, err := s.RunInput(ctx, strings.NewReader(dockerfile), "docker", "build", "--build-arg", "BASE_IMAGE="+g.options.Image, "-t", image, "-f", "-", chart); err != nil {
		return nil, err
	}
	if err := s.Cluster.LoadImages(ctx, image); err != nil {
		return nil, err
	}
	if _, err := s.Helm(ctx, "package", chart, "--destination", repository); err != nil {
		return nil, err
	}
	files, err := os.OpenRoot(repository)
	if err != nil {
		return nil, err
	}
	defer files.Close()
	archive, err := files.ReadFile("haptic-" + version + ".tgz")
	if err != nil {
		return nil, err
	}
	imageID, err := s.Run(ctx, "docker", "image", "inspect", image, "--format", "{{.Id}}")
	if err != nil {
		return nil, err
	}
	g.versions[phase], g.images[phase] = version, tag
	return map[string]string{"version": version, "sha256": fmt.Sprintf("%x", sha256.Sum256(archive)), "imageID": strings.TrimSpace(imageID.Stdout)}, nil
}

func rewriteChart(path, version string, broken bool) error {
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	content, err := root.ReadFile("Chart.yaml")
	if err != nil {
		return err
	}
	if !chartVersionLine.Match(content) {
		return errors.New("chart has no version field")
	}
	if err := root.WriteFile("Chart.yaml", chartVersionLine.ReplaceAll(content, []byte("version: "+version)), 0o600); err != nil {
		return err
	}
	if !broken {
		return nil
	}
	const library = "charts/base/library.yaml"
	content, err = root.ReadFile(library)
	if err != nil {
		return err
	}
	content, err = corruptMainTemplate(content)
	if err != nil {
		return err
	}
	return root.WriteFile(library, content, 0o600)
}

func (g *gitops) serveCharts(ctx context.Context, directory string) error {
	s := g.session
	pod := resource("v1", "Pod", gitopsNamespace, "chart-repository")
	pod["metadata"].(map[string]any)["labels"] = map[string]string{"app": "chart-repository"}
	pod["spec"] = map[string]any{"containers": []any{map[string]any{fieldName: "http", "image": "busybox:1.37.0", "command": []string{"sh", "-c", "mkdir -p /www; exec httpd -f -p 8080 -h /www"}, "ports": []any{map[string]int{"containerPort": 8080}}}}}
	if err := s.Apply(ctx, pod); err != nil {
		return err
	}
	service := resource("v1", "Service", gitopsNamespace, "chart-repository")
	service["spec"] = map[string]any{"selector": map[string]string{"app": "chart-repository"}, "ports": []any{map[string]int{"port": 8080, "targetPort": 8080}}}
	if err := s.Apply(ctx, service); err != nil {
		return err
	}
	if _, err := s.Kube(ctx, nil, "-n", gitopsNamespace, "wait", "--for=condition=Ready", "pod/chart-repository", "--timeout=180s"); err != nil {
		return err
	}
	archive, err := directoryArchive(directory)
	if err != nil {
		return err
	}
	_, err = s.Kube(ctx, bytes.NewReader(archive), "-n", gitopsNamespace, "exec", "-i", "chart-repository", "--", "tar", "xf", "-", "-C", "/www")
	return err
}

func directoryArchive(path string) ([]byte, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	archive := tar.NewWriter(&buffer)
	for _, entry := range entries {
		content, err := root.ReadFile(entry.Name())
		if err != nil {
			return nil, err
		}
		if err := archive.WriteHeader(&tar.Header{Name: entry.Name(), Mode: 0o600, Size: int64(len(content))}); err != nil {
			return nil, err
		}
		if _, err := io.Copy(archive, bytes.NewReader(content)); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
