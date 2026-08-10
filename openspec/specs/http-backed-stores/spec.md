# http-backed-stores Specification

## Purpose

Defines how templates consume external HTTP content safely: the http.Fetch template surface, the two-version (pending/accepted) cache that keeps unvalidated content out of production renders, the refresh-validate-promote lifecycle driven by the all-replica HTTPStore adapter, and cache eviction. Invalid remote content (for example a malformed blocklist) can therefore never break a live HAProxy configuration.

## Requirements

### Requirement: Template Fetch Surface

Templates SHALL fetch HTTP content via http.Fetch(url, options, auth). The options map SHALL support: delay (refresh interval as a duration string; 0 or absent disables refresh), timeout (per-request), retries (attempt count), and critical (boolean). The auth map SHALL support type "basic" (username/password), "bearer" (token), and "header" (custom headers map). A failed non-critical fetch SHALL return an empty string without error; a failed critical fetch SHALL fail the render with an error. A fetch whose URL has accepted cached content SHALL return it without a network request. Every fetch with delay greater than zero SHALL register the URL for periodic refresh — on both production and validation render paths, so no extra wiring is needed to start the timer.

#### Scenario: Non-critical failure degrades to empty content

- **WHEN** http.Fetch fails for a URL with critical unset
- **THEN** the render SHALL receive an empty string and continue

#### Scenario: Critical failure fails the render

- **WHEN** http.Fetch fails for a URL with critical=true
- **THEN** the fetch SHALL return an error and the render fails

#### Scenario: Delay registers a refresher

- **WHEN** a template fetches a URL with delay "5m"
- **THEN** the URL SHALL be registered for periodic refresh at that interval

### Requirement: Two-Version Cache with Render Isolation

The store SHALL keep two content versions per URL: accepted (validated, production-safe) and pending (freshly refreshed, awaiting validation). Production renders SHALL read ONLY accepted content. Validation renders SHALL read through an HTTP content overlay that resolves to pending content when present, falling back to accepted — so the proposal pipeline judges the new content before it can reach a live config. A refresh SHALL store changed content as pending, never replacing accepted directly. Refreshes SHALL use conditional requests (ETag / If-Modified-Since); a 304 or an unchanged checksum SHALL produce no pending entry. A rejected pending SHALL be discarded with accepted preserved.

#### Scenario: Pending content invisible to production

- **WHEN** a refresh has stored new pending content for a URL and a production render fetches it
- **THEN** the render SHALL receive the accepted content, not the pending content

#### Scenario: Validation render sees pending

- **WHEN** a validation render (overlay present) fetches a URL with pending content
- **THEN** it SHALL receive the pending content

#### Scenario: Unchanged refresh is a no-op

- **WHEN** a refresh returns content whose checksum equals the accepted checksum (or a 304)
- **THEN** no pending entry SHALL be created and no validation SHALL be triggered

### Requirement: Refresh Timers

The HTTPStore adapter SHALL run a refresh timer per registered URL, firing at the URL's configured delay and re-arming after each refresh. The adapter is an all-replica component (every replica keeps its cache warm so a leadership transition does not start cold). Timers SHALL be stopped on shutdown and for evicted URLs; a refresh firing for an already-evicted URL SHALL be skipped. Event handling SHALL recover per event from panics so a single bad validation event cannot kill the adapter loop.

#### Scenario: Timer re-arms after refresh

- **WHEN** a URL's refresh completes (changed or not)
- **THEN** the timer SHALL be reset to the URL's delay

#### Scenario: All replicas refresh

- **WHEN** the controller runs multiple replicas
- **THEN** each replica SHALL run its own refresh timers and maintain its own cache

### Requirement: Promote-or-Reject Validation Flow

When a refresh produces changed content, the adapter SHALL publish a ProposalValidationRequestedEvent carrying an HTTP overlay of the pending state (recording the request ID as the pending validation), plus an HTTPResourceUpdatedEvent for observability. On the ProposalValidationCompletedEvent whose request ID matches the pending one — non-matching IDs SHALL be ignored — the adapter SHALL either promote or reject every URL with pending content: on Valid=true, promote pending to accepted, publish an HTTPResourceAcceptedEvent per URL, and publish a coalescible ReconciliationTriggeredEvent with reason "http_content_validated" so the accepted content reaches HAProxy; on Valid=false, discard every pending version while preserving accepted versions, log the validation phase and error, and identify each rejected URL with the rejected and retained checksums. The pending-validation ID SHALL be checked and cleared atomically so a duplicate completion cannot double-process.

#### Scenario: Valid content is promoted and reconciled

- **WHEN** the matching ProposalValidationCompletedEvent arrives with Valid=true
- **THEN** each pending URL SHALL be promoted to accepted, an HTTPResourceAcceptedEvent published per URL, and one coalescible reconciliation triggered

#### Scenario: Invalid content is rejected without touching accepted

- **WHEN** the matching completion arrives with Valid=false
- **THEN** each pending URL's content SHALL be discarded, the accepted content SHALL remain in use, and rejection diagnostics SHALL identify the URL and the rejected and retained checksums

#### Scenario: Foreign validation results ignored

- **WHEN** a ProposalValidationCompletedEvent arrives whose request ID does not match the adapter's pending validation
- **THEN** the adapter SHALL ignore it

### Requirement: Cache Eviction

The adapter SHALL evict cache entries not accessed within the eviction max age, running eviction at that same cadence. The controller wires the max age to twice the drift-prevention interval (120 seconds at the default 60-second interval), so an entry survives at least one full drift-driven render even if a render fails. Entries with pending validation content SHALL never be evicted. Evicting a URL SHALL also stop its refresh timer. A max age of zero disables eviction.

#### Scenario: Stale URL evicted and timer stopped

- **WHEN** a URL's templates stopped fetching it and its last access is older than the max age
- **THEN** the entry SHALL be evicted and its refresh timer stopped

#### Scenario: Pending content protects an entry

- **WHEN** an entry has pending validation content, however old its last access
- **THEN** it SHALL NOT be evicted

### Requirement: Validation-Test HTTP Fixtures

Validation tests SHALL mock HTTP content via the httpResources list on each test, where each fixture carries a url and a content string. During test execution, http.Fetch for a fixture's URL SHALL return the fixture content without any network request; fixture content is loaded directly as accepted.

#### Scenario: Fixture satisfies a template fetch

- **WHEN** a validation test declares a fixture for a URL and its template calls http.Fetch on that URL
- **THEN** the fetch SHALL return the fixture content with no HTTP request performed
