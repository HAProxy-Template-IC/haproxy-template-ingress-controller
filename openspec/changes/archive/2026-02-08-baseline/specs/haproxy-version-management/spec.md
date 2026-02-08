# HAProxy Version Management

Multi-version HAProxy support with build-time version selection and guaranteed matching between controller and HAProxy image tags.

## ADDED Requirements

### Requirement: Version Definition in versions.env

The file `versions.env` SHALL define `HAPROXY_VERSIONS` as a space-separated list of supported HAProxy major.minor versions, and `DEFAULT_HAPROXY` as the default version for local builds. These values SHALL be the single source of truth for supported versions.

#### Scenario: versions.env defines supported versions

WHEN versions.env is read
THEN HAPROXY_VERSIONS SHALL contain a space-separated list (currently "3.0 3.1 3.2") and DEFAULT_HAPROXY SHALL be set to one of those versions.

### Requirement: CI Image Tagging

CI SHALL build controller images for each version listed in HAPROXY_VERSIONS. Each image SHALL be tagged with a version suffix in the format `haptic:X.Y.Z-haproxyN.M`, where X.Y.Z is the controller version and N.M is the HAProxy version.

#### Scenario: CI produces versioned images

WHEN CI builds the controller for versions "3.0 3.1 3.2" at controller version "0.1.0"
THEN images SHALL be produced with tags `haptic:0.1.0-haproxy3.0`, `haptic:0.1.0-haproxy3.1`, and `haptic:0.1.0-haproxy3.2`.

### Requirement: Helm haproxyVersion Drives Both Image Tags

The Helm chart `haproxyVersion` value SHALL drive both the controller image tag and the HAProxy sidecar image tag. This single value SHALL guarantee that the controller and HAProxy pod always use matching versions.

#### Scenario: haproxyVersion selects matching controller and HAProxy images

WHEN haproxyVersion is set to "3.2" and the controller version is "0.1.0"
THEN the controller image tag SHALL be `haptic:0.1.0-haproxy3.2` and the HAProxy image tag SHALL reference HAProxy version 3.2.

#### Scenario: Mismatched versions prevented by single value

WHEN haproxyVersion is the only version input to the Helm chart
THEN there SHALL be no way to deploy a controller built for one HAProxy version with a different HAProxy binary version.

### Requirement: Dockerfile Default Version

The Dockerfile SHALL use `DEFAULT_HAPROXY` from versions.env for local builds when no explicit version build argument is provided.

#### Scenario: Local build uses default version

WHEN `docker build` is run without specifying an HAProxy version argument
THEN the built image SHALL use the version specified by DEFAULT_HAPROXY.
