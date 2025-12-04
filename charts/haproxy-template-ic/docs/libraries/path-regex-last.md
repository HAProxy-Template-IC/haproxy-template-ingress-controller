# Path Regex Last Library

The Path Regex Last library overrides the default path matching order to optimize HAProxy performance by evaluating regular expression paths last.

## Overview

By default, the base library evaluates paths in this order (de facto industry standard):

1. **Exact** - Exact string match
2. **Regex** - Regular expression match
3. **Prefix** - String prefix match

The Path Regex Last library changes this to a performance-first order:

1. **Exact** - Exact string match
2. **Prefix** - String prefix match (faster)
3. **Regex** - Regular expression match (slower)

This optimization improves performance because exact and prefix matching are significantly faster than regex matching in HAProxy.

## Configuration

```yaml
controller:
  templateLibraries:
    pathRegexLast:
      enabled: true  # Disabled by default
```

!!! warning "Behavior Change"
    Enabling this library changes routing behavior. A regex path that would match before a prefix path will now match after. Test thoroughly before enabling in production.

## Extension Points

### Extension Points Used

The Path Regex Last library **overrides** (not extends) a snippet from base.yaml:

| Snippet | Action | What It Changes |
|---------|--------|-----------------|
| `frontend-routing-logic` | Override | Path matching evaluation order |

Unlike other libraries that implement extension point patterns (like `backends-*`), this library provides its own complete implementation of `frontend-routing-logic` that replaces the base library's version.

### How Override Works

When both libraries are loaded, the merge order ensures path-regex-last.yaml's `frontend-routing-logic` takes precedence over base.yaml's version:

```
Merge order (lowest to highest priority):
1. base.yaml          <- defines frontend-routing-logic
2. ssl.yaml
3. ingress.yaml
4. gateway.yaml
5. haproxytech.yaml
6. haproxy-ingress.yaml
7. path-regex-last.yaml  <- overrides frontend-routing-logic
8. values.yaml
```

## Features

### Performance-First Path Matching

The library reorders the HAProxy map lookups:

**Default order (base.yaml):**
```haproxy
# Path matching order: Exact > Regex > Prefix-exact > Prefix
http-request set-var(txn.path_match) ...,map(path-exact.map)
http-request set-var(txn.path_match) ...,map_reg(path-regex.map) if !{ var(txn.path_match) -m found }
http-request set-var(txn.path_match) ...,map(path-prefix-exact.map) if !{ var(txn.path_match) -m found }
http-request set-var(txn.path_match) ...,map_beg(path-prefix.map) if !{ var(txn.path_match) -m found }
```

**Performance-first order (path-regex-last.yaml):**
```haproxy
# Path matching order: Exact > Prefix-exact > Prefix > Regex
http-request set-var(txn.path_match) ...,map(path-exact.map)
http-request set-var(txn.path_match) ...,map(path-prefix-exact.map) if !{ var(txn.path_match) -m found }
http-request set-var(txn.path_match) ...,map_beg(path-prefix.map) if !{ var(txn.path_match) -m found }
http-request set-var(txn.path_match) ...,map_reg(path-regex.map) if !{ var(txn.path_match) -m found }
```

### When to Use

**Enable this library when:**
- Your deployment has many prefix paths and few regex paths
- Performance is critical and you want to minimize regex evaluations
- You understand the routing behavior change and it's acceptable

**Keep disabled when:**
- You rely on regex paths matching before prefix paths
- You're migrating from another ingress controller that uses the default order
- Compatibility with de facto standards is more important than performance

## Example Behavior Difference

Given these paths:
- `/api/users` (Prefix)
- `/api/users/[0-9]+` (Regex)

**Request:** `GET /api/users/123`

| Library | Match |
|---------|-------|
| Default (base.yaml) | Regex: `/api/users/[0-9]+` |
| Path Regex Last | Prefix: `/api/users` |

The prefix `/api/users` matches `/api/users/123` because HAProxy's `map_beg()` only checks if the path starts with the prefix.

## Performance Impact

| Match Type | HAProxy Operation | Relative Speed |
|------------|-------------------|----------------|
| Exact | String equality | Fastest |
| Prefix | String prefix check | Fast |
| Regex | PCRE regex match | Slow |

By evaluating prefix matches before regex, most requests are routed without incurring the regex matching overhead.

## See Also

- [Template Libraries Overview](../template-libraries.md) - How template libraries work
- [Base Library](base.md) - Default routing logic
- [HAProxy Ingress Library](haproxy-ingress.md) - Regex path support
