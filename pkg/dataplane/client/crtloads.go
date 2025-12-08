package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	v32 "haproxy-template-ic/pkg/generated/dataplaneapi/v32"
	v32ee "haproxy-template-ic/pkg/generated/dataplaneapi/v32ee"
)

// CrtLoad represents a certificate load configuration within a crt-store.
// It specifies how HAProxy loads and manages certificates from a certificate store.
type CrtLoad struct {
	// Certificate is the certificate filename (required, acts as identifier).
	Certificate string `json:"certificate"`

	// Acme is the ACME section name to use for automatic certificate generation.
	Acme string `json:"acme,omitempty"`

	// Alias is an optional certificate alias.
	Alias string `json:"alias,omitempty"`

	// Domains is a list of domains for ACME certificate generation.
	Domains []string `json:"domains,omitempty"`

	// Issuer is the OCSP issuer filename.
	Issuer string `json:"issuer,omitempty"`

	// Key is the private key filename.
	Key string `json:"key,omitempty"`

	// Ocsp is the OCSP response filename.
	Ocsp string `json:"ocsp,omitempty"`

	// OcspUpdate enables automatic OCSP response updates ("enabled" or "disabled").
	OcspUpdate string `json:"ocsp_update,omitempty"`

	// Sctl is the Signed Certificate Timestamp List filename.
	Sctl string `json:"sctl,omitempty"`
}

// crtLoadAPIResponse represents the API response format with pointer fields.
type crtLoadAPIResponse struct {
	Certificate *string   `json:"certificate"`
	Acme        *string   `json:"acme"`
	Alias       *string   `json:"alias"`
	Domains     *[]string `json:"domains"`
	Issuer      *string   `json:"issuer"`
	Key         *string   `json:"key"`
	Ocsp        *string   `json:"ocsp"`
	OcspUpdate  *string   `json:"ocsp_update"`
	Sctl        *string   `json:"sctl"`
}

// toCrtLoad converts an API response to a CrtLoad.
func (r *crtLoadAPIResponse) toCrtLoad() CrtLoad {
	load := CrtLoad{}
	if r.Certificate != nil {
		load.Certificate = *r.Certificate
	}
	if r.Acme != nil {
		load.Acme = *r.Acme
	}
	if r.Alias != nil {
		load.Alias = *r.Alias
	}
	if r.Domains != nil {
		load.Domains = *r.Domains
	}
	if r.Issuer != nil {
		load.Issuer = *r.Issuer
	}
	if r.Key != nil {
		load.Key = *r.Key
	}
	if r.Ocsp != nil {
		load.Ocsp = *r.Ocsp
	}
	if r.OcspUpdate != nil {
		load.OcspUpdate = *r.OcspUpdate
	}
	if r.Sctl != nil {
		load.Sctl = *r.Sctl
	}
	return load
}

// GetAllCrtLoads retrieves all crt-load entries from a specific crt-store.
// CRT loads are only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) GetAllCrtLoads(ctx context.Context, crtStoreName string) ([]CrtLoad, error) {
	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			params := &v32.GetCrtLoadsParams{CrtStore: crtStoreName}
			return c.GetCrtLoads(ctx, params)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetCrtLoadsParams{CrtStore: crtStoreName}
			return c.GetCrtLoads(ctx, params)
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsCrtList {
			return fmt.Errorf("crt-loads require DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get crt-loads for crt-store '%s': %w", crtStoreName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get crt-loads failed with status %d", resp.StatusCode)
	}

	var apiLoads []crtLoadAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiLoads); err != nil {
		return nil, fmt.Errorf("failed to decode crt-loads response: %w", err)
	}

	loads := make([]CrtLoad, 0, len(apiLoads))
	for i := range apiLoads {
		loads = append(loads, apiLoads[i].toCrtLoad())
	}

	return loads, nil
}

// GetCrtLoad retrieves a specific crt-load by certificate name from a crt-store.
// CRT loads are only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) GetCrtLoad(ctx context.Context, crtStoreName, certificate string) (*CrtLoad, error) {
	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			params := &v32.GetCrtLoadParams{CrtStore: crtStoreName}
			return c.GetCrtLoad(ctx, certificate, params)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.GetCrtLoadParams{CrtStore: crtStoreName}
			return c.GetCrtLoad(ctx, certificate, params)
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsCrtList {
			return fmt.Errorf("crt-loads require DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get crt-load '%s' from crt-store '%s': %w", certificate, crtStoreName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("crt-load '%s' not found in crt-store '%s'", certificate, crtStoreName)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get crt-load failed with status %d", resp.StatusCode)
	}

	var load CrtLoad
	if err := json.NewDecoder(resp.Body).Decode(&load); err != nil {
		return nil, fmt.Errorf("failed to decode crt-load response: %w", err)
	}

	return &load, nil
}

// CreateCrtLoad creates a new crt-load entry in a crt-store.
// CRT loads are only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) CreateCrtLoad(ctx context.Context, crtStoreName string, load *CrtLoad, transactionID string) error {
	if !c.clientset.Capabilities().SupportsCrtList {
		return fmt.Errorf("crt-loads are not supported by DataPlane API version %s (requires v3.2+)", c.clientset.DetectedVersion())
	}

	jsonData, err := json.Marshal(load)
	if err != nil {
		return fmt.Errorf("failed to marshal crt-load '%s': %w", load.Certificate, err)
	}

	body := bytes.NewReader(jsonData)

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			params := &v32.CreateCrtLoadParams{
				CrtStore:      crtStoreName,
				TransactionId: &transactionID,
			}
			return c.CreateCrtLoadWithBody(ctx, params, "application/json", body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.CreateCrtLoadParams{
				CrtStore:      crtStoreName,
				TransactionId: &transactionID,
			}
			return c.CreateCrtLoadWithBody(ctx, params, "application/json", body)
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsCrtList {
			return fmt.Errorf("crt-loads require DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to create crt-load '%s' in crt-store '%s': %w", load.Certificate, crtStoreName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create crt-load failed with status %d", resp.StatusCode)
	}

	return nil
}

// ReplaceCrtLoad replaces an existing crt-load entry in a crt-store.
// CRT loads are only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) ReplaceCrtLoad(ctx context.Context, crtStoreName, certificate string, load *CrtLoad, transactionID string) error {
	jsonData, err := json.Marshal(load)
	if err != nil {
		return fmt.Errorf("failed to marshal crt-load '%s': %w", certificate, err)
	}

	body := bytes.NewReader(jsonData)

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			params := &v32.ReplaceCrtLoadParams{
				CrtStore:      crtStoreName,
				TransactionId: &transactionID,
			}
			return c.ReplaceCrtLoadWithBody(ctx, certificate, params, "application/json", body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.ReplaceCrtLoadParams{
				CrtStore:      crtStoreName,
				TransactionId: &transactionID,
			}
			return c.ReplaceCrtLoadWithBody(ctx, certificate, params, "application/json", body)
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsCrtList {
			return fmt.Errorf("crt-loads require DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to replace crt-load '%s' in crt-store '%s': %w", certificate, crtStoreName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("replace crt-load failed with status %d", resp.StatusCode)
	}

	return nil
}

// DeleteCrtLoad deletes a crt-load entry from a crt-store.
// CRT loads are only available in HAProxy DataPlane API v3.2+.
func (c *DataplaneClient) DeleteCrtLoad(ctx context.Context, crtStoreName, certificate, transactionID string) error {
	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			params := &v32.DeleteCrtLoadParams{
				CrtStore:      crtStoreName,
				TransactionId: &transactionID,
			}
			return c.DeleteCrtLoad(ctx, certificate, params)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			params := &v32ee.DeleteCrtLoadParams{
				CrtStore:      crtStoreName,
				TransactionId: &transactionID,
			}
			return c.DeleteCrtLoad(ctx, certificate, params)
		},
	}, func(caps Capabilities) error {
		if !caps.SupportsCrtList {
			return fmt.Errorf("crt-loads require DataPlane API v3.2+")
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to delete crt-load '%s' from crt-store '%s': %w", certificate, crtStoreName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Already deleted, not an error
		return nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete crt-load failed with status %d", resp.StatusCode)
	}

	return nil
}
