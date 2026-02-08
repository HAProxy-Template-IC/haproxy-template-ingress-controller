// Package executors provides pre-built executor functions for HAProxy configuration operations.
//
// These functions encapsulate the dispatcher callback boilerplate, providing a clean
// interface between the generic operation types and the versioned DataPlane API clients.
package executors

import (
	"context"
	"net/http"

	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	v30 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30"
	v30ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30ee"
	v31 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31"
	v31ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31ee"
	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
)

// MailersSectionCreate returns an executor for creating mailers sections.
func MailersSectionCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.MailersSection, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.MailersSection, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v32.MailersSection) (*http.Response, error) {
				params := &v32.CreateMailersSectionParams{TransactionId: &txID}
				return clientset.V32().CreateMailersSection(ctx, params, m)
			},
			func(m v31.MailersSection) (*http.Response, error) {
				params := &v31.CreateMailersSectionParams{TransactionId: &txID}
				return clientset.V31().CreateMailersSection(ctx, params, m)
			},
			func(m v30.MailersSection) (*http.Response, error) {
				params := &v30.CreateMailersSectionParams{TransactionId: &txID}
				return clientset.V30().CreateMailersSection(ctx, params, m)
			},
			func(m v32ee.MailersSection) (*http.Response, error) {
				params := &v32ee.CreateMailersSectionParams{TransactionId: &txID}
				return clientset.V32EE().CreateMailersSection(ctx, params, m)
			},
			func(m v31ee.MailersSection) (*http.Response, error) {
				params := &v31ee.CreateMailersSectionParams{TransactionId: &txID}
				return clientset.V31EE().CreateMailersSection(ctx, params, m)
			},
			func(m v30ee.MailersSection) (*http.Response, error) {
				params := &v30ee.CreateMailersSectionParams{TransactionId: &txID}
				return clientset.V30EE().CreateMailersSection(ctx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "mailers section creation")
	}
}

// MailersSectionUpdate returns an executor for updating mailers sections.
func MailersSectionUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.MailersSection, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.MailersSection, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
			func(n string, m v32.MailersSection) (*http.Response, error) {
				params := &v32.EditMailersSectionParams{TransactionId: &txID}
				return clientset.V32().EditMailersSection(ctx, n, params, m)
			},
			func(n string, m v31.MailersSection) (*http.Response, error) {
				params := &v31.EditMailersSectionParams{TransactionId: &txID}
				return clientset.V31().EditMailersSection(ctx, n, params, m)
			},
			func(n string, m v30.MailersSection) (*http.Response, error) {
				params := &v30.EditMailersSectionParams{TransactionId: &txID}
				return clientset.V30().EditMailersSection(ctx, n, params, m)
			},
			func(n string, m v32ee.MailersSection) (*http.Response, error) {
				params := &v32ee.EditMailersSectionParams{TransactionId: &txID}
				return clientset.V32EE().EditMailersSection(ctx, n, params, m)
			},
			func(n string, m v31ee.MailersSection) (*http.Response, error) {
				params := &v31ee.EditMailersSectionParams{TransactionId: &txID}
				return clientset.V31EE().EditMailersSection(ctx, n, params, m)
			},
			func(n string, m v30ee.MailersSection) (*http.Response, error) {
				params := &v30ee.EditMailersSectionParams{TransactionId: &txID}
				return clientset.V30EE().EditMailersSection(ctx, n, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "mailers section update")
	}
}

// MailersSectionDelete returns an executor for deleting mailers sections.
func MailersSectionDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.MailersSection, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.MailersSection, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v32.DeleteMailersSectionParams{TransactionId: &txID}
				return clientset.V32().DeleteMailersSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteMailersSectionParams{TransactionId: &txID}
				return clientset.V31().DeleteMailersSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteMailersSectionParams{TransactionId: &txID}
				return clientset.V30().DeleteMailersSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteMailersSectionParams{TransactionId: &txID}
				return clientset.V32EE().DeleteMailersSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteMailersSectionParams{TransactionId: &txID}
				return clientset.V31EE().DeleteMailersSection(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteMailersSectionParams{TransactionId: &txID}
				return clientset.V30EE().DeleteMailersSection(ctx, n, params)
			},
		)
		return dispatchAndCheck(resp, err, "mailers section deletion")
	}
}

// ProgramCreate returns an executor for creating program sections.
func ProgramCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Program, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Program, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v32.Program) (*http.Response, error) {
				params := &v32.CreateProgramParams{TransactionId: &txID}
				return clientset.V32().CreateProgram(ctx, params, m)
			},
			func(m v31.Program) (*http.Response, error) {
				params := &v31.CreateProgramParams{TransactionId: &txID}
				return clientset.V31().CreateProgram(ctx, params, m)
			},
			func(m v30.Program) (*http.Response, error) {
				params := &v30.CreateProgramParams{TransactionId: &txID}
				return clientset.V30().CreateProgram(ctx, params, m)
			},
			func(m v32ee.Program) (*http.Response, error) {
				params := &v32ee.CreateProgramParams{TransactionId: &txID}
				return clientset.V32EE().CreateProgram(ctx, params, m)
			},
			func(m v31ee.Program) (*http.Response, error) {
				params := &v31ee.CreateProgramParams{TransactionId: &txID}
				return clientset.V31EE().CreateProgram(ctx, params, m)
			},
			func(m v30ee.Program) (*http.Response, error) {
				params := &v30ee.CreateProgramParams{TransactionId: &txID}
				return clientset.V30EE().CreateProgram(ctx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "program creation")
	}
}

// ProgramUpdate returns an executor for updating program sections.
func ProgramUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Program, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Program, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
			func(n string, m v32.Program) (*http.Response, error) {
				params := &v32.ReplaceProgramParams{TransactionId: &txID}
				return clientset.V32().ReplaceProgram(ctx, n, params, m)
			},
			func(n string, m v31.Program) (*http.Response, error) {
				params := &v31.ReplaceProgramParams{TransactionId: &txID}
				return clientset.V31().ReplaceProgram(ctx, n, params, m)
			},
			func(n string, m v30.Program) (*http.Response, error) {
				params := &v30.ReplaceProgramParams{TransactionId: &txID}
				return clientset.V30().ReplaceProgram(ctx, n, params, m)
			},
			func(n string, m v32ee.Program) (*http.Response, error) {
				params := &v32ee.ReplaceProgramParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceProgram(ctx, n, params, m)
			},
			func(n string, m v31ee.Program) (*http.Response, error) {
				params := &v31ee.ReplaceProgramParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceProgram(ctx, n, params, m)
			},
			func(n string, m v30ee.Program) (*http.Response, error) {
				params := &v30ee.ReplaceProgramParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceProgram(ctx, n, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "program update")
	}
}

// ProgramDelete returns an executor for deleting program sections.
func ProgramDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Program, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Program, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v32.DeleteProgramParams{TransactionId: &txID}
				return clientset.V32().DeleteProgram(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteProgramParams{TransactionId: &txID}
				return clientset.V31().DeleteProgram(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteProgramParams{TransactionId: &txID}
				return clientset.V30().DeleteProgram(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteProgramParams{TransactionId: &txID}
				return clientset.V32EE().DeleteProgram(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteProgramParams{TransactionId: &txID}
				return clientset.V31EE().DeleteProgram(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteProgramParams{TransactionId: &txID}
				return clientset.V30EE().DeleteProgram(ctx, n, params)
			},
		)
		return dispatchAndCheck(resp, err, "program deletion")
	}
}

// ResolverCreate returns an executor for creating resolver sections.
func ResolverCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Resolver, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Resolver, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v32.Resolver) (*http.Response, error) {
				params := &v32.CreateResolverParams{TransactionId: &txID}
				return clientset.V32().CreateResolver(ctx, params, m)
			},
			func(m v31.Resolver) (*http.Response, error) {
				params := &v31.CreateResolverParams{TransactionId: &txID}
				return clientset.V31().CreateResolver(ctx, params, m)
			},
			func(m v30.Resolver) (*http.Response, error) {
				params := &v30.CreateResolverParams{TransactionId: &txID}
				return clientset.V30().CreateResolver(ctx, params, m)
			},
			func(m v32ee.Resolver) (*http.Response, error) {
				params := &v32ee.CreateResolverParams{TransactionId: &txID}
				return clientset.V32EE().CreateResolver(ctx, params, m)
			},
			func(m v31ee.Resolver) (*http.Response, error) {
				params := &v31ee.CreateResolverParams{TransactionId: &txID}
				return clientset.V31EE().CreateResolver(ctx, params, m)
			},
			func(m v30ee.Resolver) (*http.Response, error) {
				params := &v30ee.CreateResolverParams{TransactionId: &txID}
				return clientset.V30EE().CreateResolver(ctx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "resolver creation")
	}
}

// ResolverUpdate returns an executor for updating resolver sections.
func ResolverUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Resolver, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Resolver, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
			func(n string, m v32.Resolver) (*http.Response, error) {
				params := &v32.ReplaceResolverParams{TransactionId: &txID}
				return clientset.V32().ReplaceResolver(ctx, n, params, m)
			},
			func(n string, m v31.Resolver) (*http.Response, error) {
				params := &v31.ReplaceResolverParams{TransactionId: &txID}
				return clientset.V31().ReplaceResolver(ctx, n, params, m)
			},
			func(n string, m v30.Resolver) (*http.Response, error) {
				params := &v30.ReplaceResolverParams{TransactionId: &txID}
				return clientset.V30().ReplaceResolver(ctx, n, params, m)
			},
			func(n string, m v32ee.Resolver) (*http.Response, error) {
				params := &v32ee.ReplaceResolverParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceResolver(ctx, n, params, m)
			},
			func(n string, m v31ee.Resolver) (*http.Response, error) {
				params := &v31ee.ReplaceResolverParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceResolver(ctx, n, params, m)
			},
			func(n string, m v30ee.Resolver) (*http.Response, error) {
				params := &v30ee.ReplaceResolverParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceResolver(ctx, n, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "resolver update")
	}
}

// ResolverDelete returns an executor for deleting resolver sections.
func ResolverDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Resolver, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Resolver, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v32.DeleteResolverParams{TransactionId: &txID}
				return clientset.V32().DeleteResolver(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteResolverParams{TransactionId: &txID}
				return clientset.V31().DeleteResolver(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteResolverParams{TransactionId: &txID}
				return clientset.V30().DeleteResolver(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteResolverParams{TransactionId: &txID}
				return clientset.V32EE().DeleteResolver(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteResolverParams{TransactionId: &txID}
				return clientset.V31EE().DeleteResolver(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteResolverParams{TransactionId: &txID}
				return clientset.V30EE().DeleteResolver(ctx, n, params)
			},
		)
		return dispatchAndCheck(resp, err, "resolver deletion")
	}
}

// RingCreate returns an executor for creating ring sections.
func RingCreate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Ring, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Ring, _ string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v32.Ring) (*http.Response, error) {
				params := &v32.CreateRingParams{TransactionId: &txID}
				return clientset.V32().CreateRing(ctx, params, m)
			},
			func(m v31.Ring) (*http.Response, error) {
				params := &v31.CreateRingParams{TransactionId: &txID}
				return clientset.V31().CreateRing(ctx, params, m)
			},
			func(m v30.Ring) (*http.Response, error) {
				params := &v30.CreateRingParams{TransactionId: &txID}
				return clientset.V30().CreateRing(ctx, params, m)
			},
			func(m v32ee.Ring) (*http.Response, error) {
				params := &v32ee.CreateRingParams{TransactionId: &txID}
				return clientset.V32EE().CreateRing(ctx, params, m)
			},
			func(m v31ee.Ring) (*http.Response, error) {
				params := &v31ee.CreateRingParams{TransactionId: &txID}
				return clientset.V31EE().CreateRing(ctx, params, m)
			},
			func(m v30ee.Ring) (*http.Response, error) {
				params := &v30ee.CreateRingParams{TransactionId: &txID}
				return clientset.V30EE().CreateRing(ctx, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "ring creation")
	}
}

// RingUpdate returns an executor for updating ring sections.
func RingUpdate() func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Ring, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, model *models.Ring, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, name, model,
			func(n string, m v32.Ring) (*http.Response, error) {
				params := &v32.ReplaceRingParams{TransactionId: &txID}
				return clientset.V32().ReplaceRing(ctx, n, params, m)
			},
			func(n string, m v31.Ring) (*http.Response, error) {
				params := &v31.ReplaceRingParams{TransactionId: &txID}
				return clientset.V31().ReplaceRing(ctx, n, params, m)
			},
			func(n string, m v30.Ring) (*http.Response, error) {
				params := &v30.ReplaceRingParams{TransactionId: &txID}
				return clientset.V30().ReplaceRing(ctx, n, params, m)
			},
			func(n string, m v32ee.Ring) (*http.Response, error) {
				params := &v32ee.ReplaceRingParams{TransactionId: &txID}
				return clientset.V32EE().ReplaceRing(ctx, n, params, m)
			},
			func(n string, m v31ee.Ring) (*http.Response, error) {
				params := &v31ee.ReplaceRingParams{TransactionId: &txID}
				return clientset.V31EE().ReplaceRing(ctx, n, params, m)
			},
			func(n string, m v30ee.Ring) (*http.Response, error) {
				params := &v30ee.ReplaceRingParams{TransactionId: &txID}
				return clientset.V30EE().ReplaceRing(ctx, n, params, m)
			},
		)
		return dispatchAndCheck(resp, err, "ring update")
	}
}

// RingDelete returns an executor for deleting ring sections.
func RingDelete() func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Ring, name string) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ *models.Ring, name string) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, name,
			func(n string) (*http.Response, error) {
				params := &v32.DeleteRingParams{TransactionId: &txID}
				return clientset.V32().DeleteRing(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31.DeleteRingParams{TransactionId: &txID}
				return clientset.V31().DeleteRing(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30.DeleteRingParams{TransactionId: &txID}
				return clientset.V30().DeleteRing(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v32ee.DeleteRingParams{TransactionId: &txID}
				return clientset.V32EE().DeleteRing(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v31ee.DeleteRingParams{TransactionId: &txID}
				return clientset.V31EE().DeleteRing(ctx, n, params)
			},
			func(n string) (*http.Response, error) {
				params := &v30ee.DeleteRingParams{TransactionId: &txID}
				return clientset.V30EE().DeleteRing(ctx, n, params)
			},
		)
		return dispatchAndCheck(resp, err, "ring deletion")
	}
}
