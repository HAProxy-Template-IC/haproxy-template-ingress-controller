## Context

This is a brownfield baseline change — no code modifications, only specification artifacts. The HAProxy Template Ingress Controller is a fully implemented, production-ready Kubernetes operator. The codebase has comprehensive CLAUDE.md files and README documentation but lacks formal, structured specifications that can serve as a behavioral contract for future changes.

The controller follows an event-driven architecture with pure components wrapped in event adapters, managing HAProxy configurations through a template-driven approach using the Scriggo engine.

## Goals / Non-Goals

**Goals:**

- Reverse-engineer behavioral specifications from the existing implementation
- Establish a baseline that future changes can diff against (ADDED/MODIFIED/REMOVED)
- Create testable requirements with WHEN/THEN scenarios
- Cover all 15 major capability areas identified in the proposal

**Non-Goals:**

- No code changes or refactoring
- Not a replacement for existing CLAUDE.md files (those are developer context; specs are behavioral contracts)
- Not aspirational — specs describe what the system *does*, not what it *should* do
- Not exhaustive coverage of every internal implementation detail — focus on user-facing and architecturally significant behavior

## Decisions

### Decision 1: One spec file per capability

Each capability listed in the proposal gets its own `specs/<capability>/spec.md` file. This keeps specs focused and allows future changes to target individual capabilities without touching unrelated specs.

Alternative considered: Grouping related capabilities (e.g., all observability in one spec). Rejected because it makes delta specs harder to scope.

### Decision 2: SHALL/MUST for normative language

Using RFC 2119-style language (SHALL, MUST) for requirements. This makes requirements unambiguous and testable, rather than descriptive prose that could be interpreted loosely.

### Decision 3: Specs derived from implementation, not documentation

Specs are reverse-engineered from source code, not from README/CLAUDE.md files. Documentation may describe intent or aspirational behavior; code shows actual behavior. Where documentation and code disagree, the code wins.

### Decision 4: WHEN/THEN scenarios as behavioral tests

Every requirement includes at least one scenario in WHEN/THEN format. These scenarios serve as both documentation and potential test cases. They describe observable behavior, not internal implementation.

## Risks / Trade-offs

- **Staleness risk**: Specs become outdated if code changes without updating specs. Mitigation: OpenSpec's delta workflow ensures changes go through specs first.
- **Incomplete coverage**: Some edge cases may be missed in reverse-engineering. Mitigation: Specs can be incrementally refined as gaps are discovered.
- **Over-specification**: Specifying internal implementation details constrains future refactoring. Mitigation: Focus on behavioral contracts (inputs → outputs), not internal algorithms.
