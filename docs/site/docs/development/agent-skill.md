# Customize HAPTIC with an AI agent

Install the HAPTIC skill to give your coding agent instructions for
templates, Helm customization, resource watches, and validation. It includes
runnable examples and links to HAPTIC's documentation.

The skill uses the open [Agent Skills format](https://agentskills.io/specification).
You can use the same files with Codex, Claude Code, GitHub Copilot in VS Code,
and other clients that support this format. Installation and invocation depend
on the client.

## Install in your project

Run this from the repository where you keep your HAPTIC configuration. You need
Node.js with `npx` for this installation method:

```bash
npx skills add https://haproxy-haptic.org --skill haptic
```

The [Skills CLI](https://github.com/vercel-labs/skills) lets you select your agents
and installs at project scope by default. Review and commit the generated skill
files and `skills-lock.json` if you want your team to share them.

To install for all your projects instead:

```bash
npx skills add https://haproxy-haptic.org --skill haptic --global
```

These commands install the skill published from HAPTIC's `main` branch. Give the agent your deployed
HAPTIC and HAProxy versions so it can check the matching documentation. For a
skill snapshot from the documentation version you're reading, use the download
below. Older releases may predate the skill.

You can also install from the [source repository](https://gitlab.com/haproxy-haptic/haptic/-/tree/main/skills/haptic).
This method additionally requires Git:

```bash
npx skills add https://gitlab.com/haproxy-haptic/haptic --skill haptic
```

### Download the skill

Download [haptic.zip](../agent-skills/haptic.zip). It contains `SKILL.md`,
reference files, YAML examples, and the license. Keep these files together.
You can inspect the [skill instructions](../agent-skills/haptic/SKILL.md)
before installing; a [SHA-256 checksum](../agent-skills/haptic.zip.sha256) is also available.

From your configuration repository, extract the downloaded archive into your
agent's project skills directory. These commands assume `haptic.zip` is in the
current directory:

=== "Codex"

    ```bash
    mkdir -p .agents/skills/haptic
    unzip haptic.zip -d .agents/skills/haptic
    ```

=== "Claude Code"

    ```bash
    mkdir -p .claude/skills/haptic
    unzip haptic.zip -d .claude/skills/haptic
    ```

=== "GitHub Copilot in VS Code"

    ```bash
    mkdir -p .github/skills/haptic
    unzip haptic.zip -d .github/skills/haptic
    ```

If your client accepts skill ZIP uploads, upload the archive through its skill
settings instead. For other clients, use their documented skill directory.

## Use the skill

Open your configuration project in the agent. In Codex, select `$haptic`; in
Claude Code, use `/haptic`; in VS Code, select `/haptic` in agent chat. The client
can also select the skill when your request matches its description. If it isn't
listed, start a new session and check that `haptic/SKILL.md` is under the chosen
skills directory.

Start with a concrete request and point to your files:

```text
Use the haptic skill. Read our values.yaml and add an annotation that lets
each Ingress choose a request-ID header for its own backends. Preserve our
existing routing. Add tests for an annotated Ingress, an unannotated Ingress,
and an invalid header name, then validate the resulting configuration.
```

For a custom resource:

```text
Use the haptic skill. Our MaintenancePolicy CRD is in schemas/ and sample
objects are in fixtures/. Make HAPTIC return 503 when the apps/public policy
is enabled. Use typed resource access and test enabled, disabled, absent,
and other-namespace policies.
```

The skill provides instructions. Your agent still needs access to your files
and the tools required for the task. Native validation needs `haptic` and a
matching `haproxy` binary; chart preflight also needs the chart and schemas. Follow
[validation tests](../validation-tests.md) and
[validate before deploying](../operations/validate-before-deploy.md) to set those up.

Review the generated changes and validation results before deploying. Installing
the skill doesn't connect to a cluster or change a HAPTIC installation.

## Update or remove it

For a project installation made with the Skills CLI:

```bash
npx skills update haptic --project
```

For a global installation:

```bash
npx skills update haptic --global
```

To remove a project installation:

```bash
npx skills remove haptic
```

To remove a global installation:

```bash
npx skills remove haptic --global
```

For a manual installation, replace the `haptic/` folder with a newly downloaded
copy, or delete that folder to remove the skill. Review updates before committing
them to your project.

## Agents without skill support

Give the agent the [HAPTIC skill instructions](../agent-skills/haptic/SKILL.md) and
ask it to read the linked references for your task. This supplies context for
that conversation; it doesn't enable automatic skill discovery.

HAPTIC also publishes a documentation index at `llms.txt`, a combined manual at
`llms-full.txt`, and each page as Markdown at its URL plus `index.md`. Start with
the index for your version, for example
[the development documentation index](https://haproxy-haptic.org/docs/dev/llms.txt),
and load the pages your task needs.
