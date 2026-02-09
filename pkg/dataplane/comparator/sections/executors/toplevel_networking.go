// Package executors provides pre-built executor functions for HAProxy configuration operations.
//
// These functions encapsulate the dispatcher callback boilerplate, providing a clean
// interface between the generic operation types and the versioned DataPlane API clients.
package executors

import (
	"context"
	"fmt"
	"net/http"

	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	v30 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30"
	v30ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30ee"
	v31 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31"
	v31ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31ee"
	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
	v33 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v33"
)

// CacheCreate returns an executor for creating cache sections.
func CacheCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Cache, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Cache, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v33.Cache) (*http.Response, error) {
				params := &v33.CreateCacheParams{TransactionId: &txID}
				return clientset.V33().CreateCache(ctx, params, m)
			},
			func(m v32.Cache) (*http.Response, error) {
				params := &v32.CreateCacheParams{TransactionId: &txID}
				return clientset.V32().CreateCache(ctx, params, m)
			},
			func(m v31.Cache) (*http.Response, error) {
				params := &v31.CreateCacheParams{TransactionId: &txID}
				return clientset.V31().CreateCache(ctx, params, m)
			},
			func(m v30.Cache) (*http.Response, error) {
				params := &v30.CreateCacheParams{TransactionId: &txID}
				return clientset.V30().CreateCache(ctx, params, m)
			},
			func(m v32ee.Cache) (*http.Response, error) {
				params := &v32ee.CreateCacheParams{TransactionId: &txID}
				return clientset.V32EE().CreateCache(ctx, params, m)
			},
			func(m v31ee.Cache) (*http.Response, error) {
				params := &v31ee.CreateCacheParams{TransactionId: &txID}
				return clientset.V31EE().CreateCache(ctx, params, m)
			},
			func(m v30ee.Cache) (*http.Response, error) {
				params := &v30ee.CreateCacheParams{TransactionId: &txID}
				return clientset.V30EE().CreateCache(ctx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "cache creation")
	}
}

// CacheUpdate returns an executor for updating cache sections.
func CacheUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Cache, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Cache, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
			func(n string, m v33.Cache) (*http.Response, error) {
				params := &v33.ReplaceCacheParams{TransactionId: &txID}
				return clientset.V33().ReplaceCache(ctx, n, params, m)
			},
			func(n string, m v32.Cache) (*http.Response, error) {
				params := &v32.ReplaceCacheParams{TransactionId: &txID}
				return clientset.V32().ReplaceCache(ctx, n, params, m)
			},
			func(n string, m v31.Cache) (*http.Response, error) {
				params := &v31.ReplaceCacheParams{TransactionId: &txID}
				return clientset.V31().ReplaceCache(ctx, n, params, m)
			},
			func(n string, m v30.Cache) (*http.Response, error) {
				params := &v30.ReplaceCacheParams{TransactionId: &txID}
				return clientset.V30().ReplaceCache(ctx, n, params, m)
			},
			func(n string, m v32ee.Cache) (*http.Response, error) {
				params := &v32ee.ReplaceCacheParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceCache(ctx, n, params, m)
			},
			func(n string, m v31ee.Cache) (*http.Response, error) {
				params := &v31ee.ReplaceCacheParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceCache(ctx, n, params, m)
			},
			func(n string, m v30ee.Cache) (*http.Response, error) {
				params := &v30ee.ReplaceCacheParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceCache(ctx, n, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "cache update")
	}
}

// CacheDelete returns an executor for deleting cache sections.
func CacheDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Cache, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Cache, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v33.DeleteCacheParams{TransactionId: &txID}
				return clientset.V33().DeleteCache(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32.DeleteCacheParams{TransactionId: &txID}
				return clientset.V32().DeleteCache(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteCacheParams{TransactionId: &txID}
				return clientset.V31().DeleteCache(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteCacheParams{TransactionId: &txID}
				return clientset.V30().DeleteCache(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteCacheParams{TransactionId: &txID}
				return clientset.V32EE().DeleteCache(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteCacheParams{TransactionId: &txID}
				return clientset.V31EE().DeleteCache(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteCacheParams{TransactionId: &txID}
				return clientset.V30EE().DeleteCache(ctx, n, params)
			},
		)
		return dispatchAndCheck(resp, err, "cache deletion")
	}
}

// HTTPErrorsSectionCreate returns an executor for creating http-errors sections.
func HTTPErrorsSectionCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.HTTPErrorsSection, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.HTTPErrorsSection, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v33.HttpErrorsSection) (*http.Response, error) {
				params := &v33.CreateHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V33().CreateHTTPErrorsSection(ctx, params, m)
			},
			func(m v32.HttpErrorsSection) (*http.Response, error) {
				params := &v32.CreateHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V32().CreateHTTPErrorsSection(ctx, params, m)
			},
			func(m v31.HttpErrorsSection) (*http.Response, error) {
				params := &v31.CreateHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V31().CreateHTTPErrorsSection(ctx, params, m)
			},
			func(m v30.HttpErrorsSection) (*http.Response, error) {
				params := &v30.CreateHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V30().CreateHTTPErrorsSection(ctx, params, m)
			},
			func(m v32ee.HttpErrorsSection) (*http.Response, error) {
				params := &v32ee.CreateHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V32EE().CreateHTTPErrorsSection(ctx, params, m)
			},
			func(m v31ee.HttpErrorsSection) (*http.Response, error) {
				params := &v31ee.CreateHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V31EE().CreateHTTPErrorsSection(ctx, params, m)
			},
			func(m v30ee.HttpErrorsSection) (*http.Response, error) {
				params := &v30ee.CreateHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V30EE().CreateHTTPErrorsSection(ctx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "http-errors section creation")
	}
}

// HTTPErrorsSectionUpdate returns an executor for updating http-errors sections.
func HTTPErrorsSectionUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.HTTPErrorsSection, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.HTTPErrorsSection, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
			func(n string, m v33.HttpErrorsSection) (*http.Response, error) {
				params := &v33.ReplaceHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V33().ReplaceHTTPErrorsSection(ctx, n, params, m)
			},
			func(n string, m v32.HttpErrorsSection) (*http.Response, error) {
				params := &v32.ReplaceHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V32().ReplaceHTTPErrorsSection(ctx, n, params, m)
			},
			func(n string, m v31.HttpErrorsSection) (*http.Response, error) {
				params := &v31.ReplaceHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V31().ReplaceHTTPErrorsSection(ctx, n, params, m)
			},
			func(n string, m v30.HttpErrorsSection) (*http.Response, error) {
				params := &v30.ReplaceHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V30().ReplaceHTTPErrorsSection(ctx, n, params, m)
			},
			func(n string, m v32ee.HttpErrorsSection) (*http.Response, error) {
				params := &v32ee.ReplaceHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceHTTPErrorsSection(ctx, n, params, m)
			},
			func(n string, m v31ee.HttpErrorsSection) (*http.Response, error) {
				params := &v31ee.ReplaceHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceHTTPErrorsSection(ctx, n, params, m)
			},
			func(n string, m v30ee.HttpErrorsSection) (*http.Response, error) {
				params := &v30ee.ReplaceHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceHTTPErrorsSection(ctx, n, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "http-errors section update")
	}
}

// HTTPErrorsSectionDelete returns an executor for deleting http-errors sections.
func HTTPErrorsSectionDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.HTTPErrorsSection, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.HTTPErrorsSection, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v33.DeleteHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V33().DeleteHTTPErrorsSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32.DeleteHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V32().DeleteHTTPErrorsSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V31().DeleteHTTPErrorsSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V30().DeleteHTTPErrorsSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V32EE().DeleteHTTPErrorsSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V31EE().DeleteHTTPErrorsSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteHTTPErrorsSectionParams{TransactionId: &txID}
				return clientset.V30EE().DeleteHTTPErrorsSection(ctx, n, params)
			},
		)
		return dispatchAndCheck(resp, err, "http-errors section deletion")
	}
}

// LogForwardCreate returns an executor for creating log-forward sections.
func LogForwardCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.LogForward, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.LogForward, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v33.LogForward) (*http.Response, error) {
				params := &v33.CreateLogForwardParams{TransactionId: &txID}
				return clientset.V33().CreateLogForward(ctx, params, m)
			},
			func(m v32.LogForward) (*http.Response, error) {
				params := &v32.CreateLogForwardParams{TransactionId: &txID}
				return clientset.V32().CreateLogForward(ctx, params, m)
			},
			func(m v31.LogForward) (*http.Response, error) {
				params := &v31.CreateLogForwardParams{TransactionId: &txID}
				return clientset.V31().CreateLogForward(ctx, params, m)
			},
			func(m v30.LogForward) (*http.Response, error) {
				params := &v30.CreateLogForwardParams{TransactionId: &txID}
				return clientset.V30().CreateLogForward(ctx, params, m)
			},
			func(m v32ee.LogForward) (*http.Response, error) {
				params := &v32ee.CreateLogForwardParams{TransactionId: &txID}
				return clientset.V32EE().CreateLogForward(ctx, params, m)
			},
			func(m v31ee.LogForward) (*http.Response, error) {
				params := &v31ee.CreateLogForwardParams{TransactionId: &txID}
				return clientset.V31EE().CreateLogForward(ctx, params, m)
			},
			func(m v30ee.LogForward) (*http.Response, error) {
				params := &v30ee.CreateLogForwardParams{TransactionId: &txID}
				return clientset.V30EE().CreateLogForward(ctx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "log-forward creation")
	}
}

// LogForwardUpdate returns an executor for updating log-forward sections.
func LogForwardUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.LogForward, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.LogForward, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
			func(n string, m v33.LogForward) (*http.Response, error) {
				params := &v33.ReplaceLogForwardParams{TransactionId: &txID}
				return clientset.V33().ReplaceLogForward(ctx, n, params, m)
			},
			func(n string, m v32.LogForward) (*http.Response, error) {
				params := &v32.ReplaceLogForwardParams{TransactionId: &txID}
				return clientset.V32().ReplaceLogForward(ctx, n, params, m)
			},
			func(n string, m v31.LogForward) (*http.Response, error) {
				params := &v31.ReplaceLogForwardParams{TransactionId: &txID}
				return clientset.V31().ReplaceLogForward(ctx, n, params, m)
			},
			func(n string, m v30.LogForward) (*http.Response, error) {
				params := &v30.ReplaceLogForwardParams{TransactionId: &txID}
				return clientset.V30().ReplaceLogForward(ctx, n, params, m)
			},
			func(n string, m v32ee.LogForward) (*http.Response, error) {
				params := &v32ee.ReplaceLogForwardParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceLogForward(ctx, n, params, m)
			},
			func(n string, m v31ee.LogForward) (*http.Response, error) {
				params := &v31ee.ReplaceLogForwardParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceLogForward(ctx, n, params, m)
			},
			func(n string, m v30ee.LogForward) (*http.Response, error) {
				params := &v30ee.ReplaceLogForwardParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceLogForward(ctx, n, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "log-forward update")
	}
}

// LogForwardDelete returns an executor for deleting log-forward sections.
func LogForwardDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.LogForward, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.LogForward, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v33.DeleteLogForwardParams{TransactionId: &txID}
				return clientset.V33().DeleteLogForward(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32.DeleteLogForwardParams{TransactionId: &txID}
				return clientset.V32().DeleteLogForward(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteLogForwardParams{TransactionId: &txID}
				return clientset.V31().DeleteLogForward(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteLogForwardParams{TransactionId: &txID}
				return clientset.V30().DeleteLogForward(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteLogForwardParams{TransactionId: &txID}
				return clientset.V32EE().DeleteLogForward(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteLogForwardParams{TransactionId: &txID}
				return clientset.V31EE().DeleteLogForward(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteLogForwardParams{TransactionId: &txID}
				return clientset.V30EE().DeleteLogForward(ctx, n, params)
			},
		)
		return dispatchAndCheck(resp, err, "log-forward deletion")
	}
}

// PeerSectionCreate returns an executor for creating peer sections.
func PeerSectionCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.PeerSection, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.PeerSection, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v33.PeerSection) (*http.Response, error) {
				params := &v33.CreatePeerParams{TransactionId: &txID}
				return clientset.V33().CreatePeer(ctx, params, m)
			},
			func(m v32.PeerSection) (*http.Response, error) {
				params := &v32.CreatePeerParams{TransactionId: &txID}
				return clientset.V32().CreatePeer(ctx, params, m)
			},
			func(m v31.PeerSection) (*http.Response, error) {
				params := &v31.CreatePeerParams{TransactionId: &txID}
				return clientset.V31().CreatePeer(ctx, params, m)
			},
			func(m v30.PeerSection) (*http.Response, error) {
				params := &v30.CreatePeerParams{TransactionId: &txID}
				return clientset.V30().CreatePeer(ctx, params, m)
			},
			func(m v32ee.PeerSection) (*http.Response, error) {
				params := &v32ee.CreatePeerParams{TransactionId: &txID}
				return clientset.V32EE().CreatePeer(ctx, params, m)
			},
			func(m v31ee.PeerSection) (*http.Response, error) {
				params := &v31ee.CreatePeerParams{TransactionId: &txID}
				return clientset.V31EE().CreatePeer(ctx, params, m)
			},
			func(m v30ee.PeerSection) (*http.Response, error) {
				params := &v30ee.CreatePeerParams{TransactionId: &txID}
				return clientset.V30EE().CreatePeer(ctx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "peer section creation")
	}
}

// PeerSectionUpdate returns an executor for updating peer sections.
// Note: The HAProxy Dataplane API does not support updating peer sections directly.
func PeerSectionUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.PeerSection, name string) error {
	return func(_ context.Context, _ *client.DataplaneClient, _ string, _ *models.PeerSection, name string) error {
		return fmt.Errorf("peer section updates are not supported by HAProxy Dataplane API (section: %s)", name)
	}
}

// PeerSectionDelete returns an executor for deleting peer sections.
func PeerSectionDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.PeerSection, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.PeerSection, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v33.DeletePeerParams{TransactionId: &txID}
				return clientset.V33().DeletePeer(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32.DeletePeerParams{TransactionId: &txID}
				return clientset.V32().DeletePeer(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeletePeerParams{TransactionId: &txID}
				return clientset.V31().DeletePeer(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeletePeerParams{TransactionId: &txID}
				return clientset.V30().DeletePeer(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeletePeerParams{TransactionId: &txID}
				return clientset.V32EE().DeletePeer(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeletePeerParams{TransactionId: &txID}
				return clientset.V31EE().DeletePeer(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeletePeerParams{TransactionId: &txID}
				return clientset.V30EE().DeletePeer(ctx, n, params)
			},
		)
		return dispatchAndCheck(resp, err, "peer section deletion")
	}
}
