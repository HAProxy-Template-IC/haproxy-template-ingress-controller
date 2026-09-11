// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ObservationProbe samples until Stop and retains the first observation error.
type ObservationProbe struct {
	stop chan struct{}
	once sync.Once
	done chan struct{}
	err  error
}

// StartObservationProbe requires a successful initial observation before returning.
func StartObservationProbe(ctx context.Context, interval time.Duration, observe func(context.Context) error) (*ObservationProbe, error) {
	if interval <= 0 || observe == nil {
		return nil, errors.New("observation probe requires a positive interval and an observer")
	}
	if err := observeWithContext(ctx, observe); err != nil {
		return nil, err
	}
	probe := &ObservationProbe{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(probe.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				probe.err = ctx.Err()
				return
			case <-probe.stop:
				probe.err = observeWithContext(ctx, observe)
				return
			case <-ticker.C:
				if err := observeWithContext(ctx, observe); err != nil {
					probe.err = err
					return
				}
			}
		}
	}()
	return probe, nil
}

func observeWithContext(ctx context.Context, observe func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := observe(ctx); err != nil {
		return err
	}
	return ctx.Err()
}

// Stop waits for the in-flight observation and takes a final boundary sample.
func (p *ObservationProbe) Stop() error {
	p.once.Do(func() { close(p.stop) })
	<-p.done
	return p.err
}
