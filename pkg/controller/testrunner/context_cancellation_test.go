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

package testrunner

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type cancelBlockingEngine struct {
	templating.Engine
	blockTemplate string
	blockCall     int
	started       chan struct{}
	release       chan struct{}
	startedOnce   sync.Once
	mu            sync.Mutex
	calls         map[string]int
}

type renderCancellationCase struct {
	name            string
	blockTemplate   string
	blockCall       int
	profileIncludes bool
	withMap         bool
	withK8sResource bool
}

func (e *cancelBlockingEngine) Render(ctx context.Context, templateName string, templateContext map[string]any) (string, error) {
	if err := e.wait(ctx, templateName); err != nil {
		return "", err
	}
	return e.Engine.Render(ctx, templateName, templateContext)
}

func (e *cancelBlockingEngine) RenderWithProfiling(ctx context.Context, templateName string, templateContext map[string]any) (string, []templating.IncludeStats, error) {
	if err := e.wait(ctx, templateName); err != nil {
		return "", nil, err
	}
	return e.Engine.RenderWithProfiling(ctx, templateName, templateContext)
}

func (e *cancelBlockingEngine) wait(ctx context.Context, templateName string) error {
	e.mu.Lock()
	e.calls[templateName]++
	shouldBlock := templateName == e.blockTemplate && e.calls[templateName] == e.blockCall
	e.mu.Unlock()
	if !shouldBlock {
		return nil
	}

	e.startedOnce.Do(func() { close(e.started) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.release:
		return nil
	}
}

func TestRunTestsCancelsEveryRenderStage(t *testing.T) {
	tests := []renderCancellationCase{
		{name: "main render", blockTemplate: "haproxy.cfg", blockCall: 1},
		{name: "profiled main render", blockTemplate: "haproxy.cfg", blockCall: 1, profileIncludes: true},
		{name: "auxiliary render", blockTemplate: "routes.map", blockCall: 1, withMap: true},
		{name: "Kubernetes resource render", blockTemplate: "resource.yaml", blockCall: 1, withK8sResource: true},
		{name: "determinism render", blockTemplate: "haproxy.cfg", blockCall: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { testRenderStageCancellation(t, test) })
	}
}

func testRenderStageCancellation(t *testing.T, test renderCancellationCase) {
	t.Helper()
	templates := map[string]string{"haproxy.cfg": "global\n"}
	cfg := cancellationConfig(templates, test)
	engine, err := templating.New(templates, nil)
	require.NoError(t, err)
	probe := &cancelBlockingEngine{
		Engine: engine, blockTemplate: test.blockTemplate, blockCall: test.blockCall,
		started: make(chan struct{}), release: make(chan struct{}), calls: make(map[string]int),
	}
	var releaseOnce sync.Once
	releaseRender := func() { releaseOnce.Do(func() { close(probe.release) }) }
	defer releaseRender()

	runner := New(cfg, probe, &dataplane.ValidationPaths{ConfigFile: filepath.Join(t.TempDir(), "haproxy.cfg")}, &Options{
		Workers: 1, ProfileIncludes: test.profileIncludes,
		CheckWithoutBinary: func(string) error { return nil },
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, runErr := runner.RunTests(ctx, "")
		done <- runErr
	}()

	waitForRenderStart(t, probe.started, releaseRender)
	cancel()
	waitForRunTestsStop(t, done, releaseRender)
}

func cancellationConfig(templates map[string]string, test renderCancellationCase) *config.Config {
	cfg := &config.Config{
		HAProxyConfig:   config.HAProxyConfig{Template: templates["haproxy.cfg"]},
		ValidationTests: map[string]config.ValidationTest{"cancellation": {}},
	}
	if test.withMap {
		templates["routes.map"] = "key value\n"
		cfg.Maps = map[string]config.MapFile{"routes.map": {Template: templates["routes.map"]}}
	}
	if test.withK8sResource {
		templates["resource.yaml"] = "{}\n"
		cfg.K8sResources = map[string]config.K8sResource{"resource.yaml": {Template: templates["resource.yaml"]}}
	}
	config.SetDefaults(cfg)
	return cfg
}

func waitForRenderStart(t *testing.T, started <-chan struct{}, release func()) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		release()
		t.Fatal("render did not reach cancellation probe")
	}
}

func waitForRunTestsStop(t *testing.T, done <-chan error, release func()) {
	t.Helper()
	select {
	case runErr := <-done:
		require.NoError(t, runErr)
	case <-time.After(2 * time.Second):
		release()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		t.Fatal("RunTests did not stop after its context was canceled")
	}
}
