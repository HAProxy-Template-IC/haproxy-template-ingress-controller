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

package logging

import (
	"context"
	"errors"
	"log/slog"
)

// WithCancellationLogging records cancellation errors at debug level after lifecycle shutdown.
func WithCancellationLogging(logger *slog.Logger, lifecycle context.Context) *slog.Logger {
	return slog.New(&cancellationHandler{Handler: logger.Handler(), lifecycle: lifecycle})
}

type cancellationHandler struct {
	slog.Handler
	lifecycle context.Context
}

func (h *cancellationHandler) Handle(ctx context.Context, record slog.Record) error {
	if h.lifecycle.Err() == context.Canceled {
		record.Attrs(func(attr slog.Attr) bool {
			if err, ok := attr.Value.Any().(error); ok && errors.Is(err, context.Canceled) {
				record.Level = slog.LevelDebug
				return false
			}
			return true
		})
	}
	if !h.Enabled(ctx, record.Level) {
		return nil
	}
	return h.Handler.Handle(ctx, record)
}

func (h *cancellationHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &cancellationHandler{Handler: h.Handler.WithAttrs(attrs), lifecycle: h.lifecycle}
}

func (h *cancellationHandler) WithGroup(name string) slog.Handler {
	return &cancellationHandler{Handler: h.Handler.WithGroup(name), lifecycle: h.lifecycle}
}
