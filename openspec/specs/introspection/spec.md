# Introspection

Debug HTTP server for inspecting internal controller state via a variable registry, JSONPath queries, and Go profiling endpoints.

## ADDED Requirements

### Requirement: Instance-Based Registry

The introspection system SHALL use an instance-based Registry (not a global registry like `expvar`). Each Registry instance SHALL maintain its own set of registered variables, allowing independent lifecycle management.

#### Scenario: Two registries hold independent variables

WHEN two separate Registry instances are created and variables are registered on each
THEN each Registry SHALL only expose its own variables, not those from the other.

#### Scenario: Registry garbage collected on reinitialization

WHEN a Registry instance is discarded and a new one is created
THEN the old Registry and its registered variables SHALL become eligible for garbage collection with no stale references retained.

### Requirement: Var Interface and Built-In Types

Variables SHALL implement the Var interface with a `Get() (interface{}, error)` method. The system SHALL provide built-in types: IntVar (atomic int64), StringVar (mutex-protected string), FloatVar (atomic float64), MapVar (mutex-protected map), and Func (a function invoked on-demand to compute the value).

#### Scenario: IntVar returns current atomic value

WHEN an IntVar is set to 42 and then Get() is called
THEN Get() SHALL return 42 with no error.

#### Scenario: Func computes value on each Get

WHEN a Func variable is registered that returns the current timestamp
THEN each call to Get() SHALL invoke the function and return the current value.

#### Scenario: MapVar returns thread-safe snapshot

WHEN a MapVar is updated from multiple goroutines
THEN Get() SHALL return a consistent map without data races.

### Requirement: HTTP Endpoints

The introspection server SHALL expose: GET `/debug/vars` to list all registered variables and their values, and GET `/debug/vars/{path}` to retrieve a specific variable by its registration path. A `?field={.jsonpath}` query parameter SHALL be supported for extracting sub-fields from the returned value using JSONPath.

#### Scenario: List all variables

WHEN a GET request is made to `/debug/vars`
THEN the response SHALL contain a JSON object with all registered variable names and their current values.

#### Scenario: Get specific variable by path

WHEN a GET request is made to `/debug/vars/reconciler/lastRun`
THEN the response SHALL contain the current value of the variable registered at path `reconciler/lastRun`.

#### Scenario: JSONPath field selection

WHEN a GET request is made to `/debug/vars/store/resources?field={.metadata.name}`
THEN the response SHALL contain only the value extracted by the JSONPath expression from the variable's value.

#### Scenario: Non-existent variable returns 404

WHEN a GET request is made to `/debug/vars/nonexistent`
THEN the response status code SHALL be 404.

### Requirement: JSONPath via client-go

JSONPath evaluation SHALL use `k8s.io/client-go/util/jsonpath` for field selection queries. Invalid JSONPath expressions SHALL return an error response.

#### Scenario: Invalid JSONPath returns error

WHEN a GET request includes `?field={.invalid[}` with malformed JSONPath
THEN the response SHALL indicate an error with the JSONPath expression.

### Requirement: Automatic pprof Integration

The introspection server SHALL register Go pprof handlers at `/debug/pprof/*`, providing CPU profiling, heap profiling, goroutine dumps, and other standard pprof endpoints.

#### Scenario: pprof endpoints available

WHEN a GET request is made to `/debug/pprof/`
THEN the response SHALL contain the standard Go pprof index page with links to available profiles.

### Requirement: Thread-Safe Concurrent Access

Variable registration and queries SHALL be thread-safe. Multiple goroutines SHALL be able to register new variables and query existing ones concurrently without data races.

#### Scenario: Concurrent registration and query

WHEN multiple goroutines simultaneously register variables and query the registry
THEN all operations SHALL complete without data races or panics.

### Requirement: Graceful Shutdown

The introspection HTTP server SHALL support graceful shutdown with a 30-second timeout. On context cancellation, the server SHALL stop accepting new connections and wait up to 30 seconds for in-flight requests to complete before forcing closure.

#### Scenario: Server shuts down gracefully within timeout

WHEN the context is cancelled and no in-flight requests are pending
THEN the server SHALL shut down immediately without error.

#### Scenario: Server waits for in-flight requests

WHEN the context is cancelled while requests are in-flight
THEN the server SHALL wait up to 30 seconds for those requests to complete before forcing shutdown.

### Requirement: Network Binding

The introspection server SHALL bind to `0.0.0.0` to ensure compatibility with `kubectl port-forward`, which requires the server to listen on all interfaces.

#### Scenario: Server accessible via port-forward

WHEN the introspection server is running and `kubectl port-forward` is used
THEN the server SHALL be reachable through the forwarded port because it binds to all interfaces.
