# pkg/dataplane/parser

Parses HAProxy configuration text into a typed `StructuredConfig` that the comparator can diff. Wraps [`github.com/haproxytech/client-native`](https://github.com/haproxytech/client-native) so the rest of the controller doesn't have to deal with that library's direct API.

## Usage

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"

p, err := parser.New()
if err != nil {
    return err
}

cfg, err := p.ParseFromString(haproxyConfig)
if err != nil {
    return err        // syntax error — wrap in pkg/dataplane.ParseError if you're on the public path
}

// cfg exposes Global, Defaults, Frontends, Backends, Peers, Resolvers,
// Mailers, Caches, Rings, HTTPErrors, Userlists, Programs, LogForwards,
// LogProfiles, FCGIApps, CrtStores, AcmeProviders, plus the Enterprise
// sections when present.
```

Creation is not free — `New` constructs a client-native parser and pre-initialises its section registry. Cache the `*Parser` if you're parsing many configurations.

## What This Package Is Not

- **Not a validator.** `ParseFromString` fails on *syntactically invalid* input only. Semantic checks (cross-references, `haproxy -c`) are `pkg/dataplane/validator.go` / `pkg/dataplane.ValidateConfiguration`.
- **Not for single-directive lookups.** If you want "does this config have frontend X?" you operate on the returned `StructuredConfig`, you don't reparse.
- **Not an authoring API.** Emitting HAProxy config is the template engine's job; the parser only reads.

## See Also

- [`pkg/dataplane`](../) — validation + sync entry points that wrap this parser
- [`pkg/dataplane/comparator`](../comparator/) — consumer of `StructuredConfig`
- `pkg/dataplane/CLAUDE.md` — client-native limitations, parser error wrapping

## License

Apache-2.0 — see root `LICENSE`.
