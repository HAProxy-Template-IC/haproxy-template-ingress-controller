# Watched resources and schemas

Define any resource type in `watchedResources`; the key becomes its template
store name. `resources` is the plural Kubernetes API resource name, not its Kind.

```yaml
watchedResources:
  maintenancepolicies:
    apiVersion: platform.example.com/v1
    resources: maintenancepolicies
    indexBy:
      - metadata.namespace
      - metadata.name
```

The [custom resource example](../assets/maintenance-config.yaml) uses this watch.
Its [CRD](../assets/schemas/maintenancepolicy.yaml) supplies the schema offline and
defines the API an operator would install in a cluster. No Go registration is
needed. The example is a standalone demonstration; merge its watch, snippet,
and tests into an existing installation instead of replacing its entire config.

## Typed and untyped access

With a schema, `resources.maintenancepolicies.List()` returns typed pointers, and
`policy.spec.enabled` reads the CRD's boolean field. The controller obtains schemas
from the API server; offline validation needs `--schema-dir`. That directory
accepts full CRD YAMLs and supported OpenAPI schemas. Match it to the target
cluster; a schema for a different API version can hide a compatibility issue.

Use direct fields for operator templates with schemas. Range optional typed
slices directly: nil slices have zero elements. Wrapping a typed collection in
`fallback(..., []any{})` erases its element type. Use `len(slice)` to test emptiness;
a generated value struct can't be compared to nil. The resource type is available
as `resources.<key>.T` for macro signatures and type switches.

Use `dig()`, `toSlice()`, and explicit defaults for untyped maps, schema-less
resources, and polymorphic `any` boundaries. Bundled library edits must follow
their package's resource-agnostic rules. Don't add Go helpers tied to a routing
kind to avoid handling a map or schema in templates.

## Lookups and scope

`Fetch()` arguments follow `indexBy` order. With the two indexes above:

| Call | Result |
| --- | --- |
| `resources.maintenancepolicies.List()` | All objects in the store |
| `resources.maintenancepolicies.Fetch("apps")` | Objects in that namespace |
| `resources.maintenancepolicies.GetSingle("apps", "public")` | One object or nil |

`GetSingle` fails on multiple matches. Read and conversion failures are errors,
not empty results. Don't silently substitute an empty resource set.

Avoid whole-store scans when an index answers the question, especially for
on-demand stores. Include namespace in namespaced relationships and generated
identities. A watch may filter or strip fields before templates see it; inspect
the effective watch and preserve fields your template reads.

`labelSelector` accepts equality pairs. `fieldSelector` is HAPTIC's client-side
JSONPath equality filter and can select fields beyond Kubernetes server-side
field selectors. Consult `watching-resources/index.md` for storage modes,
selectors, optional API discovery, and HTTP resources.

When adding a watch, include the corresponding RBAC and admission coverage. The
chart derives these from its configured watch set; manually managed deployments
must supply them too. Do not narrow admission coverage to bypass rejected input.
