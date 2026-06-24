package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"

	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
	v33 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v33"
)

// runtimeCert is a loaded SSL cert as reported by `show ssl cert` (via the
// DataPlane API's getAllCerts): storageName is the path HAProxy loaded it under
// (the exact id ReplaceCert expects), description is the basename.
type runtimeCert struct {
	description string
	storageName string
}

// ReplaceRuntimeSSLCerts replaces the live (in-memory) contents of one or more
// already-loaded SSL certificates on the worker via the runtime API (set ssl
// cert + commit ssl cert, applied atomically per cert by the DataPlane API),
// WITHOUT a reload. pemByName maps each cert's config identifier to its new PEM.
// Available on DataPlane API v3.2+ only — callers must gate on
// Capabilities().SupportsRuntimeSSLCerts; older versions take the reload path.
//
// The loaded-cert list is fetched ONCE and reused to resolve every cert's
// runtime identifier, so N rotations cost a single list fetch, not N.
//
// Like ReplaceRuntimeMap, disk durability is left to the orchestrator's
// pre-config storage write (skip_reload); this updates worker memory only and
// carries no force_sync.
func (c *DataplaneClient) ReplaceRuntimeSSLCerts(ctx context.Context, pemByName map[string]string) error {
	if len(pemByName) == 0 {
		return nil
	}

	loaded, err := c.listRuntimeSSLCerts(ctx)
	if err != nil {
		return err
	}
	for name, pem := range pemByName {
		ident, err := resolveRuntimeSSLCertID(loaded, name)
		if err != nil {
			return err
		}
		if err := c.replaceRuntimeSSLCert(ctx, name, ident, pem); err != nil {
			return err
		}
	}
	return nil
}

// replaceRuntimeSSLCert pushes one cert's new PEM to the already-resolved
// runtime identifier. HAProxy refuses to replace a cert that is "not referenced
// by the configuration", and identifies it by the exact path it loaded it under
// (ident), hence the separate resolution step.
func (c *DataplaneClient) replaceRuntimeSSLCert(ctx context.Context, name, ident, pem string) error {
	body, contentType, err := buildMultipartFilePayload(path.Base(ident), pem)
	if err != nil {
		return fmt.Errorf("building payload for runtime ssl cert '%s': %w", name, err)
	}

	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V33: func(c *v33.Client) (*http.Response, error) {
			return c.ReplaceCertWithBody(ctx, ident, contentType, body)
		},
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.ReplaceCertWithBody(ctx, ident, contentType, body)
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.ReplaceCertWithBody(ctx, ident, contentType, body)
		},
	}, requireRuntimeSSLCerts)
	if err != nil {
		return fmt.Errorf("replacing runtime ssl cert '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if _, err := checkUpdateResponse(resp, "runtime ssl certificate", name); err != nil {
		return err
	}
	return nil
}

// listRuntimeSSLCerts returns the certs currently loaded by the worker.
func (c *DataplaneClient) listRuntimeSSLCerts(ctx context.Context) ([]runtimeCert, error) {
	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
		V33:   func(c *v33.Client) (*http.Response, error) { return c.GetAllCerts(ctx) },
		V32:   func(c *v32.Client) (*http.Response, error) { return c.GetAllCerts(ctx) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.GetAllCerts(ctx) },
	}, requireRuntimeSSLCerts)
	if err != nil {
		return nil, fmt.Errorf("listing runtime ssl certs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("listing runtime ssl certs failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var raw []struct {
		Description *string `json:"description"`
		StorageName *string `json:"storage_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding runtime ssl certs: %w", err)
	}
	certs := make([]runtimeCert, 0, len(raw))
	for _, cert := range raw {
		certs = append(certs, runtimeCert{description: derefStr(cert.Description), storageName: derefStr(cert.StorageName)})
	}
	return certs, nil
}

// resolveRuntimeSSLCertID returns the storage_name (the id ReplaceCert expects)
// of the loaded cert matching name. An exact storage-path match wins outright;
// otherwise it falls back to a basename/description match but only when that is
// UNAMBIGUOUS — if two certs loaded from different dirs share a basename, it
// errors rather than silently picking the wrong one (the caller then reloads,
// which converges on the correct cert). Pure function so the matching is
// unit-tested directly.
func resolveRuntimeSSLCertID(certs []runtimeCert, name string) (string, error) {
	want := SanitizeSSLCertName(name)

	var basenameMatch string
	matches := 0
	for _, cert := range certs {
		if cert.storageName == want { // exact storage path — unambiguous
			return cert.storageName, nil
		}
		if cert.description == want || path.Base(cert.storageName) == want {
			basenameMatch = cert.storageName
			matches++
		}
	}
	switch {
	case matches == 1:
		return basenameMatch, nil
	case matches > 1:
		return "", fmt.Errorf("runtime ssl cert %q (%q) is ambiguous: %d loaded certs share that basename", name, want, matches)
	default:
		return "", fmt.Errorf("runtime ssl cert %q (%q) is not loaded", name, want)
	}
}

func requireRuntimeSSLCerts(caps Capabilities) error {
	if !caps.SupportsRuntimeSSLCerts {
		return fmt.Errorf("runtime SSL certificate update requires DataPlane API v3.2+")
	}
	return nil
}
