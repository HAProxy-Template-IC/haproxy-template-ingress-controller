// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
)

func TestCombinedInputTransactionReleasesRenderSessionReferences(t *testing.T) {
	tests := map[string]func(*testing.T, *combinedRenderInputTransaction){
		"commit": func(t *testing.T, transaction *combinedRenderInputTransaction) {
			t.Helper()
			require.NoError(t, transaction.Commit(t.Context()))
		},
		"abort": func(_ *testing.T, transaction *combinedRenderInputTransaction) {
			transaction.Abort()
		},
		"cancelled commit": func(t *testing.T, transaction *combinedRenderInputTransaction) {
			t.Helper()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			require.ErrorIs(t, transaction.Commit(ctx), context.Canceled)
		},
	}
	for name, finish := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newIncrementalHTTPTestFixture(t)
			result, err := fixture.service.Render(
				t.Context(), fixture.provider, rendercontext.RenderModeReconcile,
			)
			require.NoError(t, err)
			transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
			require.True(t, ok)
			session := transaction.incremental
			require.NotNil(t, session)

			finish(t, transaction)

			http, incrementalSession, logger := transaction.references()
			assert.Nil(t, http)
			assert.Nil(t, incrementalSession)
			assert.Nil(t, logger)
			assert.True(t, session.released)
			assert.False(t, session.resourceMaterializations.valid())
			assert.False(t, session.publicationGeneration.validFor(session.publicationAuthority))
		})
	}
}
