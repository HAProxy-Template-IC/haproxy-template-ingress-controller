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
// FCGIApps, CrtStores, LogProfiles (v3.1+), Traces (v3.1+),
// AcmeProviders (v3.2+), plus the Enterprise sections (UDPLBs,
// WAFProfiles, BotMgmtProfiles, etc.) when an Enterprise parser is used.
```

Creation is not free — `New` constructs a client-native parser and pre-initialises its section registry. Cache the `*Parser` if you're parsing many configurations.

### Pointer-Based Child Indexes

Beyond the section slices, the parser builds pointer-based indexes for child resources that comparators iterate hot:

- `cfg.ServerIndex` — `backend name → server name → *Server`
- `cfg.ServerTemplateIndex` — `backend name → template prefix → *ServerTemplate`
- `cfg.BindIndex` — `frontend name → bind name → *Bind`
- `cfg.PeerEntryIndex` — `peer section name → peer entry name → *PeerEntry`
- `cfg.NameserverIndex` — `resolver name → nameserver name → *Nameserver`
- `cfg.MailerEntryIndex` — `mailers section name → mailer entry name → *MailerEntry`
- `cfg.UserIndex` — `userlist name → username → *User`
- `cfg.GroupIndex` — `userlist name → group name → *Group`

The corresponding value maps inside the parent models (e.g. `Backend.Servers`) are left nil — callers should use the indexes above to avoid copying large structs (`models.Server` is ~1.5 KB) on every iteration. Comparators in `pkg/dataplane/comparator` already do this; new code that walks servers/binds/etc. should follow the same pattern.

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
