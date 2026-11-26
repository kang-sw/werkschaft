---
kind: print
delegates: true
variables:
  - ExploreAgent
  - SpawnIdiom
---

# Mockup

Produce a `visual` or `interactive-mock` HTML mockup through `mockup-writer`,
mount it live on the whiteboard with `wb:panel src`, and settle its design
decisions with the user. You own scope, mode classification, and whiteboard
review; `mockup-writer` writes the mockup files.

## Modes

Classify the request before delegating:

- `visual` — static layout, typography, spacing, and visual hierarchy
  exploration. Interactive code stays minimal (option tabs or simple view
  toggles).
- `interactive-mock` — a minimal portable frontend slice exercising real user
  flows against a cleanly separated `mock-api.js` adapter layer that models
  real API contracts, so the UI can be ported into production with minimal
  churn.

## Steps

1. **Clarify scope and mode.** State your mode classification (`visual` vs
   `interactive-mock`), target folder
   `ai-docs/assets/mockups/YYMMDD/<topic>/` (entry `index.html`), and key
   design options or open questions on the whiteboard (or in chat when the
   board is not active). Confirm open policy choices before spawning; if
   existing code or tokens matter and you have not read them, spawn
   {{.ExploreAgent}} or pass the paths to `mockup-writer`.
2. **Render and spawn `mockup-writer`.**
   - Call `{{.McpNamespace}}/playbook.render(name: "mockup-writer",
     session_key: <your key>)`, passing `tier_override` (`small`–`xlarge`)
     when complexity warrants stepping up or down from its default `medium`
     tier. Do not read the rendered file.
   - Spawn one worker at the returned tier ({{.SpawnIdiom}}) with a task block
     naming:
     - `Read <rendered-path> as your system prompt.`
     - `Mode: visual | interactive-mock`
     - `Target: ai-docs/assets/mockups/YYMMDD/<topic>/index.html`
     - Settled requirements, options to compare, and any project visual style
       guide or source/API paths declared in `AGENTS.md` or the codebase.
   - Wait for the host's completion signal; do not poll. Never pass mockup
     HTML bodies between agents.
3. **Mount and review on the whiteboard.**
   - When `mockup-writer` returns its entry path and design decisions, mount
     the mockup in the topic with
     `<wb:panel name="..." recap="..." src="ai-docs/assets/mockups/YYMMDD/<topic>/index.html"/>`.
   - Surface each open design decision as its own `wb:add` item, never
     bundled into a panel or shared ask.
4. **Iterate.**
   - On user feedback, send follow-up instructions to the same `mockup-writer`
     worker (or spawn a fresh one if its handle is lost, citing the existing
     folder and feedback).
   - After every edit, re-post `<wb:panel src="..."/>` — `wb:panel src`
     snapshots the folder at post time and `wb:change` is refused on mockup
     panels.
5. **Track and hand off.**
   - Text mockups under `ai-docs/assets/mockups/YYMMDD/<topic>/` are tracked
     and committed following the project's commit rules once settled; keep
     raster captures gitignored unless the project directs otherwise.
   - When the user moves from mockup approval to implementation or ticket
     capture, hand off to `{{.SkillNamespace}}:lead-ticket` or
     `{{.SkillNamespace}}:lead-delegate`.

## Stops

- An unanswered policy question on scope, mode, or a presented design option.
- An `ask-user` or `blocked` report from `mockup-writer` — relay `tell_user`
  and `ask` to the user without adding or dropping substance.

## Output

Mounted whiteboard mockup panel(s), decision items, and tracked mockup files
under `ai-docs/assets/mockups/YYMMDD/<topic>/`.
