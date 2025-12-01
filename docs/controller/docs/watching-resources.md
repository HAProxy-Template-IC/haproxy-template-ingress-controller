# Watched Resources Configuration

## Overview

The `watched_resources` configuration determines which Kubernetes resources the controller monitors and how it stores them in memory. Each resource type you configure creates a watcher that tracks changes and maintains an indexed store for template access.

This configuration is critical for:
- **Performance**: Choosing the right storage strategy affects memory usage and template rendering speed
- **Functionality**: Resources must be watched to be accessible in templates
- **Scalability**: Proper store selection allows operation in memory-constrained environments

The controller supports two storage strategies: **memory store** (full in-memory storage) and **cached store** (on-demand API-backed storage). Understanding when to use each is essential for optimal controller performance.

For complete configuration syntax, see [Configuration Reference](./configuration.md#watched_resources).

## Understanding Store Types

The controller provides two store implementations with different trade-offs:

| Aspect | Memory Store (`store: full` or default) | Cached Store (`store: on-demand`) |
|--------|----------------------------------------|-----------------------------------|
| **Storage** | Complete resources in memory | Only references in memory, resources fetched from API |
| **Lookup Performance** | O(1), instant | O(1) with cache hit, API latency on cache miss |
| **Memory Usage** | ~1KB per resource | Minimal (only references + cache) |
| **API Load** | Initial list only | Initial list + fetch on cache miss |
| **Best For** | Iterating over all resources | Selective resource access |
| **Template Method** | `.List()` efficient | `.Fetch()` efficient |

**How they work:**

**Memory Store**: When a resource changes, the watcher stores the complete resource object in memory. Template rendering accesses this in-memory copy directly.

**Cached Store**: When a resource changes, the watcher stores only a reference (namespace + name + index keys). When a template accesses the resource via `.Fetch()`, the store checks its TTL cache. On cache miss, it fetches from the Kubernetes API and caches the result.

## Memory Store

The memory store is the default storage strategy. It keeps complete resource objects in memory for fast access.

### How It Works

When you configure a resource with memory store:

1. The watcher performs an initial list operation to load all existing resources
2. Each resource is stored completely in memory after field filtering
3. Resources are indexed using the `index_by` configuration for O(1) lookups
4. Real-time changes (add/update/delete) update the in-memory store
5. Template rendering accesses resources directly from memory

### When to Use Memory Store

Use memory store when you will:
- **Iterate over all resources** frequently (e.g., generating map files with all ingresses)
- **Access most resources** during each template render
- **Need fastest template rendering** (no API latency)
- Work with **small to medium resources** (Ingress, Service, EndpointSlice)

**Example use cases:**
- Ingress resources for generating HAProxy routing rules
- Service resources for backend configuration
- EndpointSlice resources for dynamic server lists
- ConfigMap resources for configuration snippets

### Memory Usage

Approximate memory usage per resource:
- **Ingress**: ~1-2 KB (depends on number of rules and paths)
- **Service**: ~1 KB (simple services) to ~5 KB (many ports/annotations)
- **EndpointSlice**: ~2-5 KB (depends on number of endpoints)
- **ConfigMap/Secret**: ~1 KB + data size

**Example**: 1000 Ingress resources ≈ 1-2 MB memory

### Configuration

Configure memory store explicitly or use the default:

```yaml
watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    index_by: ["metadata.namespace", "metadata.name"]
    # Memory store is the default
    store: full  # Optional - this is the default

  services:
    api_version: v1
    kind: Service
    index_by: ["metadata.namespace", "metadata.name"]
    # Omitting 'store' uses memory store by default
```

See [Configuration Reference](./configuration.md#watched_resources) for complete field descriptions.

## Cached Store

The cached store minimizes memory usage by storing only resource references and fetching complete resources on-demand from the Kubernetes API.

### How It Works

When you configure a resource with cached store:

1. The watcher performs an initial list operation to discover resources
2. Only references are stored (namespace, name, and index keys)
3. Resources are indexed using the `index_by` configuration
4. When a template calls `.Fetch()`, the store checks its TTL cache
5. On cache miss, the store fetches the resource from the Kubernetes API
6. Fetched resources are cached for the configured TTL duration
7. Real-time changes update the reference index but don't fetch the full resource

### When to Use Cached Store

Use cached store when you will:
- **Access resources selectively** (e.g., fetch specific secrets by name)
- Work with **large resources** (Secrets with certificate data, large ConfigMaps)
- Need to **minimize memory usage** (hundreds of secrets or large data)
- Can tolerate **API latency** on cache misses

**Example use cases:**
- Secrets containing TLS certificates (large PEM data)
- Secrets for authentication (accessed by annotation reference)
- ConfigMaps with large configuration data
- Resources accessed conditionally in templates

### Trade-offs

**Benefits:**
- Minimal memory footprint (only references + cache)
- Suitable for large resource types
- Cache reduces API calls for frequently accessed resources

**Costs:**
- API latency on cache misses (typically 10-50ms)
- Additional load on Kubernetes API server
- Slightly slower template rendering when cache misses occur

### Configuration

Configure cached store with the `store: on-demand` setting:

```yaml
watched_resources:
  secrets:
    api_version: v1
    kind: Secret
    store: on-demand  # Use cached store
    cache_ttl: 2m10s  # Optional: cache duration (default: 2m10s)
    index_by: ["metadata.namespace", "metadata.name"]
```

**Cache TTL considerations:**
- **Shorter TTL** (1-2 minutes): More API calls, fresher data
- **Longer TTL** (5-10 minutes): Fewer API calls, may show stale data between template renders
- **Default** (2m10s): Balanced approach for most use cases

See [Configuration Reference](./configuration.md#watched_resources) for complete field descriptions.

## Indexing with index_by

The `index_by` configuration determines how resources are indexed for lookups and what parameters the `.Fetch()` method expects in templates.

### Understanding Indexing

Resources are indexed using composite keys constructed from JSONPath expressions. This enables O(1) lookup performance for specific resources.

**Example**: Indexing by namespace and name:
```yaml
index_by: ["metadata.namespace", "metadata.name"]
```

Creates composite keys like:
- `default/my-ingress` → [Ingress resource]
- `production/api-gateway` → [Ingress resource]

**How lookups work:**
```jinja2
{# In templates, Fetch() parameters match index_by order #}
{% for ingress in resources.ingresses.Fetch("default", "my-ingress") %}
  {# Returns the specific ingress #}
{% endfor %}
```

### Common Indexing Patterns

#### Pattern 1: By Namespace and Name

**Most common pattern** for standard Kubernetes resources:

```yaml
watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    index_by: ["metadata.namespace", "metadata.name"]
```

**Use when:**
- Looking up specific resources by namespace and name
- Standard resource types (Ingress, Service, ConfigMap, Secret)

**Template usage:**
```jinja2
{# Fetch specific ingress #}
{% for ingress in resources.ingresses.Fetch("default", "my-app") %}
  {# Process specific ingress #}
{% endfor %}

{# Fetch all in namespace #}
{% for ingress in resources.ingresses.Fetch("default") %}
  {# Process all ingresses in 'default' namespace #}
{% endfor %}
```

See [Configuration Reference](./configuration.md#watched_resources) for `index_by` syntax.

#### Pattern 2: By Service Name (for EndpointSlices)

**Essential pattern** for mapping services to endpoints:

```yaml
watched_resources:
  endpoints:
    api_version: discovery.k8s.io/v1
    kind: EndpointSlice
    index_by: ["metadata.labels.kubernetes\\.io/service-name"]
```

**Why this works**: EndpointSlices are labeled with `kubernetes.io/service-name`, allowing O(1) lookup of all endpoint slices for a service.

**Template usage:**
```jinja2
{# In ingress loop, extract service name #}
{% set service_name = path.backend.service.name %}

{# Fetch all endpoint slices for this service #}
{% for endpoint_slice in resources.endpoints.Fetch(service_name) %}
  {# Generate server entries from endpoints #}
  {% for endpoint in (endpoint_slice.endpoints | default([])) %}
    {% for address in (endpoint.addresses | default([])) %}
      server {{ endpoint.targetRef.name }} {{ address }}:{{ port }} check
    {% endfor %}
  {% endfor %}
{% endfor %}
```

This is the **most important cross-resource lookup pattern** in HAProxy configurations.

See [Configuration Reference](./configuration.md#index_by) for label selector syntax.

#### Pattern 3: By Type (for Secrets)

**Useful pattern** for grouping secrets by type:

```yaml
watched_resources:
  secrets:
    api_version: v1
    kind: Secret
    store: on-demand
    index_by: ["metadata.namespace", "type"]
```

**Template usage:**
```jinja2
{# Fetch all TLS secrets in namespace #}
{% for secret in resources.secrets.Fetch("default", "kubernetes.io/tls") %}
  {# Generate SSL certificate files #}
{% endfor %}
```

#### Pattern 4: Single Key Index

**Simplified pattern** when resources are uniquely identified by one field:

```yaml
watched_resources:
  configmaps:
    api_version: v1
    kind: ConfigMap
    namespace: haproxy-system  # Watch single namespace
    index_by: ["metadata.name"]
```

**Template usage:**
```jinja2
{# Fetch by name only (namespace is implicit) #}
{% for cm in resources.configmaps.Fetch("error-pages") %}
  {# Use configmap data #}
{% endfor %}
```

### Choosing Index Strategy

Consider these questions when choosing `index_by`:

1. **How will you look up resources in templates?**
   - By namespace + name → Use both in `index_by`
   - By label value → Use label in `index_by`
   - By type → Include type in `index_by`

2. **What cross-resource relationships exist?**
   - Service → Endpoints: Index endpoints by service name label
   - Ingress → Secret: Index secrets by namespace + name

3. **Is the index unique or non-unique?**
   - Unique (namespace + name): Returns 0 or 1 resource
   - Non-unique (service name): Returns 0+ resources

**Example decision**: For EndpointSlices, you need to find all slices for a service name. Index by the service name label (non-unique) rather than namespace + name (unique).

See [Configuration Reference](./configuration.md#index_by) for additional indexing examples.

### Non-Unique Indexes

Some index configurations intentionally map multiple resources to the same key:

```yaml
# Multiple endpoint slices can have the same service-name label
index_by: ["metadata.labels.kubernetes\\.io/service-name"]
```

**Result**: `.Fetch("my-service")` returns all endpoint slices labeled with `my-service`.

This is the correct behavior for one-to-many relationships.

## Accessing Resources in Templates

Resources are accessed in templates through the `resources` variable, which contains stores for all configured resource types.

### The resources Variable

The `resources` variable structure matches your `watched_resources` configuration:

```yaml
# Configuration
watched_resources:
  ingresses: {...}
  services: {...}
  endpoints: {...}
  secrets: {...}
```

```jinja2
{# Template access #}
{{ resources.ingresses }}  {# Store for ingresses #}
{{ resources.services }}   {# Store for services #}
{{ resources.endpoints }}  {# Store for endpoints #}
{{ resources.secrets }}    {# Store for secrets #}
```

Each store provides two methods: `.List()` and `.Fetch()`.

### Using List() Method

The `.List()` method returns all resources in the store.

**Best for:**
- Iterating over all resources
- Generating configuration for every resource
- Counting total resources
- Memory store (efficient)

**Example - Generate routing map for all ingresses:**
```jinja2
{# maps/host.map template #}
{% for ingress in resources.ingresses.List() %}
  {% for rule in (ingress.spec.rules | default([])) %}
    {{ rule.host }} backend_{{ ingress.metadata.name }}
  {% endfor %}
{% endfor %}
```

**Example - Count resources:**
```jinja2
{# Total ingresses: {{ resources.ingresses.List() | length }} #}
```

**Performance note**: With memory store, `.List()` returns in-memory objects (fast). With cached store, `.List()` still returns references, but template rendering will trigger fetches for each resource (slower).

### Using Fetch() Method

The `.Fetch()` method returns resources matching index keys.

**Best for:**
- Looking up specific resources
- Cross-resource relationships (service → endpoints)
- Conditional resource access
- Cached store (efficient)

**Parameters**: Match the `index_by` configuration order.

**Example - Fetch specific ingress:**
```yaml
# Configuration
index_by: ["metadata.namespace", "metadata.name"]
```

```jinja2
{# Template #}
{% for ingress in resources.ingresses.Fetch("default", "my-app") %}
  {# Usually returns 0 or 1 items #}
{% endfor %}
```

**Example - Fetch all in namespace:**
```jinja2
{# Partial key match - returns all in namespace #}
{% for ingress in resources.ingresses.Fetch("default") %}
  {# All ingresses in 'default' namespace #}
{% endfor %}
```

**Example - Cross-resource lookup:**
```jinja2
{# Get endpoints for a service #}
{% set service_name = path.backend.service.name %}
{% for endpoint_slice in resources.endpoints.Fetch(service_name) %}
  {# All endpoint slices for this service #}
{% endfor %}
```

**Performance note**: With cached store, `.Fetch()` uses the cache effectively. Only fetches from API on cache miss.

### Method Comparison

| Method | Memory Store | Cached Store | Use Case |
|--------|-------------|--------------|----------|
| `.List()` | Fast (in-memory) | Slow (fetches all) | Iterate over all resources |
| `.Fetch(keys)` | Fast (indexed lookup) | Fast (cached/on-demand) | Lookup specific resources |

**Rule of thumb:**
- Use `.List()` when you need most or all resources
- Use `.Fetch()` when you need specific resources
- With memory store, both are fast
- With cached store, prefer `.Fetch()` for selective access

### GetSingle() Helper

For convenience, stores also provide `.GetSingle()` which returns a single resource or `null`:

```jinja2
{# Instead of looping #}
{% set secret = resources.secrets.GetSingle("default", "my-secret") %}
{% if secret %}
  {{ secret.data.password | b64decode }}
{% endif %}
```

This is equivalent to:
```jinja2
{% for secret in resources.secrets.Fetch("default", "my-secret") %}
  {{ secret.data.password | b64decode }}
{% endfor %}
```

See [Templating Guide](./templating.md#using-list-method) for more template patterns.

## Performance Implications

The choice between memory and cached stores affects multiple performance dimensions:

### Memory Usage

**Memory Store:**
- **Per-resource overhead**: 1-10 KB depending on resource type
- **Total usage**: Number of resources × average size
- **Example**: 1000 ingresses × 1.5 KB = 1.5 MB

**Cached Store:**
- **Per-reference overhead**: ~100 bytes (namespace, name, index keys)
- **Cache overhead**: Cache size × average resource size
- **Example**: 1000 secret references × 100 bytes = 100 KB (plus cache)

**Memory savings example:**
- 1000 secrets with 10 KB certificates each
- Memory store: 10 MB
- Cached store: 100 KB + cache (bounded by cache_ttl and access patterns)

### Template Rendering Time

**Memory Store:**
- `.List()`: <1ms (return in-memory slice)
- `.Fetch()`: <1ms (indexed lookup)
- **No API calls during rendering**

**Cached Store:**
- `.List()`: Triggers fetch for all resources (slow)
- `.Fetch()` with cache hit: <1ms (cached lookup)
- `.Fetch()` with cache miss: 10-50ms (API call)
- **API calls add latency**

**Rendering time example:**
- Template accessing 10 secrets
- Memory store: ~1ms total
- Cached store (all cached): ~1ms total
- Cached store (all misses): ~100-500ms total

### Kubernetes API Load

**Memory Store:**
- **Initial sync**: Single list operation per resource type
- **Updates**: Watch events only (efficient)
- **Template rendering**: No API calls

**Cached Store:**
- **Initial sync**: Single list operation (same as memory)
- **Updates**: Watch events only (same as memory)
- **Template rendering**: API calls on cache misses
- **Additional load**: Depends on cache hit rate

**API call estimation:**
- 100 template renders per minute
- 10 secrets accessed per render
- 50% cache hit rate
- API calls: 100 × 10 × 0.5 = 500 per minute

### Performance Recommendations

**Optimize for memory store:**
- Use field filtering to remove unnecessary data (see configuration.md#watched_resources_ignore_fields)
- Watch only necessary namespaces
- Use label selectors to limit resource count

**Optimize for cached store:**
- Set appropriate cache TTL (balance freshness vs API load)
- Use `.Fetch()` instead of `.List()` in templates
- Access resources conditionally (don't fetch unless needed)
- Consider cache pre-warming for frequently accessed resources

## Choosing the Right Store Type

Use this decision guide to select the appropriate store type for each resource:

### Use Memory Store When:

- ✅ Resources are small to medium size (<10 KB)
- ✅ You iterate over most/all resources in templates
- ✅ You need fastest possible template rendering
- ✅ Memory is not constrained
- ✅ Resource count is manageable (<10,000)

**Common resources for memory store:**
- Ingress resources
- Service resources
- EndpointSlice resources
- Small ConfigMap resources
- Deployment/Pod resources (if watching)

### Use Cached Store When:

- ✅ Resources are large (>10 KB)
- ✅ You access resources selectively by key
- ✅ Memory is constrained
- ✅ Resource count is high (>1,000)
- ✅ Can tolerate API latency on cache misses

**Common resources for cached store:**
- Secrets containing certificates (large PEM data)
- Secrets for authentication (selective access via annotations)
- Large ConfigMap resources
- Resources with high volume but infrequent access

### Decision Matrix

| Resource Type | Typical Size | Access Pattern | Recommended Store |
|---------------|--------------|----------------|-------------------|
| Ingress | 1-2 KB | Iterate all | Memory |
| Service | 1-5 KB | Iterate all | Memory |
| EndpointSlice | 2-5 KB | Lookup by service | Memory |
| Secret (TLS) | 5-20 KB | Lookup by name | Cached |
| Secret (auth) | <1 KB | Lookup by annotation | Cached |
| ConfigMap (small) | <5 KB | Iterate all | Memory |
| ConfigMap (large) | >10 KB | Selective access | Cached |

### Mixed Store Configuration

You can use different stores for different resource types:

```yaml
watched_resources:
  # High-frequency access, small resources
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    store: full  # Memory store
    index_by: ["metadata.namespace", "metadata.name"]

  services:
    api_version: v1
    kind: Service
    store: full  # Memory store
    index_by: ["metadata.namespace", "metadata.name"]

  endpoints:
    api_version: discovery.k8s.io/v1
    kind: EndpointSlice
    store: full  # Memory store
    index_by: ["metadata.labels.kubernetes\\.io/service-name"]

  # Selective access, large resources
  secrets:
    api_version: v1
    kind: Secret
    store: on-demand  # Cached store
    cache_ttl: 2m
    index_by: ["metadata.namespace", "metadata.name"]
```

This is the recommended approach for most deployments.

See [Configuration Reference](./configuration.md#watched_resources) for complete configuration options.

## Configuration Examples

### Example 1: Standard Ingress Controller Configuration

Typical configuration for an ingress controller with memory store:

```yaml
watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    index_by: ["metadata.namespace", "metadata.name"]
    # Memory store (default)

  services:
    api_version: v1
    kind: Service
    index_by: ["metadata.namespace", "metadata.name"]

  endpoints:
    api_version: discovery.k8s.io/v1
    kind: EndpointSlice
    index_by: ["metadata.labels.kubernetes\\.io/service-name"]
```

**Template usage:**
```jinja2
{# Generate backends with dynamic servers #}
{% for ingress in resources.ingresses.List() %}
  {% for rule in (ingress.spec.rules | default([])) %}
    {% for path in (rule.http.paths | default([])) %}
      {% set service_name = path.backend.service.name %}
      {% set port = path.backend.service.port.number %}

      backend ing_{{ ingress.metadata.name }}_{{ service_name }}
        balance roundrobin
        {# Cross-resource lookup: service → endpoints #}
        {% for endpoint_slice in resources.endpoints.Fetch(service_name) %}
          {% for endpoint in (endpoint_slice.endpoints | default([])) %}
            {% for address in (endpoint.addresses | default([])) %}
              server {{ endpoint.targetRef.name }} {{ address }}:{{ port }} check
            {% endfor %}
          {% endfor %}
        {% endfor %}
    {% endfor %}
  {% endfor %}
{% endfor %}
```

### Example 2: Authentication with Cached Secrets

Configuration using cached store for authentication secrets:

```yaml
watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    index_by: ["metadata.namespace", "metadata.name"]

  # Secrets accessed selectively via annotations
  secrets:
    api_version: v1
    kind: Secret
    store: on-demand  # Cached store
    cache_ttl: 5m
    index_by: ["metadata.namespace", "metadata.name"]
```

**Template usage:**
```jinja2
{# Generate userlist sections for basic auth #}
{% set ns = namespace(processed=[]) %}
{% for ingress in resources.ingresses.List() %}
  {% set auth_type = ingress.metadata.annotations["haproxy.org/auth-type"] | default("") %}
  {% if auth_type == "basic-auth" %}
    {% set auth_secret = ingress.metadata.annotations["haproxy.org/auth-secret"] | default("") %}
    {% if auth_secret and auth_secret not in ns.processed %}
      {% set ns.processed = ns.processed + [auth_secret] %}

      {# Parse namespace/name from annotation #}
      {% if "/" in auth_secret %}
        {% set secret_ns = auth_secret.split("/")[0] %}
        {% set secret_name = auth_secret.split("/")[1] %}
      {% else %}
        {% set secret_ns = ingress.metadata.namespace %}
        {% set secret_name = auth_secret %}
      {% endif %}

      {# Fetch specific secret (cached or API call) #}
      {% set secret = resources.secrets.GetSingle(secret_ns, secret_name) %}
      {% if secret %}
        userlist auth_{{ secret_ns }}_{{ secret_name }}
        {% for username in secret.data | default({}) %}
          user {{ username }} password {{ secret.data[username] | b64decode }}
        {% endfor %}
      {% endif %}
    {% endif %}
  {% endif %}
{% endfor %}
```

**Why cached store here**: Secrets are large and accessed selectively (only those referenced in annotations). Cached store minimizes memory usage.

See [Templating Guide](./templating.md#authentication-annotations) for complete authentication examples.

### Example 3: TLS Certificates with Cached Store

Configuration for TLS certificates using cached store:

```yaml
watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    index_by: ["metadata.namespace", "metadata.name"]

  # Large TLS secrets accessed conditionally
  secrets:
    api_version: v1
    kind: Secret
    store: on-demand
    cache_ttl: 10m  # Longer TTL for certificates
    index_by: ["metadata.namespace", "metadata.name"]
```

**Template usage:**
```jinja2
{# Generate SSL certificates from ingress TLS configuration #}
{% for ingress in resources.ingresses.List() %}
  {% if ingress.spec.tls %}
    {% for tls in ingress.spec.tls %}
      {% set secret_name = tls.secretName %}
      {% set namespace = ingress.metadata.namespace %}

      {# Fetch TLS secret only if ingress has TLS #}
      {% set secret = resources.secrets.GetSingle(namespace, secret_name) %}
      {% if secret %}
        {# SSL certificate content for {{ namespace }}/{{ secret_name }} #}
        {{ secret.data["tls.crt"] | b64decode }}
        {{ secret.data["tls.key"] | b64decode }}
      {% endif %}
    {% endfor %}
  {% endif %}
{% endfor %}
```

**Why cached store here**: Certificate secrets are 5-20 KB each. Only ingresses with TLS need certificate data. Cached store avoids loading all certificates into memory.

### Example 4: Namespace-Restricted Watching

Watch resources only in specific namespaces:

```yaml
watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    namespace: production  # Only watch 'production' namespace
    index_by: ["metadata.name"]  # Single key (namespace is implicit)

  services:
    api_version: v1
    kind: Service
    namespace: production
    index_by: ["metadata.name"]
```

**Benefits:**
- Reduced memory usage (fewer resources)
- Reduced API load (narrower watch scope)
- Simplified indexing (single key)

**Template usage:**
```jinja2
{# Fetch by name only (namespace is implicit) #}
{% for ingress in resources.ingresses.Fetch("api-gateway") %}
  {# ... #}
{% endfor %}
```

See [Configuration Reference](./configuration.md#namespace) for namespace restrictions.

## Troubleshooting

### Store Returns Empty Results

**Symptoms:**
- `.List()` returns empty array
- `.Fetch()` returns no resources
- Templates generate empty output

**Diagnosis:**

1. **Check if watcher has completed initial sync:**
   ```bash
   # Check controller logs for sync completion
   kubectl logs deployment/haproxy-template-ic | grep "initial sync complete"
   ```

2. **Verify resources exist matching the watch configuration:**
   ```bash
   # Check if resources exist
   kubectl get ingresses -A

   # Check if resources match label selector (if configured)
   kubectl get ingresses -A -l app=myapp
   ```

3. **Verify RBAC permissions:**
   ```bash
   # Check if service account can list resources
   kubectl auth can-i list ingresses --all-namespaces \
     --as=system:serviceaccount:haproxy-system:haproxy-template-ic
   ```

4. **Check index_by configuration matches template usage:**
   ```yaml
   # Configuration
   index_by: ["metadata.namespace", "metadata.name"]
   ```
   ```jinja2
   {# Template must match index order #}
   {% for ing in resources.ingresses.Fetch("default", "my-app") %}
   ```

**Solutions:**
- Wait for initial sync to complete
- Verify resource selectors (namespace, labels)
- Fix RBAC permissions
- Align template `.Fetch()` calls with `index_by` configuration

### High Memory Usage

**Symptoms:**
- Controller pod OOMKilled
- High resident memory (RSS)
- Memory growth over time

**Diagnosis:**

1. **Count resources in each store:**
   ```bash
   # Enable debug port and check /debug/vars endpoint
   kubectl port-forward pod/haproxy-template-ic-xxx 6060:6060
   curl http://localhost:6060/debug/vars | jq .
   ```

2. **Identify large resource types:**
   - Secrets with large data (certificates)
   - ConfigMaps with large configuration
   - High resource counts (>10,000)

3. **Check field filtering configuration:**
   ```yaml
   # Are unnecessary fields being stored?
   watched_resources_ignore_fields:
     - metadata.managedFields
     - metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"]
   ```

**Solutions:**

1. **Switch large resources to cached store:**
   ```yaml
   secrets:
     store: on-demand
     cache_ttl: 2m
   ```

2. **Add field filtering:**
   ```yaml
   watched_resources_ignore_fields:
     - metadata.managedFields
     - metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"]
   ```
   See [Configuration Reference](./configuration.md#watched_resources_ignore_fields) for field filtering syntax.

3. **Limit watch scope:**
   ```yaml
   ingresses:
     namespace: production  # Watch single namespace
     label_selector:
       app: myapp  # Filter by labels
   ```

4. **Monitor memory with container limits:**
   ```yaml
   resources:
     limits:
       memory: 256Mi  # Adjust based on resource count
   ```

### Slow Template Rendering

**Symptoms:**
- Template rendering takes >1 second
- Deployment delays
- High CPU usage during rendering

**Diagnosis:**

1. **Check if using cached store with `.List()`:**
   ```jinja2
   {# This triggers fetch for ALL resources - slow with cached store #}
   {% for secret in resources.secrets.List() %}
   ```

2. **Check for cache misses:**
   ```bash
   # Look for "cache miss" in logs with verbose logging
   kubectl logs deployment/haproxy-template-ic | grep "cache miss"
   ```

3. **Profile template rendering:**
   - Enable debug mode (verbose=2)
   - Check rendering duration in logs

**Solutions:**

1. **Use cached store efficiently:**
   ```jinja2
   {# Instead of List() #}
   {% for secret in resources.secrets.List() %}  {# SLOW #}

   {# Use Fetch() with specific keys #}
   {% set secret = resources.secrets.GetSingle(namespace, name) %}  {# FAST #}
   ```

2. **Switch to memory store for frequently accessed resources:**
   ```yaml
   ingresses:
     store: full  # Use memory store instead of cached
   ```

3. **Increase cache TTL to reduce API calls:**
   ```yaml
   secrets:
     cache_ttl: 5m  # Longer cache duration
   ```

4. **Optimize template logic:**
   - Avoid nested loops over large collections
   - Cache computed values in variables
   - Use `{% set %}` for repeated expressions

### Cache Miss Debugging

**Symptoms:**
- Inconsistent rendering performance
- Occasional slow template renders
- High API call rate

**Diagnosis:**

1. **Enable debug logging:**
   ```yaml
   logging:
     verbose: 2  # Debug level
   ```

2. **Monitor cache hit/miss ratio in logs:**
   ```bash
   kubectl logs deployment/haproxy-template-ic | grep -E "cache (hit|miss)"
   ```

3. **Check cache TTL configuration:**
   ```yaml
   secrets:
     cache_ttl: 2m10s  # Current TTL
   ```

**Solutions:**

1. **Increase cache TTL if resources don't change frequently:**
   ```yaml
   secrets:
     cache_ttl: 10m  # Longer TTL for stable resources
   ```

2. **Pre-warm cache by accessing resources early:**
   ```jinja2
   {# Access commonly used resources at template start #}
   {% set _ = resources.secrets.GetSingle("default", "common-secret") %}
   ```

3. **Switch to memory store if cache miss rate is high:**
   ```yaml
   secrets:
     store: full  # Switch to memory store
   ```

### Common Misconfigurations

**Issue: index_by doesn't match template usage**

```yaml
# Configuration
index_by: ["metadata.namespace", "metadata.name"]
```

```jinja2
{# Template - WRONG: only provides one key #}
{% for ing in resources.ingresses.Fetch("my-app") %}
```

**Solution:** Match template parameters to index_by order:
```jinja2
{# Correct: both namespace and name #}
{% for ing in resources.ingresses.Fetch("default", "my-app") %}
```

**Issue: Using memory store for large resources**

```yaml
# Bad: Large secrets in memory
secrets:
  store: full  # Default, stores all in memory
```

**Solution:** Use cached store for large resources:
```yaml
secrets:
  store: on-demand
  cache_ttl: 2m
```

**Issue: Using .List() with cached store**

```jinja2
{# Slow: Fetches all resources from API #}
{% for secret in resources.secrets.List() %}
```

**Solution:** Use `.Fetch()` for selective access:
```jinja2
{# Fast: Fetches only required resources #}
{% set secret = resources.secrets.GetSingle(namespace, name) %}
```

See [Configuration Reference](./configuration.md) for additional configuration guidance.

## See Also

- [Configuration Reference](./configuration.md) - Complete configuration syntax and field descriptions
- [Templating Guide](./templating.md) - Template syntax and resource access patterns
- [pkg/k8s README](../pkg/k8s/README.md) - Store implementation details and API documentation
- [Architecture Design](./development/design.md) - System architecture and component interactions
