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

// Package server is the HAPTIC agent: an HTTP endpoint that owns one HAProxy
// pod's file tree and its runtime socket. It executes what the controller
// decided and reports truthfully; it makes no HAProxy decisions of its own.
package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/errgroup"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/cli"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/files"
)

// Timeouts of the HTTP endpoint. The read timeout has to cover a full config
// bundle on a slow link, which is why it is far above the write timeout.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 2 * time.Minute
	writeTimeout      = 3 * time.Minute
	idleTimeout       = 2 * time.Minute
	shutdownGrace     = 15 * time.Second
	startupProbeTick  = 200 * time.Millisecond
)

// Config is everything the agent needs to run.
type Config struct {
	BaseDir           string
	ConfigFile        string
	MasterSocket      string
	WorkerSocket      string
	StateFile         string
	Listen            string
	ReloadIntervalMin time.Duration
	Username          string
	Password          string
	AgentVersion      string
	Logger            *slog.Logger
	Registry          *prometheus.Registry
}

// Server owns the tree, the runtime plumbing and the apply state machine.
type Server struct {
	cfg       Config
	logger    *slog.Logger
	store     *files.Store
	runtime   *cli.Client
	deferrals *cli.Deferrals
	metrics   *Metrics
	states    *stateStore

	// apply serialises the state machine: at most one apply is ever in flight.
	apply sync.Mutex
	// mu guards the fields below, which /v1/state reads while an apply runs.
	mu         sync.Mutex
	state      *persistentState
	tree       map[string]api.FileAt
	inventory  api.Inventory
	worker     api.HAProxyInfo
	lastReload time.Time
	reloadWake chan struct{}
	// reportedInventory is the inventory generation the last ACK carried, so a
	// delta rides an apply exactly once.
	reportedInventory uint64
	// baselineInvalidations counts how often the running worker became
	// unexplained, so an apply can tell whether one happened underneath it.
	baselineInvalidations uint64
	// appliedPlan is the opaque blob of the plan the pod applied; the state
	// file names the plan it belongs to, so a stale one is never handed out.
	appliedPlan []byte

	ready atomic.Bool
	addr  atomic.Pointer[string]
	http  *http.Server
}

// New builds the agent. It probes the mounts under the base directory and
// loads the state file, but it does not touch HAProxy: Start does that, and
// readiness is what reports it.
func New(ctx context.Context, cfg *Config) (*Server, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Registry == nil {
		cfg.Registry = prometheus.NewRegistry()
	}
	if err := files.ValidatePath(cfg.ConfigFile); err != nil {
		return nil, fmt.Errorf("--config: %w", err)
	}
	maxInterval := api.MaxReloadIntervalMs * time.Millisecond
	if cfg.ReloadIntervalMin < 0 || cfg.ReloadIntervalMin > maxInterval {
		return nil, fmt.Errorf("--reload-interval-min %s is outside 0..%s", cfg.ReloadIntervalMin, maxInterval)
	}
	store, err := files.NewStore(cfg.BaseDir, cfg.Logger, cfg.MasterSocket, cfg.WorkerSocket)
	if err != nil {
		return nil, err
	}
	runtimeClient, err := cli.New(ctx, cli.Config{
		WorkerSocket: cfg.WorkerSocket,
		MasterSocket: cfg.MasterSocket,
		Logger:       cfg.Logger,
	})
	if err != nil {
		return nil, err
	}
	deferralClient, err := runtimeClient.Sibling(ctx)
	if err != nil {
		return nil, err
	}
	metrics := NewMetrics(cfg.Registry, cfg.Logger)
	s := &Server{
		cfg:        *cfg,
		logger:     cfg.Logger,
		store:      store,
		runtime:    runtimeClient,
		deferrals:  cli.NewDeferrals(deferralClient, cfg.Logger, metrics),
		metrics:    metrics,
		states:     newStateStore(store.BaseDir(), cfg.StateFile),
		reloadWake: make(chan struct{}, 1),
	}
	if s.state, err = s.states.load(); err != nil {
		return nil, err
	}
	s.loadPlanBlob()
	s.http = &http.Server{
		Addr:              cfg.Listen,
		Handler:           s.routes(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
	return s, nil
}

// routes wires the API. Only the two probes are unauthenticated: kubelet
// cannot present credentials without putting them in the pod spec, and neither
// probe reveals anything about the configuration.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+api.PathHealthz, s.handleHealthz)
	mux.HandleFunc("GET "+api.PathReadyz, s.handleReadyz)
	mux.Handle("GET "+api.PathState, s.authenticated(s.handleState))
	mux.Handle("POST "+api.PathApply, s.authenticated(s.handleApply))
	return mux
}

// Start serves the API until ctx ends. Startup initialisation runs first and
// is what flips readiness; a failure there is retried, never fatal, because
// HAProxy may simply be slower to come up than the agent.
func (s *Server) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.cfg.Listen, err)
	}
	bound := listener.Addr().String()
	s.addr.Store(&bound)

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error { return s.deferrals.Start(groupCtx) })
	group.Go(func() error { return s.pacer(groupCtx) })
	group.Go(func() error { return s.initialise(groupCtx) })
	group.Go(func() error {
		if err := s.http.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	group.Go(func() error {
		<-groupCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
		defer cancel()
		return s.http.Shutdown(shutdownCtx)
	})
	if err := group.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// Addr is the address the endpoint bound to, which is the resolved port when
// the configured address asked for any.
func (s *Server) Addr() string {
	if bound := s.addr.Load(); bound != nil {
		return *bound
	}
	return s.cfg.Listen
}

// Ready reports whether startup initialisation finished.
func (s *Server) Ready() bool { return s.ready.Load() }

// initialise runs the startup sequence readiness reports: both sockets answer,
// the tree is hashed against the state file, crash recovery has run and the
// inventory is built.
func (s *Server) initialise(ctx context.Context) error {
	if err := s.store.SweepTemp(); err != nil {
		s.logger.Warn("could not clear stale temp files", "error", err)
	}
	for {
		err := s.probeHAProxy()
		if err == nil {
			break
		}
		s.logger.Info("waiting for HAProxy", "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(startupProbeTick):
		}
	}
	if err := s.adoptDisk(); err != nil {
		return err
	}
	if err := s.recoverFromCrash(); err != nil {
		return err
	}
	s.ready.Store(true)
	s.logger.Info("agent ready",
		"applied_plan_id", s.snapshot().AppliedPlanID,
		"worker_pid", s.workerIdentity().WorkerPID)
	return nil
}

// probeHAProxy is the readiness gate: the worker answers `show info` and the
// master answers `show proc`.
func (s *Server) probeHAProxy() error {
	info, err := s.runtime.Info()
	if err != nil {
		return err
	}
	if _, err := s.runtime.ShowProc(); err != nil {
		return err
	}
	inventory, err := s.runtime.Inventory(1)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.worker = info
	s.inventory = inventory
	return nil
}

// adoptDisk compares the tree with the state file. A mismatch means something
// other than the agent wrote the tree — a container restart that copied the
// bootstrap config, most often — so the baseline is unknown and the next apply
// is a full reload.
func (s *Server) adoptDisk() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tree, err := s.store.HashTree(s.state.ManifestPaths)
	if err != nil {
		return err
	}
	digest := treeDigest(tree)
	if s.state.TreeDigest != "" && s.state.TreeDigest != digest {
		s.logger.Warn("the file tree changed while the agent was away; baseline unknown",
			"expected", s.state.TreeDigest, "observed", digest)
		s.state.AppliedPlanID = ""
		s.state.RunningPlanID = ""
		s.state.WorkerOpsPlanID = ""
	}
	s.tree = tree
	s.state.TreeDigest = digest
	s.state.ExpectedWorker = s.worker
	return s.states.save(s.state)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeText(w, http.StatusOK, "ok")
}

func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if !s.ready.Load() {
		writeText(w, http.StatusServiceUnavailable, "initialising")
		return
	}
	writeText(w, http.StatusOK, "ready")
}

// authenticated wraps a handler in constant-time basic auth.
func (s *Server) authenticated(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.Username)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(password), []byte(s.cfg.Password)) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="haptic-agent"`)
			writeText(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	})
}

// snapshot copies the persisted state for readers that must not hold the lock.
func (s *Server) snapshot() persistentState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.state
}

func (s *Server) workerIdentity() api.HAProxyInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.worker
}
