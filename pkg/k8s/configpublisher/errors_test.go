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

package configpublisher

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestIsRetryablePublicationError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "service unavailable", err: apierrors.NewServiceUnavailable("unavailable"), want: true},
		{name: "request deadline", err: context.DeadlineExceeded, want: true},
		{name: "truncated response", err: io.ErrUnexpectedEOF, want: true},
		{name: "missing intermediate resource", err: apierrors.NewNotFound(schema.GroupResource{Resource: "haproxycfgs"}, "cfg"), want: true},
		{name: "forbidden", err: apierrors.NewForbidden(schema.GroupResource{Resource: "haproxycfgs"}, "cfg", errors.New("denied")), want: false},
		{name: "canceled", err: context.Canceled, want: false},
		{name: "local permanent error", err: errors.New("invalid publication request"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := incompletePublicationError(PublicationStageRuntimeConfig, "default", "cfg", "HAProxyCfg", "cfg", tt.err)
			assert.Equal(t, tt.want, IsRetryablePublicationError(wrapped))
		})
	}
}
