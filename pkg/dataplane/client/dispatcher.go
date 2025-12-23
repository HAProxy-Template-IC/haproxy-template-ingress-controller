package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	v30 "haptic/pkg/generated/dataplaneapi/v30"
	v30ee "haptic/pkg/generated/dataplaneapi/v30ee"
	v31 "haptic/pkg/generated/dataplaneapi/v31"
	v31ee "haptic/pkg/generated/dataplaneapi/v31ee"
	v32 "haptic/pkg/generated/dataplaneapi/v32"
	v32ee "haptic/pkg/generated/dataplaneapi/v32ee"
)

// ErrEnterpriseRequired is returned when an enterprise-only operation is attempted
// on a HAProxy Community edition instance.
var ErrEnterpriseRequired = errors.New("this operation requires HAProxy Enterprise edition")

// CallFunc represents a versioned API call function.
// Each field is a function that takes a version-specific client and returns a result of type T.
// This allows type-safe dispatch to the appropriate client version based on runtime detection.
//
// For HAProxy Community editions, use V30, V31, V32.
// For HAProxy Enterprise editions, use V30EE, V31EE, V32EE.
//
// Example usage:
//
//	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
//	    V32: func(c *v32.Client) (*http.Response, error) { return c.SomeMethod(ctx, params) },
//	    V31: func(c *v31.Client) (*http.Response, error) { return c.SomeMethod(ctx, params) },
//	    V30: func(c *v30.Client) (*http.Response, error) { return c.SomeMethod(ctx, params) },
//	    V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.SomeMethod(ctx, params) },
//	    V31EE: func(c *v31ee.Client) (*http.Response, error) { return c.SomeMethod(ctx, params) },
//	    V30EE: func(c *v30ee.Client) (*http.Response, error) { return c.SomeMethod(ctx, params) },
//	})
type CallFunc[T any] struct {
	// Community edition clients
	// V32 is the function to call for DataPlane API v3.2+
	V32 func(*v32.Client) (T, error)

	// V31 is the function to call for DataPlane API v3.1
	V31 func(*v31.Client) (T, error)

	// V30 is the function to call for DataPlane API v3.0
	V30 func(*v30.Client) (T, error)

	// Enterprise edition clients
	// V32EE is the function to call for HAProxy Enterprise DataPlane API v3.2+
	V32EE func(*v32ee.Client) (T, error)

	// V31EE is the function to call for HAProxy Enterprise DataPlane API v3.1
	V31EE func(*v31ee.Client) (T, error)

	// V30EE is the function to call for HAProxy Enterprise DataPlane API v3.0
	V30EE func(*v30ee.Client) (T, error)
}

// Dispatch executes the appropriate versioned function based on the detected API version.
// This is the primary method for executing API calls that work across all versions.
//
// Returns error if:
//   - The client type is unexpected
//   - The version-specific function is nil
//   - The versioned function itself returns an error
//
// Example:
//
//	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
//	    V32: func(c *v32.Client) (*http.Response, error) {
//	        return c.GetAllStorageMapFiles(ctx)
//	    },
//	    V31: func(c *v31.Client) (*http.Response, error) {
//	        return c.GetAllStorageMapFiles(ctx)
//	    },
//	    V30: func(c *v30.Client) (*http.Response, error) {
//	        return c.GetAllStorageMapFiles(ctx)
//	    },
//	})
func (c *DataplaneClient) Dispatch(ctx context.Context, call CallFunc[*http.Response]) (*http.Response, error) {
	return c.DispatchWithCapability(ctx, call, nil)
}

// DispatchWithCapability executes the appropriate versioned function with an optional capability check.
// Use this for version-specific features (e.g., crt-list only available in v3.2+).
//
// The capability check is performed before executing the versioned function. If the check fails,
// the function is not executed and the capability error is returned.
//
// Parameters:
//   - ctx: Context for the API call
//   - call: Version-specific functions to execute
//   - capabilityCheck: Optional function to verify feature availability. If nil, no check is performed.
//
// Returns error if:
//   - Capability check fails
//   - The client type is unexpected
//   - The version-specific function is nil
//   - The versioned function itself returns an error
//
// Example (CRT-list only in v3.2+):
//
//	resp, err := c.DispatchWithCapability(ctx, CallFunc[*http.Response]{
//	    V32: func(c *v32.Client) (*http.Response, error) {
//	        return c.GetAllStorageSSLCrtListFiles(ctx)
//	    },
//	    // V31 and V30 omitted - not supported
//	}, func(caps Capabilities) error {
//	    if !caps.SupportsCrtList {
//	        return fmt.Errorf("crt-list storage requires DataPlane API v3.2+")
//	    }
//	    return nil
//	})
func (c *DataplaneClient) DispatchWithCapability(
	ctx context.Context,
	call CallFunc[*http.Response],
	capabilityCheck func(Capabilities) error,
) (*http.Response, error) {
	// Check capabilities first (for version-specific features)
	if capabilityCheck != nil {
		if err := capabilityCheck(c.clientset.Capabilities()); err != nil {
			return nil, err
		}
	}

	// Dispatch to appropriate version (community or enterprise)
	switch client := c.clientset.PreferredClient().(type) {
	// Community edition clients
	case *v32.Client:
		if call.V32 == nil {
			return nil, fmt.Errorf("operation not supported by DataPlane API v3.2 (v3.2 function is nil)")
		}
		return call.V32(client)

	case *v31.Client:
		if call.V31 == nil {
			return nil, fmt.Errorf("operation not supported by DataPlane API v3.1 (v3.1 function is nil)")
		}
		return call.V31(client)

	case *v30.Client:
		if call.V30 == nil {
			return nil, fmt.Errorf("operation not supported by DataPlane API v3.0 (v3.0 function is nil)")
		}
		return call.V30(client)

	// Enterprise edition clients
	case *v32ee.Client:
		if call.V32EE == nil {
			return nil, fmt.Errorf("operation not supported by HAProxy Enterprise DataPlane API v3.2 (v3.2ee function is nil)")
		}
		return call.V32EE(client)

	case *v31ee.Client:
		if call.V31EE == nil {
			return nil, fmt.Errorf("operation not supported by HAProxy Enterprise DataPlane API v3.1 (v3.1ee function is nil)")
		}
		return call.V31EE(client)

	case *v30ee.Client:
		if call.V30EE == nil {
			return nil, fmt.Errorf("operation not supported by HAProxy Enterprise DataPlane API v3.0 (v3.0ee function is nil)")
		}
		return call.V30EE(client)

	default:
		return nil, fmt.Errorf("unexpected client type: %T", client)
	}
}

// DispatchGeneric is a generic version of Dispatch for non-HTTP response types.
// Use this when the return type is not *http.Response (e.g., for string, int64, etc.).
//
// This is a package-level function because it needs to work with any clientset,
// not just DataplaneClient instances.
//
// Example (returning parsed data instead of raw response):
//
//	version, err := DispatchGeneric[int64](ctx, c.clientset, CallFunc[int64]{
//	    V32: func(c *v32.Client) (int64, error) {
//	        resp, err := c.GetConfigurationVersion(ctx, &v32.GetConfigurationVersionParams{})
//	        if err != nil {
//	            return 0, err
//	        }
//	        defer resp.Body.Close()
//	        // ... parse version from response ...
//	        return parsedVersion, nil
//	    },
//	    // ... similar for V31 and V30 ...
//	})
func DispatchGeneric[T any](
	ctx context.Context,
	clientset *Clientset,
	call CallFunc[T],
) (T, error) {
	switch client := clientset.PreferredClient().(type) {
	// Community edition clients
	case *v32.Client:
		if call.V32 == nil {
			var zero T
			return zero, fmt.Errorf("operation not supported by DataPlane API v3.2 (v3.2 function is nil)")
		}
		return call.V32(client)

	case *v31.Client:
		if call.V31 == nil {
			var zero T
			return zero, fmt.Errorf("operation not supported by DataPlane API v3.1 (v3.1 function is nil)")
		}
		return call.V31(client)

	case *v30.Client:
		if call.V30 == nil {
			var zero T
			return zero, fmt.Errorf("operation not supported by DataPlane API v3.0 (v3.0 function is nil)")
		}
		return call.V30(client)

	// Enterprise edition clients
	case *v32ee.Client:
		if call.V32EE == nil {
			var zero T
			return zero, fmt.Errorf("operation not supported by HAProxy Enterprise DataPlane API v3.2 (v3.2ee function is nil)")
		}
		return call.V32EE(client)

	case *v31ee.Client:
		if call.V31EE == nil {
			var zero T
			return zero, fmt.Errorf("operation not supported by HAProxy Enterprise DataPlane API v3.1 (v3.1ee function is nil)")
		}
		return call.V31EE(client)

	case *v30ee.Client:
		if call.V30EE == nil {
			var zero T
			return zero, fmt.Errorf("operation not supported by HAProxy Enterprise DataPlane API v3.0 (v3.0ee function is nil)")
		}
		return call.V30EE(client)

	default:
		var zero T
		return zero, fmt.Errorf("unexpected client type: %T", client)
	}
}

// EnterpriseCallFunc represents versioned API call functions for enterprise-only endpoints.
// Unlike CallFunc, this only includes enterprise edition clients since these endpoints
// are not available in HAProxy Community edition.
//
// Example usage for WAF profile management:
//
//	resp, err := c.DispatchEnterpriseOnly(ctx, EnterpriseCallFunc[*http.Response]{
//	    V32EE: func(c *v32ee.Client) (*http.Response, error) {
//	        return c.GetWafProfiles(ctx, &v32ee.GetWafProfilesParams{})
//	    },
//	    V31EE: func(c *v31ee.Client) (*http.Response, error) {
//	        return c.GetWafProfiles(ctx, &v31ee.GetWafProfilesParams{})
//	    },
//	    V30EE: func(c *v30ee.Client) (*http.Response, error) {
//	        return c.GetWafProfiles(ctx, &v30ee.GetWafProfilesParams{})
//	    },
//	})
type EnterpriseCallFunc[T any] struct {
	// V32EE is the function to call for HAProxy Enterprise DataPlane API v3.2+
	V32EE func(*v32ee.Client) (T, error)

	// V31EE is the function to call for HAProxy Enterprise DataPlane API v3.1
	V31EE func(*v31ee.Client) (T, error)

	// V30EE is the function to call for HAProxy Enterprise DataPlane API v3.0
	V30EE func(*v30ee.Client) (T, error)
}

// DispatchEnterpriseOnly executes the appropriate versioned function for enterprise-only endpoints.
// Returns ErrEnterpriseRequired if connected to HAProxy Community edition.
//
// Use this for enterprise-exclusive features like:
//   - WAF management (waf_profiles, waf_body_rules, waf/rulesets)
//   - Bot management (botmgmt_profiles, captchas)
//   - UDP load balancing (udp_lbs)
//   - Keepalived/VRRP (vrrp_instances, vrrp_sync_groups)
//   - Advanced logging (logs/config, logs/inputs, logs/outputs)
//   - Git integration (git/settings, git/actions)
//   - Dynamic updates (dynamic_update_rules)
//   - ALOHA features (aloha/actions)
//
// Example:
//
//	resp, err := c.DispatchEnterpriseOnly(ctx, EnterpriseCallFunc[*http.Response]{
//	    V32EE: func(c *v32ee.Client) (*http.Response, error) {
//	        return c.GetWafProfiles(ctx, &v32ee.GetWafProfilesParams{TransactionId: &txID})
//	    },
//	    V31EE: func(c *v31ee.Client) (*http.Response, error) {
//	        return c.GetWafProfiles(ctx, &v31ee.GetWafProfilesParams{TransactionId: &txID})
//	    },
//	    V30EE: func(c *v30ee.Client) (*http.Response, error) {
//	        return c.GetWafProfiles(ctx, &v30ee.GetWafProfilesParams{TransactionId: &txID})
//	    },
//	})
func (c *DataplaneClient) DispatchEnterpriseOnly(
	ctx context.Context,
	call EnterpriseCallFunc[*http.Response],
) (*http.Response, error) {
	// Check if connected to enterprise edition
	if !c.clientset.IsEnterprise() {
		return nil, ErrEnterpriseRequired
	}

	// Route to appropriate enterprise version
	switch c.clientset.MinorVersion() {
	case 2:
		if call.V32EE == nil {
			return nil, fmt.Errorf("operation not supported by HAProxy Enterprise DataPlane API v3.2 (v3.2ee function is nil)")
		}
		return call.V32EE(c.clientset.V32EE())
	case 1:
		if call.V31EE == nil {
			return nil, fmt.Errorf("operation not supported by HAProxy Enterprise DataPlane API v3.1 (v3.1ee function is nil)")
		}
		return call.V31EE(c.clientset.V31EE())
	default:
		if call.V30EE == nil {
			return nil, fmt.Errorf("operation not supported by HAProxy Enterprise DataPlane API v3.0 (v3.0ee function is nil)")
		}
		return call.V30EE(c.clientset.V30EE())
	}
}

// DispatchEnterpriseOnlyGeneric is a generic version of DispatchEnterpriseOnly for non-HTTP response types.
// Use this when the return type is not *http.Response (e.g., for parsed data, structs, etc.).
//
// Example (returning parsed WAF profile list):
//
//	profiles, err := DispatchEnterpriseOnlyGeneric[[]WafProfile](ctx, c.clientset, EnterpriseCallFunc[[]WafProfile]{
//	    V32EE: func(c *v32ee.Client) ([]WafProfile, error) {
//	        resp, err := c.GetWafProfiles(ctx, &v32ee.GetWafProfilesParams{})
//	        if err != nil {
//	            return nil, err
//	        }
//	        defer resp.Body.Close()
//	        // ... parse profiles from response ...
//	        return profiles, nil
//	    },
//	    // ... similar for V31EE and V30EE ...
//	})
func DispatchEnterpriseOnlyGeneric[T any](
	ctx context.Context,
	clientset *Clientset,
	call EnterpriseCallFunc[T],
) (T, error) {
	var zero T

	// Check if connected to enterprise edition
	if !clientset.IsEnterprise() {
		return zero, ErrEnterpriseRequired
	}

	// Route to appropriate enterprise version
	switch clientset.MinorVersion() {
	case 2:
		if call.V32EE == nil {
			return zero, fmt.Errorf("operation not supported by HAProxy Enterprise DataPlane API v3.2 (v3.2ee function is nil)")
		}
		return call.V32EE(clientset.V32EE())
	case 1:
		if call.V31EE == nil {
			return zero, fmt.Errorf("operation not supported by HAProxy Enterprise DataPlane API v3.1 (v3.1ee function is nil)")
		}
		return call.V31EE(clientset.V31EE())
	default:
		if call.V30EE == nil {
			return zero, fmt.Errorf("operation not supported by HAProxy Enterprise DataPlane API v3.0 (v3.0ee function is nil)")
		}
		return call.V30EE(clientset.V30EE())
	}
}
