package client

import (
	"context"
	"fmt"
	"net/http"

	v30 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30"
	v30ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30ee"
	v31 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31"
	v31ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31ee"
	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
	v33 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v33"
)

// errNotSupported returns an error indicating that the operation is not supported
// by the given DataPlane API version (the version-specific function was nil).
func errNotSupported(version string) error {
	return fmt.Errorf("operation not supported by DataPlane API %s (%s function is nil)", version, version)
}

// errEnterpriseNotSupported returns an error indicating that the operation is not supported
// by the given HAProxy Enterprise DataPlane API version.
func errEnterpriseNotSupported(version string) error {
	return fmt.Errorf("operation not supported by HAProxy Enterprise DataPlane API %s (%see function is nil)", version, version)
}

// CallFunc represents a versioned API call function.
// Each field is a function that takes a version-specific client and returns a result of type T.
// This allows type-safe dispatch to the appropriate client version based on runtime detection.
//
// For HAProxy Community editions, use V30, V31, V32, V33.
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
	// V33 is the function to call for DataPlane API v3.3+
	V33 func(*v33.Client) (T, error)

	// V32 is the function to call for DataPlane API v3.2
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
//	        return errors.New("crt-list storage requires DataPlane API v3.2+")
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
	case *v33.Client:
		if call.V33 == nil {
			return nil, errNotSupported("v3.3")
		}
		return call.V33(client)

	case *v32.Client:
		if call.V32 == nil {
			return nil, errNotSupported("v3.2")
		}
		return call.V32(client)

	case *v31.Client:
		if call.V31 == nil {
			return nil, errNotSupported("v3.1")
		}
		return call.V31(client)

	case *v30.Client:
		if call.V30 == nil {
			return nil, errNotSupported("v3.0")
		}
		return call.V30(client)

	// Enterprise edition clients
	case *v32ee.Client:
		if call.V32EE == nil {
			return nil, errEnterpriseNotSupported("v3.2")
		}
		return call.V32EE(client)

	case *v31ee.Client:
		if call.V31EE == nil {
			return nil, errEnterpriseNotSupported("v3.1")
		}
		return call.V31EE(client)

	case *v30ee.Client:
		if call.V30EE == nil {
			return nil, errEnterpriseNotSupported("v3.0")
		}
		return call.V30EE(client)

	default:
		return nil, fmt.Errorf("unexpected client type: %T", client)
	}
}
