// Copyright 2026 Philipp Hossner
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

package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

func (s *Server) serveAdmin(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+api.PathHealthz, s.handleHealthz)
	mux.HandleFunc("GET "+api.PathState, s.handleState)
	return s.serveUnix(ctx, s.cfg.AdminSocket, mux, shutdownGrace)
}

func (s *Server) serveUnix(ctx context.Context, path string, handler http.Handler, grace time.Duration) error {
	if err := removeStaleSocket(path); err != nil {
		return err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("listen on local agent socket %s: %w", path, err)
	}
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: readHeaderTimeout, WriteTimeout: grace,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), grace)
	defer cancel()
	err = server.Shutdown(shutdownCtx)
	<-done
	if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		s.logger.Warn("could not remove local agent socket", "error", removeErr)
	}
	return err
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect local agent socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("local agent socket %s is occupied by a non-socket file; choose another path", path)
	}
	return os.Remove(path)
}

func validateLocalSocketPaths(cfg *Config) error {
	paths := map[string]string{}
	for _, entry := range []struct{ name, path string }{
		{"config", cfg.ConfigFile}, {"state-file", cfg.StateFile},
		{"master-socket", cfg.MasterSocket}, {"worker-socket", cfg.WorkerSocket},
		{"drain-socket", cfg.DrainSocket}, {"admin-socket", cfg.AdminSocket},
	} {
		if entry.path == "" {
			continue
		}
		path := entry.path
		if !filepath.IsAbs(path) {
			path = filepath.Join(cfg.BaseDir, path)
		}
		path = filepath.Clean(path)
		if previous, exists := paths[path]; exists {
			return fmt.Errorf("--%s and --%s share %s; choose distinct file and socket paths", previous, entry.name, path)
		}
		paths[path] = entry.name
	}
	return nil
}
