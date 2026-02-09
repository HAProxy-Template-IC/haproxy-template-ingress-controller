## MODIFIED Requirements

### Requirement: Version Definition in versions.env

The file `versions.env` SHALL define `HAPROXY_VERSIONS` as a space-separated list of supported HAProxy major.minor versions, and `DEFAULT_HAPROXY` as the default version for local builds. These values SHALL be the single source of truth for supported versions. `DEFAULT_HAPROXY` SHALL be set to the latest LTS version.

#### Scenario: versions.env defines supported versions

WHEN versions.env is read
THEN HAPROXY_VERSIONS SHALL contain a space-separated list (currently "3.0 3.1 3.2 3.3") and DEFAULT_HAPROXY SHALL be set to "3.2" (the latest LTS version).

#### Scenario: Default version is the latest LTS

WHEN a user does not override the HAProxy version
THEN the system SHALL use the LTS version (3.2), not the latest short-term release (3.3).

### Requirement: CI Image Tagging

CI SHALL build controller images for each version listed in HAPROXY_VERSIONS. Each image SHALL be tagged with a version suffix in the format `haptic:X.Y.Z-haproxyN.M`, where X.Y.Z is the controller version and N.M is the HAProxy version.

#### Scenario: CI produces versioned images

WHEN CI builds the controller for versions "3.0 3.1 3.2 3.3" at controller version "0.1.0"
THEN images SHALL be produced with tags `haptic:0.1.0-haproxy3.0`, `haptic:0.1.0-haproxy3.1`, `haptic:0.1.0-haproxy3.2`, and `haptic:0.1.0-haproxy3.3`.

## ADDED Requirements

### Requirement: Enterprise Version Mapping

`versions.env` SHALL define enterprise version mappings for each supported community version that has a corresponding HAProxy Enterprise release. Enterprise mappings for versions without available Enterprise images SHALL be omitted until the Enterprise image is published.

#### Scenario: Enterprise mapping present for released versions

WHEN versions.env is read and HAProxy Enterprise 3.0, 3.1, 3.2 are released
THEN HAPROXY_ENTERPRISE_30, HAPROXY_ENTERPRISE_31, and HAPROXY_ENTERPRISE_32 SHALL be defined.

#### Scenario: Enterprise mapping absent for unreleased versions

WHEN HAProxy Enterprise 3.3 has not been released
THEN HAPROXY_ENTERPRISE_33 SHALL NOT be defined in versions.env.

### Requirement: OpenAPI Spec Extraction Script

The `extract-dataplane-spec.sh` script SHALL use the correct DataPlane API binary path for the target container image. For community images, the binary path SHALL be `/usr/local/bin/dataplaneapi`.

#### Scenario: Spec extraction succeeds for HAProxy 3.3

WHEN `extract-dataplane-spec.sh 3.3` is run
THEN the script SHALL successfully extract the OpenAPI spec from the `haproxytech/haproxy-alpine:3.3` container.

#### Scenario: Spec extraction succeeds for HAProxy 3.2

WHEN `extract-dataplane-spec.sh 3.2` is run
THEN the script SHALL successfully extract the OpenAPI spec from the `haproxytech/haproxy-alpine:3.2` container.
