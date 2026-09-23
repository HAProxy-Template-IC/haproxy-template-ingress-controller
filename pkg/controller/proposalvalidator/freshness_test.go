package proposalvalidator

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/dataplanetest"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores/storetest"
)

func TestAdmissionRefreshesMissingDependencyWithoutChangingWatchStore(t *testing.T) {
	const dependency = `{% if len(resources.ingresses.Fetch("default", "dependency")) == 0 %}{% fail("missing dependency") %}{% end %}`
	for _, test := range []struct {
		name         string
		exists       bool
		rejectOutput bool
		refreshErr   error
		wantError    string
	}{
		{name: "created before watch delivery", exists: true},
		{name: "still absent", wantError: "missing dependency"},
		{name: "API unavailable", refreshErr: errors.New("API unavailable"), wantError: "API unavailable"},
		{name: "invalid rendered configuration", exists: true, rejectOutput: true, wantError: "invalid configuration"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.rejectOutput {
				t.Cleanup(dataplanetest.InstallFakeHAProxy(dataplanetest.WithRejectAll("invalid configuration")))
			}
			cached := &storetest.MockStore{}
			fresh := &storetest.MockStore{}
			if test.exists {
				require.NoError(t, fresh.Add(unstructuredObj("default", "dependency"), []string{"default", "dependency"}))
			}
			refreshes := 0
			svc := NewService(&ServiceConfig{
				Pipeline:          createStoreTestPipeline(t, dependency+testutil.MinimalHAProxyConfig+"\n# count {{ len(resources.ingresses.List()) }}\n"),
				BaseStoreProvider: stores.NewRealStoreProvider(map[string]stores.Store{"ingresses": cached}),
				FreshStoreProvider: func(context.Context) (stores.StoreProvider, error) {
					refreshes++
					return stores.NewRealStoreProvider(map[string]stores.Store{"ingresses": fresh}), test.refreshErr
				},
			})
			_, result := svc.ValidateSync(t.Context(), map[string]*stores.StoreOverlay{
				"ingresses": stores.NewStoreOverlayForCreate(unstructuredObj("default", "proposal")),
			})
			assert.Equal(t, 1, refreshes)
			if test.wantError == "" {
				require.NoError(t, result.Error)
				assert.True(t, result.Valid)
			} else {
				assert.False(t, result.Valid)
				require.ErrorContains(t, result.Error, test.wantError)
			}
			resources, err := cached.List()
			require.NoError(t, err)
			assert.Empty(t, resources)
		})
	}
}

func TestAdmissionDoesNotRefreshSuccessfulOrCanceledValidation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "canceled"}[canceled], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if canceled {
				cancel()
			}
			svc := NewService(&ServiceConfig{
				Pipeline:          createTestPipeline(t, testutil.MinimalHAProxyConfig),
				BaseStoreProvider: stores.NewRealStoreProvider(nil),
				FreshStoreProvider: func(context.Context) (stores.StoreProvider, error) {
					t.Error("unexpected API refresh")
					return nil, errors.New("unexpected refresh")
				},
			})
			_, result := svc.ValidateSync(ctx, nil)
			assert.Equal(t, !canceled, result.Valid)
		})
	}
}
