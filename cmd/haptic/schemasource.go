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

package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/conversion"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/schemafetcher"
)

// schemaSource is where typed-resource schemas come from — a directory, the
// live cluster, or nowhere.
//
// The two are not interchangeable in reach. A directory can only hold what
// somebody put in it, and the API surface it describes is whatever it was
// dumped from. The cluster is authoritative for the cluster you deploy to,
// including which optional CRDs are actually installed — but needs
// credentials, so it is not always available.
//
// The zero value is the third case: no schemas at all. Effective-config
// resolution then keeps every candidate and typed access falls through to the
// untyped resources path, which is what `validate` does without --schema-dir.
type schemaSource struct {
	dir  *schemafetcher.DirFetcher
	live *liveCluster
}

// liveSchemaTimeout bounds the cluster round-trips one schema operation makes.
// A stale kubeconfig pointing at an unreachable endpoint dials rather than
// failing, so without this a CLI run hangs instead of reporting no cluster
// access. Generous: discovery plus a schema fetch per watched resource.
const liveSchemaTimeout = 2 * time.Minute

// withLiveDeadline bounds ctx for cluster-backed sources and leaves offline
// ones alone — reading a local directory cannot hang on a network.
func (s schemaSource) withLiveDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.live == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, liveSchemaTimeout)
}

// newDirSchemaSource loads a schema directory. An empty path yields the zero
// value — no schemas — rather than an error.
func newDirSchemaSource(schemaDir string, logger *slog.Logger) (schemaSource, error) {
	if schemaDir == "" {
		return schemaSource{}, nil
	}
	dirFetcher, err := schemafetcher.NewDirFetcher(schemaDir)
	if err != nil {
		return schemaSource{}, fmt.Errorf("loading schema directory %q: %w", schemaDir, err)
	}
	logger.Info("Offline type bootstrap: loaded schema directory",
		"path", schemaDir, "schemas", dirFetcher.Len())
	return schemaSource{dir: dirFetcher}, nil
}

// newLiveSchemaSource connects to the cluster named by kubeconfig (or the
// standard loading rules, or in-cluster credentials).
func newLiveSchemaSource(kubeconfig string) (schemaSource, error) {
	live, err := connectLiveCluster(kubeconfig)
	if err != nil {
		return schemaSource{}, err
	}
	return schemaSource{live: live}, nil
}

// resolveEffectiveSpec mirrors the controller's effective-config resolution:
// apiVersions candidates resolve against the source, and features whose
// optional resources it doesn't have get stripped.
func (s schemaSource) resolveEffectiveSpec(
	ctx context.Context,
	spec *v1alpha1.HAProxyTemplateConfigSpec,
	logger *slog.Logger,
) (*conversion.SpecResolution, error) {
	if s.live == nil {
		served, fieldServed := dirServedCheckers(s.dir)
		resolution, err := conversion.ResolveEffectiveSpec(spec, served, fieldServed, logger)
		if err != nil {
			return nil, fmt.Errorf("resolving effective config: %w", err)
		}
		return resolution, nil
	}

	ctx, cancel := s.withLiveDeadline(ctx)
	defer cancel()

	checker := controller.NewDiscoveryServedChecker(ctx, s.live.discovery(), s.live.fetcher, logger)
	resolution, err := conversion.ResolveEffectiveSpec(spec, checker.IsServed, checker.FieldServed, logger)
	if err != nil {
		return nil, fmt.Errorf("resolving effective config: %w", err)
	}
	// A transient discovery failure would silently strip features that are
	// actually served, so report it rather than validating a smaller config.
	if terr := checker.TransientErr(); terr != nil {
		return nil, fmt.Errorf("resolving effective config against the cluster: %w", terr)
	}
	return resolution, nil
}

// typeBootstrap generates the typed Go structs for the watched resources.
// Always returns a non-nil Result.
func (s schemaSource) typeBootstrap(
	ctx context.Context,
	spec *v1alpha1.HAProxyTemplateConfigSpec,
	logger *slog.Logger,
) (*typebootstrap.Result, error) {
	if s.live == nil {
		return runOfflineTypeBootstrap(spec, s.dir, logger)
	}
	cfg, err := conversion.ConvertSpec(spec)
	if err != nil {
		return nil, fmt.Errorf("converting config: %w", err)
	}
	ctx, cancel := s.withLiveDeadline(ctx)
	defer cancel()

	return controller.RunTypeBootstrap(ctx, cfg, s.live.fetcher, s.live.discovery(), logger)
}
