## MODIFIED Requirements

### Requirement: Fine-Grained Configuration Comparison

The Comparator SHALL perform attribute-level comparison between two parsed StructuredConfig instances. It SHALL compare global, defaults, frontends, backends, servers, and 15+ additional section types (resolvers, mailers, peers, caches, rings, userlists, programs, log-forwards, log-profiles, traces, acme-providers, enterprise sections, fcgi-apps, crt-stores, http-errors). The comparison SHALL produce a ConfigDiff containing an ordered list of Operations and a DiffSummary. Both current and desired configurations MUST be non-nil; nil input SHALL return an error.

For indexed rule types (HTTP request rules, HTTP response rules, TCP request rules, TCP response rules, stick rules, HTTP after-response rules, backend switching rules, server switching rules), the Comparator SHALL use LCS-based content matching via the Myers diff algorithm instead of index-based positional comparison. Two rules SHALL be considered equal when their `Equal()` method returns true. The diff SHALL produce INSERT (CREATE at index) and DELETE operations for rule additions and removals, rather than cascading UPDATE operations caused by index shifts. Rules present in both current and desired configurations at different positions SHALL produce no operations. Rules at the same LCS position with different content SHALL produce UPDATE operations.

The LCS diff positions SHALL be translated to correct Dataplane API indexes using a running offset that accounts for cumulative shifts from prior operations within the same rule section. DELETE operations SHALL use the current-config index. INSERT operations SHALL use the desired-config index (the target position in the final configuration). The existing priority system (deletes highest-index-first, creates lowest-index-first) SHALL handle execution ordering.

The LCS-based comparison SHALL be implemented as a single generic function parameterized over rule type, accepting an equality function and producing abstract diff entries (keep/insert/delete). Each rule-type-specific comparison function SHALL wrap this generic function with its own operation factory calls.

#### Scenario: Single attribute change produces single update operation

WHEN a backend's balance algorithm changes from "roundrobin" to "leastconn" with no other changes
THEN the ConfigDiff SHALL contain exactly one Update operation for that backend.

#### Scenario: New frontend produces create operation

WHEN the desired configuration contains a frontend not present in the current configuration
THEN the ConfigDiff SHALL contain a Create operation for that frontend.

#### Scenario: Removed backend produces delete operation

WHEN the current configuration contains a backend not present in the desired configuration
THEN the ConfigDiff SHALL contain a Delete operation for that backend.

#### Scenario: Nil configuration rejected

WHEN either current or desired configuration is nil
THEN Compare SHALL return an error.

#### Scenario: Rule insertion produces only insert operations

WHEN one HTTP request rule is inserted at position 5 in a frontend with 100 existing rules
THEN the ConfigDiff SHALL contain exactly one CREATE operation for that rule and zero UPDATE operations for the subsequent 95 rules.

#### Scenario: Rule deletion produces only delete operations

WHEN one HTTP request rule is deleted from position 5 in a frontend with 100 existing rules
THEN the ConfigDiff SHALL contain exactly one DELETE operation for that rule and zero UPDATE operations for the subsequent 94 rules.

#### Scenario: Rule content change produces update operation

WHEN an HTTP request rule at position 10 changes its action from "deny" to "allow" with all other rules unchanged
THEN the ConfigDiff SHALL contain exactly one UPDATE operation at index 10 and no INSERT or DELETE operations for that frontend's rules.

#### Scenario: Mixed insertions and deletions at different positions

WHEN one rule is deleted at position 3 and one different rule is inserted at position 7 in the same frontend
THEN the ConfigDiff SHALL contain exactly one DELETE and one CREATE operation, with no UPDATE operations for unchanged rules between or after the changed positions.

#### Scenario: LCS comparison applies to all eight indexed rule types

WHEN rules shift due to insertion in any of the eight indexed rule types (HTTP request, HTTP response, TCP request, TCP response, stick, HTTP after-response, backend switching, server switching)
THEN the Comparator SHALL use LCS-based content matching for that rule type, producing INSERT/DELETE operations instead of cascading UPDATEs.

#### Scenario: DELETE index uses current-config position

WHEN a rule at current-config index 5 is deleted
THEN the DELETE operation SHALL specify index 5.

#### Scenario: INSERT index uses desired-config position

WHEN a new rule should appear at desired-config index 8
THEN the CREATE operation SHALL specify index 8.
