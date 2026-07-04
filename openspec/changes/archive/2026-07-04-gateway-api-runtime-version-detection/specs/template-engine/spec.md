# Template Engine — Delta

## ADDED Requirements

### Requirement: Watched Resource Metadata in Render Context

The render context's per-resource surface (`resources.<name>`) SHALL expose the resolved API version of each watched resource via an `APIVersion()` accessor returning the group/version string the controller actually watches. The accessor SHALL be generic watch-set metadata, identical in shape for every watched resource, and SHALL reflect runtime resolution (not the configuration literal) when an ordered candidate list is in use.

#### Scenario: Templates read the resolved version

- **WHEN** a watched resource configured with `apiVersions: [example.io/v1, example.io/v1beta1]` resolves to `example.io/v1beta1` and a template evaluates `resources.<name>.APIVersion()`
- **THEN** the expression SHALL yield `example.io/v1beta1`.

#### Scenario: Status patches target a served version

- **WHEN** a status-patch macro passes `resources.<name>.APIVersion()` as the statusPatch apiVersion argument
- **THEN** the emitted patch SHALL target the version the cluster serves, and the status applier SHALL apply it without a version-mapping error.
