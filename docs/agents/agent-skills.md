# Publishing the HAPTIC agent skill

Research checked on 2026-09-18. The public skill lives in `skills/haptic/` and
serves operators customizing HAPTIC from their own repositories. Repository
contributor instructions stay in `CLAUDE.md` and package context files.

## Findings and choices

| Surface | Evidence | HAPTIC choice |
| --- | --- | --- |
| Portable skill | The [Agent Skills specification](https://agentskills.io/specification) defines `SKILL.md` with `name` and `description`, plus optional resources. | Maintain one tool-neutral directory with standard frontmatter. |
| Context loading | [Authoring guidance](https://agentskills.io/skill-creation/best-practices) recommends domain-specific procedures, focused scope, and references loaded on demand. [Anthropic's guidance](https://platform.claude.com/docs/en/agents-and-tools/agent-skills/best-practices) likewise separates discovery metadata, instructions, and supporting files. | Keep the controller model and task selection in the entrypoint; split customization, resource access, and validation into references. |
| Codex | [OpenAI's skill documentation](https://learn.chatgpt.com/docs/build-skills) supports standalone repository/user skills and recommends plugins for broader distribution across ChatGPT surfaces. | Support standalone `.agents/skills/haptic`; don't imply that every ChatGPT surface loads local skills. |
| Claude Code | [Claude Code's documentation](https://code.claude.com/docs/en/skills) supports `.claude/skills/`, on-demand activation, and `/name` invocation. | Install the same package in its native directory without Claude-only frontmatter. |
| GitHub Copilot | [VS Code's documentation](https://code.visualstudio.com/docs/agent-customization/agent-skills) supports `.github/skills/`, `.claude/skills/`, and `.agents/skills/`. | Document `.github/skills/haptic` for manual installation. |
| Cross-agent installer | The [Skills CLI](https://github.com/vercel-labs/skills) supports website discovery, GitLab URLs, named skill selection, project/global scope, and update/removal commands. | Use `npx skills add https://haproxy-haptic.org --skill haptic`, with GitLab as an alternative; test with published `skills@1.7.0`. |
| Product precedents | [Supabase](https://supabase.com/docs/guides/ai-tools/ai-skills) documents project installation and updates. [Cloudflare](https://github.com/cloudflare/skills) offers the common installer and manual folder copying. | Put an installation command on the landing page and README, with a task-oriented guide and manual download. |
| Agent-readable docs | The [llms.txt proposal](https://llmstxt.org/) describes a small Markdown index, page Markdown, and discovery links. It isn't a skill loader or a guarantee of agent adoption. | Keep the existing generated docs, link the skill from the index, and expose Markdown through HTML link relations. |
| Web discovery | Cloudflare's draft [discovery proposal](https://github.com/cloudflare/agent-skills-discovery-rfc) and the [Skills CLI implementation](https://github.com/vercel-labs/skills/blob/main/src/providers/wellknown.ts) describe `/.well-known/agent-skills/`. | Publish the v0.2.0 archive index with a content digest at the product origin. Keep Git and ZIP alternatives for clients without this discovery mechanism. |

These are engineering choices for HAPTIC, not requirements of the shared format.
The published installer was checked as well as its upstream documentation;
client paths and flags can change independently of HAPTIC.

## Packaging and versioning

`docs/site/hooks/agent_skills.py` publishes the directory as raw files and
`agent-skills/haptic.zip` in each documentation build. It includes the repository
license and provides a SHA-256 checksum. `SKILL.md` is at the archive root, as
required by the Skills CLI's discovery provider. Stable ordering and fixed ZIP
metadata keep the archive reproducible. The skill's source stays beside the implementation, so a
release checkout produces its own skill snapshot without a second release process.

The landing build uses the same hook to publish the package at the site root and
an index at `/.well-known/agent-skills/index.json`. Its description comes from
`SKILL.md`; its digest covers the exact archive bytes. The relative archive URL
also resolves under an MR preview's path prefix. The existing Pages publisher
copies the complete landing output, including hidden directories, on dev builds.

Both website and GitLab installation follow `main`. The guide states this and
offers the documentation version's ZIP for a snapshot. Agents must inspect the operator's
actual version; installing current instructions doesn't upgrade the controller.
The landing page links to the development guide because older stable docs don't
contain the skill page.

No bespoke installer, global instruction replacement, client-specific tool
permissions, MCP server, or marketplace account is necessary for this package.
A skill supplies knowledge and procedures. An MCP integration would supply
operations or data access and needs a separate product design. A plugin could
later wrap the same skill for client-native discovery without forking its content.

## Verification and maintenance

- `make test-agent-skill-package` builds a minimal MkDocs fixture to check raw
  downloads, ZIP contents, relative references, checksum, and reproducibility.
  A dedicated CI test job runs it when the package or build hook changes.
- `make test-agent-skill` runs the packaged resource example and renders the
  header values through Helm before native validation. The library validation
  job runs it as part of `make validate-helm-libraries`.
- Build both documentation sites with `mkdocs build --strict`, then run the
  landing page's visual checks at desktop and mobile sizes. Check the new copy
  button and guide/download links in a browser.
- Exercise the published Skills CLI against a temporary project using the
  source directory and website discovery endpoint. Check references are installed,
  then update and remove the skill and verify cleanup. Test manual ZIP extraction
  with the guide's commands. Keep evaluation installs out of the developer's
  personal skills directory.

For behavioral evaluation, give an agent only the installed skill and a fixture
project. Ask it to add a scoped annotation, consume a custom CRD, or diagnose a
missing schema. Check the resulting configuration with native validation and
inspect its tool trace. Include an unrelated HAProxy task as a selection control.
Package validation and example execution do not measure model selection accuracy
or prove that every supported client will produce correct customizations.
