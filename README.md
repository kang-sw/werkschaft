<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="ai-docs/ref/brand/werkschaft-dark.svg">
    <img src="ai-docs/ref/brand/werkschaft-light.svg" alt="Werkschaft" width="520">
  </picture>
</p>

# Werkschaft

Werkschaft (`ws`) is an agentic development environment: a workflow, the
plugin packages that carry it into AI coding hosts, and the native runtime
behind them. A lead session talks with you, settles decisions, and captures
work as tickets; worker agents execute those tickets, independent reviewers
check the result, and the lead carries it to merge and release.

## How It Works

```text
Full ceremony:  discuss -> ticket -> run -> review
Delegate:       delegate <bounded task> -> follow-up -> review
Direct:         run <ticket>
Queue:          run (no argument) -> next ready ticket, one worker per cycle
Review:         review [branch|range] -> verdict -> fix | comment | discuss | merge
Release:        ship <project> -> release gate -> execute
```

- **The lead decides, workers execute.** The lead owns the conversation and
  the decisions; `run` hands a ticket to a worker, which implements,
  verifies, gets an independent review, and commits.
- **Tickets are files.** Work lives under `ai-docs/tickets/`, and its status
  is the directory it sits in: `idea/`, `todo/`, `ready/`, `.done/`,
  `.dropped/`.
- **`AGENTS.md` is the project's root context.** Hosts that need their own
  file (such as Claude Code's `CLAUDE.md`) get a shim that points at it.
- **Host-neutral.** The same skills run on Codex and Claude Code through
  the `ws` MCP tools.

## Packages

| Path | Package |
|---|---|
| `agents-plugin/` | `ws` plugin: workflow skills, hooks, and MCP wiring |
| `agents-plugin-wsflow/` | `wsflow`: the same product with conservative defaults (optional Claude Code features off) |
| `agents-plugin-tool/` | Go source of the native `ws-mcp` runtime and tooling |

## Install

### Claude Code

From a local clone (macOS/Linux):

```sh
./scripts/install-claude-plugin.sh
```

On Windows (PowerShell):

```powershell
pwsh -NoProfile -File scripts\install-claude-plugin.ps1
```

Or via the GitHub marketplace:

```sh
claude plugin marketplace add kang-sw/werkschaft
claude plugin install ws@kang-sw-devenv
```

`.claude-plugin/marketplace.json` also lists `wsflow` for manual
installation.

### Codex

Codex installs use the repository marketplace entries under
`.agents/plugins/`, which expose both `ws` and `wsflow`.

## Repository Layout

```
agents-plugin/         ws plugin package
agents-plugin-wsflow/  wsflow plugin package (conservative defaults)
agents-plugin-tool/    native ws MCP runtime and tooling source
ai-docs/               tickets, manuals, references, and repo notes
scripts/               repository helper scripts
```

`ai-docs/` is this repository's own workflow instance: `tickets/` by status
directory, `manuals/` for procedures, `ref/` for static references,
`ws-notes/` for the tracked note layer, and `WORKFLOW.md` as the plugin-less
maintenance guide.

## Contributing

All code in this repository is authored by AI agents. Human-authored code is
not accepted.

Contributions follow the workflow described in
[`ai-docs/WORKFLOW.md`](ai-docs/WORKFLOW.md) and the project's
[`AGENTS.md`](AGENTS.md). Every change must have a corresponding ticket under
`ai-docs/tickets/` before implementation begins. A lead session settles the
design, a worker agent implements, and an independent reviewer checks the
result.

## License

Licensed under the [Apache License 2.0](LICENSE).
