# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Default SSL certificate support**: Quick-start TLS configuration for development and testing
  - Optional reference to existing Kubernetes TLS Secret via `controller.defaultSSLCertificate.secretName`
  - Optional inline certificate creation via `controller.defaultSSLCertificate.create` with cert/key in values
  - Development helper script `scripts/generate-dev-ssl-cert.sh` for self-signed certificate generation
  - Certificate name/namespace passed to templates via `extraContext` (variables: `default_ssl_cert_name`, `default_ssl_cert_namespace`)
  - Comprehensive SSL configuration documentation in Helm chart README
  - Removed static certificate from Git repository for improved security

- **Leader election for high availability**: Multiple controller replicas now supported with automatic leader election
  - Only the leader replica deploys configurations to HAProxy instances
  - All replicas continue watching resources, rendering templates, and validating configs (hot standby)
  - Automatic failover when leader fails (~15-20 seconds downtime)
  - Configurable timing parameters for failover speed and clock skew tolerance
  - Default deployment now uses 2 replicas for HA

- **Leader election metrics**: Three new Prometheus metrics for monitoring leadership
  - `haproxy_ic_leader_election_is_leader`: Current leadership status (gauge)
  - `haproxy_ic_leader_election_transitions_total`: Total leadership transitions (counter)
  - `haproxy_ic_leader_election_time_as_leader_seconds_total`: Cumulative time as leader (counter)

- **Leader election events**: Four new event types for observability
  - `LeaderElectionStartedEvent`: Published when leader election starts
  - `BecameLeaderEvent`: Published when replica becomes leader
  - `LostLeadershipEvent`: Published when replica loses leadership
  - `NewLeaderObservedEvent`: Published when new leader is observed

- **High availability operations guide**: Comprehensive documentation in `docs/operations/high-availability.md`
  - Configuration and deployment instructions
  - Leadership monitoring and troubleshooting
  - Best practices for production deployments
  - Migration guide from single-replica

### Changed

- **Service architecture refactored**: Controller and HAProxy now have separate Services
  - **Controller Service** (`haproxy-template-ic`): ClusterIP service for operational endpoints (healthz:8080, metrics:9090)
  - **HAProxy Service** (`haproxy-template-ic-haproxy`): Configurable service for ingress traffic (http:80, https:443)
  - Removed `service.httpPort` and `service.httpsPort` from Helm values
  - Added `haproxyService` section with enabled, type, ports, annotations, and labels configuration
  - Default HAProxy Service type is ClusterIP (override to LoadBalancer in production)
  - Development values (`values-dev.yaml`) use LoadBalancer for kind clusters
- **Default replica count changed from 1 to 2**: Helm chart now deploys 2 replicas by default for HA
- **Updated Helm chart**: Added POD_NAME and POD_NAMESPACE environment variables via downward API
- **Updated RBAC**: Added permissions for coordination.k8s.io/v1 Lease resources

### Fixed

- Fixed disabled mode in leader election to properly track state and publish events
