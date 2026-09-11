// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package templating

import (
	"context"
	"time"
)

type cycleTimeContextKey struct{}

// WithCycleTime gives cycleTimeBucket one immutable time input across renders.
func WithCycleTime(ctx context.Context, now time.Time) context.Context {
	return context.WithValue(ctx, cycleTimeContextKey{}, now)
}

func cycleTime(ctx context.Context) time.Time {
	if ctx != nil {
		if now, ok := ctx.Value(cycleTimeContextKey{}).(time.Time); ok {
			return now
		}
	}
	return time.Now()
}
