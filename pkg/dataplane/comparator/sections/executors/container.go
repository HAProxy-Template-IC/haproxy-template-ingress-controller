// Package executors provides pre-built executor functions for HAProxy configuration operations.
package executors

import (
	"context"
	"net/http"

	"github.com/haproxytech/client-native/v6/models"

	"haptic/pkg/dataplane/client"
	v30 "haptic/pkg/generated/dataplaneapi/v30"
	v30ee "haptic/pkg/generated/dataplaneapi/v30ee"
	v31 "haptic/pkg/generated/dataplaneapi/v31"
	v31ee "haptic/pkg/generated/dataplaneapi/v31ee"
	v32 "haptic/pkg/generated/dataplaneapi/v32"
	v32ee "haptic/pkg/generated/dataplaneapi/v32ee"
)

// =============================================================================
// User Executors (Userlist container)
// =============================================================================

// UserCreate returns an executor for creating users in userlists.
func UserCreate(userlistName string) func(ctx context.Context, c *client.DataplaneClient, txID string, containerName string, childName string, model *models.User) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ string, _ string, model *models.User) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v32.User) (*http.Response, error) {
				params := &v32.CreateUserParams{TransactionId: &txID, Userlist: userlistName}
				return clientset.V32().CreateUser(ctx, params, m)
			},
			func(m v31.User) (*http.Response, error) {
				params := &v31.CreateUserParams{TransactionId: &txID, Userlist: userlistName}
				return clientset.V31().CreateUser(ctx, params, m)
			},
			func(m v30.User) (*http.Response, error) {
				params := &v30.CreateUserParams{TransactionId: &txID, Userlist: userlistName}
				return clientset.V30().CreateUser(ctx, params, m)
			},
			func(m v32ee.User) (*http.Response, error) {
				params := &v32ee.CreateUserParams{TransactionId: &txID, Userlist: userlistName}
				return clientset.V32EE().CreateUser(ctx, params, m)
			},
			func(m v31ee.User) (*http.Response, error) {
				params := &v31ee.CreateUserParams{TransactionId: &txID, Userlist: userlistName}
				return clientset.V31EE().CreateUser(ctx, params, m)
			},
			func(m v30ee.User) (*http.Response, error) {
				params := &v30ee.CreateUserParams{TransactionId: &txID, Userlist: userlistName}
				return clientset.V30EE().CreateUser(ctx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "user creation")
	}
}

// UserUpdate returns an executor for updating users in userlists.
func UserUpdate(userlistName string) func(ctx context.Context, c *client.DataplaneClient, txID string, containerName string, childName string, model *models.User) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ string, childName string, model *models.User) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, childName, model,
			func(name string, m v32.User) (*http.Response, error) {
				params := &v32.ReplaceUserParams{TransactionId: &txID, Userlist: userlistName}
				return clientset.V32().ReplaceUser(ctx, name, params, m)
			},
			func(name string, m v31.User) (*http.Response, error) {
				params := &v31.ReplaceUserParams{TransactionId: &txID, Userlist: userlistName}
				return clientset.V31().ReplaceUser(ctx, name, params, m)
			},
			func(name string, m v30.User) (*http.Response, error) {
				params := &v30.ReplaceUserParams{TransactionId: &txID, Userlist: userlistName}
				return clientset.V30().ReplaceUser(ctx, name, params, m)
			},
			func(name string, m v32ee.User) (*http.Response, error) {
				params := &v32ee.ReplaceUserParams{TransactionId: &txID, Userlist: userlistName}
				return clientset.V32EE().ReplaceUser(ctx, name, params, m)
			},
			func(name string, m v31ee.User) (*http.Response, error) {
				params := &v31ee.ReplaceUserParams{TransactionId: &txID, Userlist: userlistName}
				return clientset.V31EE().ReplaceUser(ctx, name, params, m)
			},
			func(name string, m v30ee.User) (*http.Response, error) {
				params := &v30ee.ReplaceUserParams{TransactionId: &txID, Userlist: userlistName}
				return clientset.V30EE().ReplaceUser(ctx, name, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "user update")
	}
}

// UserDelete returns an executor for deleting users from userlists.
func UserDelete(userlistName string) func(ctx context.Context, c *client.DataplaneClient, txID string, containerName string, childName string, model *models.User) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ string, childName string, _ *models.User) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, childName,
			func(name string) (*http.Response, error) {
				params := &v32.DeleteUserParams{TransactionId: &txID, Userlist: userlistName}
				return clientset.V32().DeleteUser(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v31.DeleteUserParams{TransactionId: &txID, Userlist: userlistName}
				return clientset.V31().DeleteUser(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v30.DeleteUserParams{TransactionId: &txID, Userlist: userlistName}
				return clientset.V30().DeleteUser(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v32ee.DeleteUserParams{TransactionId: &txID, Userlist: userlistName}
				return clientset.V32EE().DeleteUser(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v31ee.DeleteUserParams{TransactionId: &txID, Userlist: userlistName}
				return clientset.V31EE().DeleteUser(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v30ee.DeleteUserParams{TransactionId: &txID, Userlist: userlistName}
				return clientset.V30EE().DeleteUser(ctx, name, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "user deletion")
	}
}

// =============================================================================
// Mailer Entry Executors (Mailers container)
// =============================================================================

// MailerEntryCreate returns an executor for creating mailer entries in mailers sections.
func MailerEntryCreate(mailersName string) func(ctx context.Context, c *client.DataplaneClient, txID string, containerName string, childName string, model *models.MailerEntry) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ string, _ string, model *models.MailerEntry) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v32.MailerEntry) (*http.Response, error) {
				params := &v32.CreateMailerEntryParams{TransactionId: &txID, MailersSection: mailersName}
				return clientset.V32().CreateMailerEntry(ctx, params, m)
			},
			func(m v31.MailerEntry) (*http.Response, error) {
				params := &v31.CreateMailerEntryParams{TransactionId: &txID, MailersSection: mailersName}
				return clientset.V31().CreateMailerEntry(ctx, params, m)
			},
			func(m v30.MailerEntry) (*http.Response, error) {
				params := &v30.CreateMailerEntryParams{TransactionId: &txID, MailersSection: mailersName}
				return clientset.V30().CreateMailerEntry(ctx, params, m)
			},
			func(m v32ee.MailerEntry) (*http.Response, error) {
				params := &v32ee.CreateMailerEntryParams{TransactionId: &txID, MailersSection: mailersName}
				return clientset.V32EE().CreateMailerEntry(ctx, params, m)
			},
			func(m v31ee.MailerEntry) (*http.Response, error) {
				params := &v31ee.CreateMailerEntryParams{TransactionId: &txID, MailersSection: mailersName}
				return clientset.V31EE().CreateMailerEntry(ctx, params, m)
			},
			func(m v30ee.MailerEntry) (*http.Response, error) {
				params := &v30ee.CreateMailerEntryParams{TransactionId: &txID, MailersSection: mailersName}
				return clientset.V30EE().CreateMailerEntry(ctx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "mailer entry creation")
	}
}

// MailerEntryUpdate returns an executor for updating mailer entries in mailers sections.
func MailerEntryUpdate(mailersName string) func(ctx context.Context, c *client.DataplaneClient, txID string, containerName string, childName string, model *models.MailerEntry) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ string, childName string, model *models.MailerEntry) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, childName, model,
			func(name string, m v32.MailerEntry) (*http.Response, error) {
				params := &v32.ReplaceMailerEntryParams{TransactionId: &txID, MailersSection: mailersName}
				return clientset.V32().ReplaceMailerEntry(ctx, name, params, m)
			},
			func(name string, m v31.MailerEntry) (*http.Response, error) {
				params := &v31.ReplaceMailerEntryParams{TransactionId: &txID, MailersSection: mailersName}
				return clientset.V31().ReplaceMailerEntry(ctx, name, params, m)
			},
			func(name string, m v30.MailerEntry) (*http.Response, error) {
				params := &v30.ReplaceMailerEntryParams{TransactionId: &txID, MailersSection: mailersName}
				return clientset.V30().ReplaceMailerEntry(ctx, name, params, m)
			},
			func(name string, m v32ee.MailerEntry) (*http.Response, error) {
				params := &v32ee.ReplaceMailerEntryParams{TransactionId: &txID, MailersSection: mailersName}
				return clientset.V32EE().ReplaceMailerEntry(ctx, name, params, m)
			},
			func(name string, m v31ee.MailerEntry) (*http.Response, error) {
				params := &v31ee.ReplaceMailerEntryParams{TransactionId: &txID, MailersSection: mailersName}
				return clientset.V31EE().ReplaceMailerEntry(ctx, name, params, m)
			},
			func(name string, m v30ee.MailerEntry) (*http.Response, error) {
				params := &v30ee.ReplaceMailerEntryParams{TransactionId: &txID, MailersSection: mailersName}
				return clientset.V30EE().ReplaceMailerEntry(ctx, name, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "mailer entry update")
	}
}

// MailerEntryDelete returns an executor for deleting mailer entries from mailers sections.
func MailerEntryDelete(mailersName string) func(ctx context.Context, c *client.DataplaneClient, txID string, containerName string, childName string, model *models.MailerEntry) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ string, childName string, _ *models.MailerEntry) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, childName,
			func(name string) (*http.Response, error) {
				params := &v32.DeleteMailerEntryParams{TransactionId: &txID, MailersSection: mailersName}
				return clientset.V32().DeleteMailerEntry(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v31.DeleteMailerEntryParams{TransactionId: &txID, MailersSection: mailersName}
				return clientset.V31().DeleteMailerEntry(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v30.DeleteMailerEntryParams{TransactionId: &txID, MailersSection: mailersName}
				return clientset.V30().DeleteMailerEntry(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v32ee.DeleteMailerEntryParams{TransactionId: &txID, MailersSection: mailersName}
				return clientset.V32EE().DeleteMailerEntry(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v31ee.DeleteMailerEntryParams{TransactionId: &txID, MailersSection: mailersName}
				return clientset.V31EE().DeleteMailerEntry(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v30ee.DeleteMailerEntryParams{TransactionId: &txID, MailersSection: mailersName}
				return clientset.V30EE().DeleteMailerEntry(ctx, name, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "mailer entry deletion")
	}
}

// =============================================================================
// Peer Entry Executors (Peer container)
// =============================================================================

// PeerEntryCreate returns an executor for creating peer entries in peers sections.
func PeerEntryCreate(peerSectionName string) func(ctx context.Context, c *client.DataplaneClient, txID string, containerName string, childName string, model *models.PeerEntry) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ string, _ string, model *models.PeerEntry) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v32.PeerEntry) (*http.Response, error) {
				params := &v32.CreatePeerEntryParams{TransactionId: &txID, PeerSection: peerSectionName}
				return clientset.V32().CreatePeerEntry(ctx, params, m)
			},
			func(m v31.PeerEntry) (*http.Response, error) {
				params := &v31.CreatePeerEntryParams{TransactionId: &txID, PeerSection: peerSectionName}
				return clientset.V31().CreatePeerEntry(ctx, params, m)
			},
			func(m v30.PeerEntry) (*http.Response, error) {
				params := &v30.CreatePeerEntryParams{TransactionId: &txID, PeerSection: peerSectionName}
				return clientset.V30().CreatePeerEntry(ctx, params, m)
			},
			func(m v32ee.PeerEntry) (*http.Response, error) {
				params := &v32ee.CreatePeerEntryParams{TransactionId: &txID, PeerSection: peerSectionName}
				return clientset.V32EE().CreatePeerEntry(ctx, params, m)
			},
			func(m v31ee.PeerEntry) (*http.Response, error) {
				params := &v31ee.CreatePeerEntryParams{TransactionId: &txID, PeerSection: peerSectionName}
				return clientset.V31EE().CreatePeerEntry(ctx, params, m)
			},
			func(m v30ee.PeerEntry) (*http.Response, error) {
				params := &v30ee.CreatePeerEntryParams{TransactionId: &txID, PeerSection: peerSectionName}
				return clientset.V30EE().CreatePeerEntry(ctx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "peer entry creation")
	}
}

// PeerEntryUpdate returns an executor for updating peer entries in peers sections.
func PeerEntryUpdate(peerSectionName string) func(ctx context.Context, c *client.DataplaneClient, txID string, containerName string, childName string, model *models.PeerEntry) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ string, childName string, model *models.PeerEntry) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, childName, model,
			func(name string, m v32.PeerEntry) (*http.Response, error) {
				params := &v32.ReplacePeerEntryParams{TransactionId: &txID, PeerSection: peerSectionName}
				return clientset.V32().ReplacePeerEntry(ctx, name, params, m)
			},
			func(name string, m v31.PeerEntry) (*http.Response, error) {
				params := &v31.ReplacePeerEntryParams{TransactionId: &txID, PeerSection: peerSectionName}
				return clientset.V31().ReplacePeerEntry(ctx, name, params, m)
			},
			func(name string, m v30.PeerEntry) (*http.Response, error) {
				params := &v30.ReplacePeerEntryParams{TransactionId: &txID, PeerSection: peerSectionName}
				return clientset.V30().ReplacePeerEntry(ctx, name, params, m)
			},
			func(name string, m v32ee.PeerEntry) (*http.Response, error) {
				params := &v32ee.ReplacePeerEntryParams{TransactionId: &txID, PeerSection: peerSectionName}
				return clientset.V32EE().ReplacePeerEntry(ctx, name, params, m)
			},
			func(name string, m v31ee.PeerEntry) (*http.Response, error) {
				params := &v31ee.ReplacePeerEntryParams{TransactionId: &txID, PeerSection: peerSectionName}
				return clientset.V31EE().ReplacePeerEntry(ctx, name, params, m)
			},
			func(name string, m v30ee.PeerEntry) (*http.Response, error) {
				params := &v30ee.ReplacePeerEntryParams{TransactionId: &txID, PeerSection: peerSectionName}
				return clientset.V30EE().ReplacePeerEntry(ctx, name, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "peer entry update")
	}
}

// PeerEntryDelete returns an executor for deleting peer entries from peers sections.
func PeerEntryDelete(peerSectionName string) func(ctx context.Context, c *client.DataplaneClient, txID string, containerName string, childName string, model *models.PeerEntry) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ string, childName string, _ *models.PeerEntry) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, childName,
			func(name string) (*http.Response, error) {
				params := &v32.DeletePeerEntryParams{TransactionId: &txID, PeerSection: peerSectionName}
				return clientset.V32().DeletePeerEntry(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v31.DeletePeerEntryParams{TransactionId: &txID, PeerSection: peerSectionName}
				return clientset.V31().DeletePeerEntry(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v30.DeletePeerEntryParams{TransactionId: &txID, PeerSection: peerSectionName}
				return clientset.V30().DeletePeerEntry(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v32ee.DeletePeerEntryParams{TransactionId: &txID, PeerSection: peerSectionName}
				return clientset.V32EE().DeletePeerEntry(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v31ee.DeletePeerEntryParams{TransactionId: &txID, PeerSection: peerSectionName}
				return clientset.V31EE().DeletePeerEntry(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v30ee.DeletePeerEntryParams{TransactionId: &txID, PeerSection: peerSectionName}
				return clientset.V30EE().DeletePeerEntry(ctx, name, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "peer entry deletion")
	}
}

// =============================================================================
// Nameserver Executors (Resolver container)
// =============================================================================

// NameserverCreate returns an executor for creating nameservers in resolver sections.
func NameserverCreate(resolverName string) func(ctx context.Context, c *client.DataplaneClient, txID string, containerName string, childName string, model *models.Nameserver) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ string, _ string, model *models.Nameserver) error {
		clientset := c.Clientset()

		resp, err := client.DispatchCreate(ctx, c, model,
			func(m v32.Nameserver) (*http.Response, error) {
				params := &v32.CreateNameserverParams{TransactionId: &txID, Resolver: resolverName}
				return clientset.V32().CreateNameserver(ctx, params, m)
			},
			func(m v31.Nameserver) (*http.Response, error) {
				params := &v31.CreateNameserverParams{TransactionId: &txID, Resolver: resolverName}
				return clientset.V31().CreateNameserver(ctx, params, m)
			},
			func(m v30.Nameserver) (*http.Response, error) {
				params := &v30.CreateNameserverParams{TransactionId: &txID, Resolver: resolverName}
				return clientset.V30().CreateNameserver(ctx, params, m)
			},
			func(m v32ee.Nameserver) (*http.Response, error) {
				params := &v32ee.CreateNameserverParams{TransactionId: &txID, Resolver: resolverName}
				return clientset.V32EE().CreateNameserver(ctx, params, m)
			},
			func(m v31ee.Nameserver) (*http.Response, error) {
				params := &v31ee.CreateNameserverParams{TransactionId: &txID, Resolver: resolverName}
				return clientset.V31EE().CreateNameserver(ctx, params, m)
			},
			func(m v30ee.Nameserver) (*http.Response, error) {
				params := &v30ee.CreateNameserverParams{TransactionId: &txID, Resolver: resolverName}
				return clientset.V30EE().CreateNameserver(ctx, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "nameserver creation")
	}
}

// NameserverUpdate returns an executor for updating nameservers in resolver sections.
func NameserverUpdate(resolverName string) func(ctx context.Context, c *client.DataplaneClient, txID string, containerName string, childName string, model *models.Nameserver) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ string, childName string, model *models.Nameserver) error {
		clientset := c.Clientset()

		resp, err := client.DispatchUpdate(ctx, c, childName, model,
			func(name string, m v32.Nameserver) (*http.Response, error) {
				params := &v32.ReplaceNameserverParams{TransactionId: &txID, Resolver: resolverName}
				return clientset.V32().ReplaceNameserver(ctx, name, params, m)
			},
			func(name string, m v31.Nameserver) (*http.Response, error) {
				params := &v31.ReplaceNameserverParams{TransactionId: &txID, Resolver: resolverName}
				return clientset.V31().ReplaceNameserver(ctx, name, params, m)
			},
			func(name string, m v30.Nameserver) (*http.Response, error) {
				params := &v30.ReplaceNameserverParams{TransactionId: &txID, Resolver: resolverName}
				return clientset.V30().ReplaceNameserver(ctx, name, params, m)
			},
			func(name string, m v32ee.Nameserver) (*http.Response, error) {
				params := &v32ee.ReplaceNameserverParams{TransactionId: &txID, Resolver: resolverName}
				return clientset.V32EE().ReplaceNameserver(ctx, name, params, m)
			},
			func(name string, m v31ee.Nameserver) (*http.Response, error) {
				params := &v31ee.ReplaceNameserverParams{TransactionId: &txID, Resolver: resolverName}
				return clientset.V31EE().ReplaceNameserver(ctx, name, params, m)
			},
			func(name string, m v30ee.Nameserver) (*http.Response, error) {
				params := &v30ee.ReplaceNameserverParams{TransactionId: &txID, Resolver: resolverName}
				return clientset.V30EE().ReplaceNameserver(ctx, name, params, m)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "nameserver update")
	}
}

// NameserverDelete returns an executor for deleting nameservers from resolver sections.
func NameserverDelete(resolverName string) func(ctx context.Context, c *client.DataplaneClient, txID string, containerName string, childName string, model *models.Nameserver) error {
	return func(ctx context.Context, c *client.DataplaneClient, txID string, _ string, childName string, _ *models.Nameserver) error {
		clientset := c.Clientset()

		resp, err := client.DispatchDelete(ctx, c, childName,
			func(name string) (*http.Response, error) {
				params := &v32.DeleteNameserverParams{TransactionId: &txID, Resolver: resolverName}
				return clientset.V32().DeleteNameserver(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v31.DeleteNameserverParams{TransactionId: &txID, Resolver: resolverName}
				return clientset.V31().DeleteNameserver(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v30.DeleteNameserverParams{TransactionId: &txID, Resolver: resolverName}
				return clientset.V30().DeleteNameserver(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v32ee.DeleteNameserverParams{TransactionId: &txID, Resolver: resolverName}
				return clientset.V32EE().DeleteNameserver(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v31ee.DeleteNameserverParams{TransactionId: &txID, Resolver: resolverName}
				return clientset.V31EE().DeleteNameserver(ctx, name, params)
			},
			func(name string) (*http.Response, error) {
				params := &v30ee.DeleteNameserverParams{TransactionId: &txID, Resolver: resolverName}
				return clientset.V30EE().DeleteNameserver(ctx, name, params)
			},
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return client.CheckResponse(resp, "nameserver deletion")
	}
}
