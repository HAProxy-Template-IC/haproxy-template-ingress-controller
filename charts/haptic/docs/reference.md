# Configuration Reference

## Overview

Complete reference of all Helm values with types, defaults, and descriptions.

## Deployment & Image

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `replicaCount` | int | `2` | Number of controller replicas (2+ recommended for HA with leader election) |
| `image.repository` | string | `registry.gitlab.com/haproxy-haptic/haptic` | Controller image repository |
| `image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `image.tag` | string | Chart appVersion | Controller image tag |
| `imagePullSecrets` | list | `[]` | Image pull secrets for private registries |
| `nameOverride` | string | `""` | Override chart name |
| `fullnameOverride` | string | `""` | Override full release name |

## Controller Core

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.crdName` | string | `haptic-config` | Name of HAProxyTemplateConfig CRD resource |
| `controller.debugPort` | int | `8080` | Introspection HTTP server port (/healthz, /debug/*) |
| `controller.ports.healthz` | int | `8080` | Health check endpoint port |
| `controller.ports.metrics` | int | `9090` | Prometheus metrics endpoint port |
| `controller.ports.webhook` | int | `9443` | Admission webhook HTTPS port |

## Template Libraries

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.templateLibraries.base.enabled` | bool | `true` | Core HAProxy configuration (always enabled) |
| `controller.templateLibraries.ssl.enabled` | bool | `true` | SSL/TLS and HTTPS frontend support |
| `controller.templateLibraries.ingress.enabled` | bool | `true` | Kubernetes Ingress resource support |
| `controller.templateLibraries.gateway.enabled` | bool | `true` | Gateway API support (HTTPRoute, GRPCRoute) |
| `controller.templateLibraries.haproxytech.enabled` | bool | `true` | haproxy.org/* annotation support |
| `controller.templateLibraries.haproxyIngress.enabled` | bool | `true` | HAProxy Ingress Controller compatibility |
| `controller.templateLibraries.pathRegexLast.enabled` | bool | `false` | Performance-first path matching (regex last) |

## Default SSL Certificate

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.defaultSSLCertificate.enabled` | bool | `true` | Enable default SSL certificate requirement |
| `controller.defaultSSLCertificate.secretName` | string | `default-ssl-cert` | TLS Secret name containing certificate |
| `controller.defaultSSLCertificate.namespace` | string | `""` | Secret namespace (defaults to Release.Namespace) |
| `controller.defaultSSLCertificate.certManager.enabled` | bool | `true` | Use cert-manager for certificate provisioning |
| `controller.defaultSSLCertificate.certManager.createIssuer` | bool | `true` | Create self-signed Issuer (dev/test only) |
| `controller.defaultSSLCertificate.certManager.dnsNames` | list | `["localdev.me", "*.localdev.me"]` | DNS names for the certificate |
| `controller.defaultSSLCertificate.certManager.issuerRef.name` | string | `""` | Issuer name (auto-set when createIssuer=true) |
| `controller.defaultSSLCertificate.certManager.issuerRef.kind` | string | `Issuer` | Issuer kind |
| `controller.defaultSSLCertificate.certManager.duration` | duration | `8760h` | Certificate validity (1 year) |
| `controller.defaultSSLCertificate.certManager.renewBefore` | duration | `720h` | Renew before expiry (30 days) |
| `controller.defaultSSLCertificate.create` | bool | `false` | Create Secret from inline cert/key (testing only) |
| `controller.defaultSSLCertificate.cert` | string | `""` | PEM certificate (when create=true) |
| `controller.defaultSSLCertificate.key` | string | `""` | PEM private key (when create=true) |

## Controller Config

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.config.credentialsSecretRef.name` | string | Auto-generated | Secret containing Dataplane API credentials |
| `controller.config.credentialsSecretRef.namespace` | string | `""` | Credentials secret namespace |
| `controller.config.podSelector.matchLabels` | map | `{app.kubernetes.io/component: loadbalancer}` | Labels to match HAProxy pods |
| `controller.config.controller.healthzPort` | int | `8080` | Health check port |
| `controller.config.controller.metricsPort` | int | `9090` | Metrics port |

## Leader Election

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.config.controller.leaderElection.enabled` | bool | `true` | Enable leader election (recommended for HA) |
| `controller.config.controller.leaderElection.leaseName` | string | `""` | Lease resource name (defaults to release fullname) |
| `controller.config.controller.leaderElection.leaseDuration` | duration | `15s` | Failover timeout duration |
| `controller.config.controller.leaderElection.renewDeadline` | duration | `10s` | Leader renewal timeout |
| `controller.config.controller.leaderElection.retryPeriod` | duration | `2s` | Retry interval between attempts |

## Dataplane Configuration

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.config.dataplane.port` | int | `5555` | Dataplane API port |
| `controller.config.dataplane.minDeploymentInterval` | duration | `2s` | Minimum time between deployments |
| `controller.config.dataplane.driftPreventionInterval` | duration | `60s` | Periodic drift prevention interval |
| `controller.config.dataplane.mapsDir` | string | `/etc/haproxy/maps` | HAProxy maps directory |
| `controller.config.dataplane.sslCertsDir` | string | `/etc/haproxy/ssl` | SSL certificates directory |
| `controller.config.dataplane.generalStorageDir` | string | `/etc/haproxy/general` | General storage directory |
| `controller.config.dataplane.configFile` | string | `/etc/haproxy/haproxy.cfg` | HAProxy config file path |

## Logging & Templating

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `controller.logLevel` | string | `INFO` | Initial log level: TRACE, DEBUG, INFO, WARN, ERROR (case-insensitive) |
| `controller.config.logging.level` | string | `""` | Log level from ConfigMap. If set, overrides controller.logLevel at runtime |
| `controller.config.templatingSettings.extraContext.debug` | bool | `true` | Enable debug headers in HAProxy responses |
| `controller.config.watchedResourcesIgnoreFields` | list | `[metadata.managedFields]` | Fields to ignore in watched resources |

## Webhook Configuration

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `webhook.enabled` | bool | `true` | Enable admission webhook validation |
| `webhook.secretName` | string | Auto-generated | Webhook TLS certificate secret name |
| `webhook.service.port` | int | `443` | Webhook service port |
| `webhook.certManager.enabled` | bool | `false` | Use cert-manager for certificates |
| `webhook.certManager.createIssuer` | bool | `true` | Create a self-signed Issuer for webhook certs |
| `webhook.certManager.issuerRef.name` | string | `""` | Issuer name (auto-set when createIssuer=true) |
| `webhook.certManager.issuerRef.kind` | string | `Issuer` | Issuer kind |
| `webhook.certManager.duration` | duration | `8760h` | Certificate validity (1 year) |
| `webhook.certManager.renewBefore` | duration | `720h` | Renew before expiry (30 days) |
| `webhook.caBundle` | string | `""` | Base64-encoded CA bundle (manual certs) |

## IngressClass

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `ingressClass.enabled` | bool | `true` | Create IngressClass resource |
| `ingressClass.name` | string | `haproxy` | IngressClass name |
| `ingressClass.default` | bool | `false` | Mark as default IngressClass |
| `ingressClass.controllerName` | string | `haproxy-haptic.org/controller` | Controller identifier |

## GatewayClass

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `gatewayClass.enabled` | bool | `true` | Create GatewayClass resource |
| `gatewayClass.name` | string | `haproxy` | GatewayClass name |
| `gatewayClass.default` | bool | `false` | Mark as default GatewayClass |
| `gatewayClass.controllerName` | string | `haproxy-haptic.org/controller` | Controller identifier |
| `gatewayClass.parametersRef.group` | string | `haproxy-haptic.org` | HAProxyTemplateConfig API group |
| `gatewayClass.parametersRef.kind` | string | `HAProxyTemplateConfig` | HAProxyTemplateConfig kind |
| `gatewayClass.parametersRef.name` | string | `""` | Config name (defaults to controller.crdName) |
| `gatewayClass.parametersRef.namespace` | string | `""` | Config namespace (defaults to Release.Namespace) |

## Credentials

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `credentials.dataplane.username` | string | `admin` | Dataplane API username |
| `credentials.dataplane.password` | string | `adminpass` | Dataplane API password |

## ServiceAccount & RBAC

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `serviceAccount.create` | bool | `true` | Create ServiceAccount |
| `serviceAccount.automount` | bool | `true` | Automount API credentials |
| `serviceAccount.annotations` | map | `{}` | ServiceAccount annotations |
| `serviceAccount.name` | string | `""` | ServiceAccount name (auto-generated if empty) |
| `rbac.create` | bool | `true` | Create RBAC resources |

## Pod Configuration

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `podAnnotations` | map | `{}` | Pod annotations |
| `podLabels` | map | `{}` | Additional pod labels |
| `priorityClassName` | string | `""` | Pod priority class name |
| `topologySpreadConstraints` | list | `[]` | Pod topology spread constraints |
| `podSecurityContext.runAsNonRoot` | bool | `true` | Run as non-root user |
| `podSecurityContext.runAsUser` | int | `65532` | User ID |
| `podSecurityContext.runAsGroup` | int | `65532` | Group ID |
| `podSecurityContext.fsGroup` | int | `65532` | Filesystem group ID |
| `podSecurityContext.seccompProfile.type` | string | `RuntimeDefault` | Seccomp profile type |

## Container Security Context

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `securityContext.allowPrivilegeEscalation` | bool | `false` | Allow privilege escalation |
| `securityContext.capabilities.drop` | list | `[ALL]` | Dropped capabilities |
| `securityContext.readOnlyRootFilesystem` | bool | `true` | Read-only root filesystem |
| `securityContext.runAsNonRoot` | bool | `true` | Run as non-root |
| `securityContext.runAsUser` | int | `65532` | Container user ID |

## Service & Health Probes

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `service.type` | string | `ClusterIP` | Controller service type |
| `livenessProbe.httpGet.path` | string | `/healthz` | Liveness probe path |
| `livenessProbe.initialDelaySeconds` | int | `10` | Initial delay |
| `livenessProbe.periodSeconds` | int | `10` | Probe period |
| `livenessProbe.failureThreshold` | int | `3` | Failure threshold |
| `readinessProbe.httpGet.path` | string | `/healthz` | Readiness probe path |
| `readinessProbe.initialDelaySeconds` | int | `5` | Initial delay |
| `readinessProbe.periodSeconds` | int | `5` | Probe period |
| `readinessProbe.failureThreshold` | int | `3` | Failure threshold |

## Resources & Scheduling

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `resources.requests.cpu` | string | `100m` | CPU request |
| `resources.requests.memory` | string | `128Mi` | Memory request |
| `resources.limits.cpu` | string | `""` | CPU limit (optional) |
| `resources.limits.memory` | string | `""` | Memory limit (optional) |
| `nodeSelector` | map | `{}` | Node selector |
| `tolerations` | list | `[]` | Pod tolerations |
| `affinity` | map | `{}` | Pod affinity rules |

## Autoscaling & PDB

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `autoscaling.enabled` | bool | `false` | Enable HorizontalPodAutoscaler |
| `autoscaling.minReplicas` | int | `1` | Minimum replicas |
| `autoscaling.maxReplicas` | int | `10` | Maximum replicas |
| `autoscaling.targetCPUUtilizationPercentage` | int | `80` | Target CPU utilization |
| `podDisruptionBudget.enabled` | bool | `true` | Enable PodDisruptionBudget |
| `podDisruptionBudget.minAvailable` | int | `1` | Minimum available pods |

## Monitoring

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `monitoring.serviceMonitor.enabled` | bool | `false` | Create ServiceMonitor for Prometheus |
| `monitoring.serviceMonitor.interval` | duration | `30s` | Scrape interval |
| `monitoring.serviceMonitor.scrapeTimeout` | duration | `10s` | Scrape timeout |
| `monitoring.serviceMonitor.labels` | map | `{}` | ServiceMonitor labels |
| `monitoring.serviceMonitor.relabelings` | list | `[]` | Prometheus relabelings |
| `monitoring.serviceMonitor.metricRelabelings` | list | `[]` | Metric relabelings |

## HAProxy Deployment

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.enabled` | bool | `true` | Deploy HAProxy pods with this chart |
| `haproxy.replicaCount` | int | `2` | Number of HAProxy replicas |
| `haproxy.image.repository` | string | `haproxytech/haproxy-debian` | HAProxy image repository |
| `haproxy.image.pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `haproxy.image.tag` | string | `3.2` | HAProxy image tag |
| `haproxy.enterprise.enabled` | bool | `false` | Use HAProxy Enterprise |
| `haproxy.enterprise.version` | string | `3.2` | Enterprise version |
| `haproxy.haproxyBin` | string | Auto-detected | HAProxy binary path |
| `haproxy.dataplaneBin` | string | Auto-detected | Dataplane API binary path |
| `haproxy.user` | string | Auto-detected | HAProxy user |

## HAProxy Ports

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.ports.http` | int | `8080` | HTTP frontend container port |
| `haproxy.ports.https` | int | `8443` | HTTPS frontend container port |
| `haproxy.ports.stats` | int | `8404` | Stats/health page port |
| `haproxy.ports.dataplane` | int | `5555` | Dataplane API port |

## HAProxy Service

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.service.type` | string | `NodePort` | HAProxy service type |
| `haproxy.service.annotations` | map | `{}` | Service annotations |
| `haproxy.service.http.port` | int | `80` | HTTP service port |
| `haproxy.service.http.nodePort` | int | `30080` | HTTP NodePort |
| `haproxy.service.https.port` | int | `443` | HTTPS service port |
| `haproxy.service.https.nodePort` | int | `30443` | HTTPS NodePort |
| `haproxy.service.stats.port` | int | `8404` | Stats service port |
| `haproxy.service.stats.nodePort` | int | `30404` | Stats NodePort |

## HAProxy Dataplane Sidecar

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.dataplane.service.type` | string | `ClusterIP` | Dataplane service type |
| `haproxy.dataplane.credentials.username` | string | `admin` | Dataplane API username |
| `haproxy.dataplane.credentials.password` | string | `adminpass` | Dataplane API password |

## HAProxy Resources & Scheduling

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.resources.requests.cpu` | string | `100m` | CPU request |
| `haproxy.resources.requests.memory` | string | `128Mi` | Memory request |
| `haproxy.resources.limits.cpu` | string | `500m` | CPU limit |
| `haproxy.resources.limits.memory` | string | `512Mi` | Memory limit |
| `haproxy.priorityClassName` | string | `""` | Pod priority class |
| `haproxy.topologySpreadConstraints` | list | `[]` | Topology spread constraints |

## HAProxy NetworkPolicy

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `haproxy.networkPolicy.enabled` | bool | `false` | Enable HAProxy NetworkPolicy |
| `haproxy.networkPolicy.allowExternal` | bool | `true` | Allow external traffic |
| `haproxy.networkPolicy.allowedSources` | list | `[]` | Allowed traffic sources (when allowExternal=false) |
| `haproxy.networkPolicy.extraIngress` | list | `[]` | Additional ingress rules |
| `haproxy.networkPolicy.extraEgress` | list | `[]` | Additional egress rules |

## Controller NetworkPolicy

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `networkPolicy.enabled` | bool | `true` | Enable controller NetworkPolicy |
| `networkPolicy.egress.allowDNS` | bool | `true` | Allow DNS resolution |
| `networkPolicy.egress.kubernetesApi` | list | See values.yaml | Kubernetes API access rules |
| `networkPolicy.egress.haproxyPods.enabled` | bool | `true` | Allow access to HAProxy pods |
| `networkPolicy.egress.haproxyPods.podSelector` | map | See values.yaml | HAProxy pod selector |
| `networkPolicy.egress.haproxyPods.namespaceSelector` | map | `{}` | Namespace selector |
| `networkPolicy.egress.additionalRules` | list | See values.yaml | Additional egress rules |
| `networkPolicy.ingress.monitoring.enabled` | bool | `false` | Allow Prometheus scraping |
| `networkPolicy.ingress.monitoring.podSelector` | map | `{}` | Prometheus pod selector |
| `networkPolicy.ingress.monitoring.namespaceSelector` | map | `{}` | Prometheus namespace selector |
| `networkPolicy.ingress.healthChecks.enabled` | bool | `true` | Allow health check access |
| `networkPolicy.ingress.dataplaneApi.enabled` | bool | `true` | Allow Dataplane API access |
| `networkPolicy.ingress.webhook.enabled` | bool | `true` | Allow webhook access |
| `networkPolicy.ingress.additionalRules` | list | `[]` | Additional ingress rules |
