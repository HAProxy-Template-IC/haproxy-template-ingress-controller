# Template Engine

## ADDED Requirements

### Requirement: StatusPatchCollector in Render Context

The render context SHALL include a `statusPatchCollector` key containing a StatusPatchCollector instance. The collector SHALL be created fresh for each render cycle (same lifecycle as FileRegistry). After rendering completes, the caller SHALL retrieve collected patches via `statusPatchCollector.Patches()`. The StatusPatchCollector SHALL be a new type in `pkg/templating` (or `pkg/controller/rendercontext`) implementing thread-safe collection with `sync.Mutex`.

#### Scenario: StatusPatchCollector available in templates

- **WHEN** a template accesses `statusPatchCollector` from the render context
- **THEN** it SHALL receive a non-nil StatusPatchCollector instance

#### Scenario: Fresh collector per render cycle

- **WHEN** two consecutive render cycles execute
- **THEN** each cycle SHALL have its own StatusPatchCollector instance with no patches carried over from the previous cycle

### Requirement: toJSON Filter Registration

The template engine SHALL register a `toJSON` filter function (also accessible as `to_json`) that serializes any Go value to a JSON string using `encoding/json.Marshal`. The filter SHALL be usable both as a piped filter (`value | toJSON`) and as a standalone function (`toJSON(value)`).

#### Scenario: toJSON registered as filter

- **WHEN** a template uses `{{ myMap | toJSON }}`
- **THEN** the engine SHALL produce the JSON-serialized representation of myMap
