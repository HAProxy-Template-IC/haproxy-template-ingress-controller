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

package controller

import (
	"context"
	"log/slog"
	"time"
)

const (
	admissionDrainQuietPeriod = 2 * time.Second
	admissionDrainMaxWait     = 10 * time.Second
	admissionShutdownTimeout  = 32 * time.Second
)

type processShutdown struct {
	started time.Time
	err     error
}

func drainOnCancellation(ctx, procCtx context.Context, cancel context.CancelFunc, drain func(context.Context) error) <-chan processShutdown {
	done := make(chan processShutdown, 1)
	go func() {
		var stopped processShutdown
		select {
		case <-ctx.Done():
			stopped.started = time.Now()
			stopped.err = drain(procCtx)
			cancel()
		case <-procCtx.Done():
			stopped.started = time.Now()
		}
		done <- stopped
	}()
	return done
}

func (p *persistentInfra) drainAdmission(ctx context.Context, logger *slog.Logger) error {
	p.webhookMu.Lock()
	p.draining.Store(true)
	server, run := p.WebhookServer, p.webhookRun
	p.webhookMu.Unlock()
	if server == nil || run == nil {
		return nil
	}

	logger.Info("Draining admission requests before stopping controller components")
	quietCtx, cancelQuiet := context.WithTimeout(ctx, admissionDrainMaxWait)
	err := server.WaitForQuiet(quietCtx, admissionDrainQuietPeriod)
	cancelQuiet()
	if err != nil && ctx.Err() == nil {
		logger.Info("Admission traffic reached the drain deadline; finishing active requests")
	}
	run.stopping.Store(true)
	shutdownCtx, cancelShutdown := context.WithTimeout(ctx, admissionShutdownTimeout)
	defer cancelShutdown()
	return server.Shutdown(shutdownCtx)
}
