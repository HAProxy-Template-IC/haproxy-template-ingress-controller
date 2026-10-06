# HAPTIC agent instructions

Read [CLAUDE.md](CLAUDE.md) before changing this repository, and read the applicable
package's `CLAUDE.md` before editing that package. These instructions apply to all
agents and subagents.

**Never add Lua-based HAPTIC features or workarounds.** This includes prototypes,
optional features, and per-thread scripts. Follow [No Lua features](CLAUDE.md#no-lua-features-rule-4);
passing tests or benchmarks does not authorize an exception. Carry this constraint
into every delegated task.
