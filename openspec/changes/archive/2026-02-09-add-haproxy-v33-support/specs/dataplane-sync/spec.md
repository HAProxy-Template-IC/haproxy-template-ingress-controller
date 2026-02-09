## MODIFIED Requirements

### Requirement: Dataplane API Multi-Version Support

The client SHALL support Dataplane API versions v3.0, v3.1, v3.2, and v3.3 simultaneously via runtime version detection. The Dispatch pattern SHALL route API calls to the appropriate version-specific client. DispatchWithCapability SHALL check a capability predicate before dispatching, returning an error if the capability is not available. The Capabilities struct SHALL expose boolean flags for feature availability based on the detected version.

#### Scenario: Version auto-detection

WHEN the client connects to a Dataplane API endpoint
THEN it SHALL detect the API version and configure the appropriate version-specific client.

#### Scenario: v3.3 client selected for HAProxy 3.3

WHEN the client connects to a Dataplane API reporting version v3.3.x
THEN the dispatcher SHALL route calls to the v33 version-specific client.

#### Scenario: Capability-gated dispatch rejects unsupported feature

WHEN a CRT-list operation is attempted against a v3.0 endpoint
THEN DispatchWithCapability SHALL return an error indicating the feature requires v3.2+.

#### Scenario: v3.3 capabilities match v3.2

WHEN the client connects to a v3.3 endpoint
THEN all capability flags SHALL match those of v3.2 (no new endpoint-based capabilities in v3.3).
