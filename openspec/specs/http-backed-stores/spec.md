# http-backed-stores Specification

## Purpose

Defines how templates consume external HTTP content safely: the http.Fetch template surface, render-local admission of new sources, the two-version (pending/accepted) cache for periodic refreshes, the refresh-validate-promote lifecycle driven by the all-replica HTTPStore adapter, and cache eviction. Invalid remote content (for example a malformed blocklist) can therefore never break a live HAProxy configuration.

## Requirements

### Requirement: Template Fetch Surface

Templates SHALL fetch HTTP content via http.Fetch(url, options, auth). The options map SHALL support: interval (refresh interval as a duration string; 0 or absent disables refresh), its deprecated delay alias, timeout (per-request), retries (attempt count), and critical (boolean). The auth map SHALL support type "basic" (username/password), "bearer" (token), and "header" (custom headers map). A failed non-critical fetch SHALL return an empty string without error; a failed critical fetch SHALL fail the render with an error. A fetch whose URL has accepted cached content for the same effective options and authentication SHALL return it without a network request. One render SHALL reject different options or authentication declarations for the same URL. A live reconciliation render SHALL reconcile each source declaration before reading shared cached content. A later live reconciliation that changes options or authentication SHALL invalidate the old accepted body and fetch under the new declaration. A live fetch with interval greater than zero SHALL register the URL for periodic refresh only after the complete render passes validation and its exact response is accepted.

#### Scenario: Non-critical failure degrades to empty content

- **WHEN** http.Fetch fails for a URL with critical unset
- **THEN** the render SHALL receive an empty string and continue

#### Scenario: Critical failure fails the render

- **WHEN** http.Fetch fails for a URL with critical=true
- **THEN** the fetch SHALL return an error and the render fails

#### Scenario: Interval registers a refresher

- **WHEN** a live reconciliation fetches a URL with interval "5m" and the complete rendered output passes validation
- **THEN** the URL SHALL be registered for periodic refresh at that interval

#### Scenario: Credential rotation cannot reuse old content

- **WHEN** a later render changes the authentication for a cached URL
- **THEN** the fetch SHALL use the new authentication and SHALL NOT return the previously cached body

#### Scenario: Conflicting declarations fail closed

- **WHEN** one render calls http.Fetch for the same URL with different options or authentication
- **THEN** the render SHALL fail and SHALL NOT let one declaration consume the other's cached body

### Requirement: Two-Version Cache with Render Isolation

The store SHALL bind each URL's shared content versions to its current source authority: accepted (validated, production-safe) and pending (freshly refreshed, awaiting periodic-refresh validation). Shared-cache reads by production renders SHALL return ONLY accepted content with the caller's source identity. An authoritative cache miss or source replacement SHALL fetch into a transaction owned by that render. Its response, including an empty successful body, SHALL be available only to that render and SHALL remain absent from shared accepted and pending content until the exact complete output passes built-in and configured validation. The transaction SHALL commit every candidate atomically after a final context-authority check; a stale token SHALL reject the whole set without accepting any candidate. A render, validation, commit, or cancellation failure SHALL discard all candidates, and a later authoritative render SHALL refetch them. Only a successful commit SHALL arm refresh timers. Validation and source-map renders SHALL remain read-only with respect to the shared source, cache versions, and refresh timer. They SHALL read a matching pending overlay when present, then matching accepted content, and fetch a miss or different declaration into a store owned by that render. A periodic refresh SHALL store changed content as pending, never replacing accepted directly. Refreshes SHALL use conditional requests (ETag / If-Modified-Since); a 304 or an unchanged checksum SHALL produce no pending entry. A rejected pending SHALL be discarded with accepted preserved.

#### Scenario: Pending content invisible to production

- **WHEN** a refresh has stored new pending content for a URL and a production render fetches it
- **THEN** the render SHALL receive the accepted content, not the pending content

#### Scenario: Validation render sees pending

- **WHEN** a validation render (overlay present) fetches a URL with pending content
- **THEN** it SHALL receive the pending content

#### Scenario: Rejected source change leaves accepted authority active

- **WHEN** a validation render declares different options or authentication for an accepted URL
- **THEN** it SHALL fetch and render that candidate without replacing the accepted source, content, generation, or timer

#### Scenario: Cold validation fetch remains render-local

- **WHEN** a validation render fetches a URL that has no shared cache entry
- **THEN** it SHALL receive the response without creating a shared cache entry or refresh timer

#### Scenario: Cold authoritative content waits for the exact pipeline verdict

- **WHEN** a live reconciliation fetches an uncached URL
- **THEN** that render SHALL receive the response, but shared accepted content and its refresh timer SHALL remain absent until the exact complete output passes every validator

#### Scenario: Failed authoritative candidate is refetched

- **WHEN** rendering, validation, commit fencing, or context authority rejects a render-local HTTP candidate
- **THEN** no candidate SHALL become accepted and the next authoritative render SHALL issue a new fetch

#### Scenario: Candidate sets commit all or none

- **WHEN** one source token in a validated render containing multiple new HTTP candidates is stale at commit
- **THEN** none of those candidates SHALL become accepted

#### Scenario: Unchanged refresh is a no-op

- **WHEN** a refresh returns content whose checksum equals the accepted checksum (or a 304)
- **THEN** no pending entry SHALL be created and no validation SHALL be triggered

### Requirement: Refresh Timers

The HTTPStore adapter SHALL run a refresh timer per registered URL, firing at the URL's configured interval and re-arming after each refresh. An authoritative interval or source change SHALL retire the old timer immediately and arm the replacement only after its render-local candidate is accepted; changing the interval to zero SHALL leave it stopped. The adapter SHALL run on every replica, but admission traffic SHALL NOT establish source or timer authority. Timers SHALL be stopped on shutdown and for evicted URLs; a refresh firing for an already-evicted URL SHALL be skipped. A refresh callback SHALL commit only to the cache entry, source generation, and accepted-content revision it fetched. A callback retired by timer or source replacement SHALL discard only the exact pending revision it created and promptly wake an active replacement timer. Cache eviction and timer retirement SHALL be atomic with respect to registration of a refetched URL. Event handling SHALL recover per event from panics so a single bad validation event cannot kill the adapter loop.

#### Scenario: Timer re-arms after refresh

- **WHEN** a URL's refresh completes (changed or not)
- **THEN** the timer SHALL be reset to the URL's delay

#### Scenario: Admission does not create a timer

- **WHEN** a replica fetches a source only for admission validation
- **THEN** it SHALL NOT create a shared cache entry or refresh timer on that replica

#### Scenario: Retired refresh cannot overwrite replacement state

- **WHEN** a refresh completes after its timer or cache entry was replaced
- **THEN** it SHALL NOT commit over the replacement, and any exact pending revision it already created SHALL be discarded before the active timer is re-driven

#### Scenario: Interval update replaces timer policy

- **WHEN** a later render changes a URL's interval or removes it
- **THEN** the previous timer SHALL be retired, and successful validation SHALL arm the new interval or leave it stopped when the interval is zero

#### Scenario: Refetch during eviction retains a timer

- **WHEN** a URL is refetched while eviction retires its previous cache entry and timer
- **THEN** registration SHALL install a timer for the refetched entry after the old timer is retired

### Requirement: Promote-or-Reject Validation Flow

When a periodic refresh produces changed content, the adapter SHALL publish an HTTPResourceUpdatedEvent and validate one active immutable batch of pending URL content, checksums, and revision tokens through ProposalValidationRequestedEvent. A refresh completed while another batch is active SHALL remain pending for a later batch. On the matching ProposalValidationCompletedEvent — non-matching IDs SHALL be ignored — the adapter SHALL finalize only URL versions that belong to that batch: on Valid=true, promote them, publish an HTTPResourceAcceptedEvent per URL, reconcile their refresh timers, and publish one coalescible ReconciliationTriggeredEvent with reason "http_content_validated"; on Valid=false, discard them while preserving accepted versions and log the validation phase and error. After either verdict, any remaining pending content SHALL start the next validation batch. Replacing a source in the active batch SHALL atomically retire the complete old batch, remove that source's pending revision, and immediately start one replacement batch containing every surviving pending version. A late verdict for the retired request SHALL change nothing. The active request ID and batch SHALL be checked and cleared atomically so a duplicate or stale completion cannot finalize another batch. Render-local initial candidates SHALL NOT enter this asynchronous proposal flow.

#### Scenario: Valid content is promoted and reconciled

- **WHEN** the matching ProposalValidationCompletedEvent arrives with Valid=true
- **THEN** each URL version in that validation batch SHALL be promoted to accepted, an HTTPResourceAcceptedEvent published per URL, and one coalescible reconciliation triggered

#### Scenario: Invalid content is rejected without touching accepted

- **WHEN** the matching completion arrives with Valid=false
- **THEN** each URL version in that validation batch SHALL be discarded, the accepted content SHALL remain in use, and rejection diagnostics SHALL identify the URL and the rejected and retained checksums

#### Scenario: Later refresh waits for its own verdict

- **WHEN** another URL becomes pending after the active validation batch was captured
- **THEN** the active batch's verdict SHALL NOT finalize that URL, and the adapter SHALL validate it in a later batch

#### Scenario: Lost batch is superseded

- **WHEN** a URL receives a new pending revision after its active validation verdict was lost
- **THEN** the adapter SHALL retire the old batch, validate the replacement revision, and ignore any late old verdict

#### Scenario: Source replacement preserves pending survivors

- **WHEN** one URL in an active multi-URL validation batch changes source authority
- **THEN** the adapter SHALL retire the old request and immediately validate every still-current pending URL in a new immutable batch

#### Scenario: Retired source verdict is inert

- **WHEN** a completion arrives for a validation request retired by source replacement
- **THEN** it SHALL NOT promote or reject any pending version or block the next validation batch

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
