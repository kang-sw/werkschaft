---
title: "화이트보드 다파일 HTML 목업 자동 정적 서빙 및 lead-mockup·mockup-writer 플레이북 추가"
---

# Automatic Static Serving for Multi-File Whiteboard HTML Mockups and `lead-mockup` / `mockup-writer` Playbooks

## Background

Whiteboard panels currently support `wb:panel src="..."` only for single-file HTML/SVG/Markdown fragments: `applyPanel` rejects full HTML documents (`<!DOCTYPE` / `<html`) via `fullDocument(body)`, and the browser mounts panel HTML inside an opaque-origin `srcdoc` iframe (`dashboard-frames.js`). As a result, leads cannot mount multi-file static mockups (`index.html` + `styles.css` + `app.js` + `mock-api.js` + `data.json` + fonts/images) on the whiteboard without manually bundling or falling back to external file links.

In addition, there is no dedicated shipped playbook pair for mockup creation: leads have to improvise prompt structure, folder conventions, visual vs. interactive-mock distinctions, and `explore` delegation when asking a worker to produce mockups.

This ticket adds:
1. Automatic static iframe serving for full/multi-file HTML mockups via the unchanged `wb:panel src="..."` lead syntax, backed by a directory-scoped `/fs/mock/` capability route and zip archive bundling.
2. Shipped `lead-mockup` and `mockup-writer` playbooks (`tier-role: mockup-writer`, `tier-parent: implement:*`), skill shims, `wsflow` mirrors, and updated `whiteboard` playbook documentation.

## Decisions

### 1. Zero-cognitive-load `wb:panel src` backed by automatic directory-scoped static iframe serving (`(1658)=a`)
- The lead call syntax remains unchanged: `<wb:panel name="..." recap="..." src="ai-docs/assets/mockups/YYMMDD/<topic>/index.html"/>`.
- When `wb:panel` receives `src` (`sourced == true`) and `fullDocument(body)` is true, the server automatically classifies the panel as a full/multi-file HTML mockup instead of rejecting `<!DOCTYPE`/`<html`. Inline `<wb:panel>` bodies (without `src`) continue to reject full HTML documents.
- The browser renders the panel inside a sandboxed iframe served via a dedicated directory-scoped capability route (`/s/<rid>/fs/mock/<cap>/...`) so ES module `import`, `mock-api.js`, `fetch('./data.json')`, local CSS, fonts, and images all work without a bundler.
- **Rejected:**
  - `(1658)=b`: Keeping panels fragment-only and opening multi-file mockups in a separate file-preview/tab link (fragments the whiteboard review experience).
  - `(1658)=c`: Server-side auto-inlining of `<link rel="stylesheet">` / classic `<script src>` into SQLite snapshots (breaks ES module `import` and relative `fetch('./data.json')` needed by `interactive-mock` mockups).

### 2. Minimal mockup folder and entry convention (`(1659)=a`)
- Standardize only the tracked folder location (`ai-docs/assets/mockups/YYMMDD/<topic>/`) and entry file (`index.html`; when presenting multiple design options, `index.html` provides option tabs/switcher or links to sibling option files such as `option-a.html`).
- Whether to author a single self-contained `index.html` or split CSS/JS/`mock-api.js`/JSON files is left to the worker based on task complexity.
- **Rejected:**
  - `(1659)=b`: Mandating a rigid A/B/C comparison shell + dark/light toggle scaffold for every mockup.
  - `(1659)=c`: No folder or entry-file convention at all.

### 3. Two-mode `lead-mockup` (`visual` vs `interactive-mock`) and `ticket-worker`-grade `mockup-writer` (`(1660)=a`)
- `lead-mockup` classifies the request into two explicit modes before delegating:
  - `visual`: static/visual layout and styling exploration.
  - `interactive-mock`: minimal portable frontend slice with a cleanly separated `mock-api.js` adapter layer modeling real API contracts so the frontend can be ported into production with minimal churn.
- `mockup-writer` is a full `ticket-worker`-grade rendered worker playbook (`kind: render`, `delegates: true`, `role: worker`, `tier-role: mockup-writer`, `tier-parent: implement:*`, supporting `tier_override` from `small` to `xlarge`, `includes: [worker-stop-protocol]`). It can spawn `explore` subagents to inspect existing design tokens, UI patterns, and real API schemas directly, and returns the entry path plus a concise list of design decisions for the lead.
- **Rejected:**
  - `(1660)=b`: Single undifferentiated prompt without an explicit `visual` vs `interactive-mock` mode split.
  - `(1660)=c`: Lightweight writer restricted from spawning `explore`.

### 4. Compatibility Tiers (settled — patch-release eligible, no schema version bumps)
- **`wswhiteboard` SQLite store (`boardDBSchemaVersion = 1`), folder store (`FolderSchemaVersion = 2`), and zip export/load — T1 (T0 for existing boards):**
  - Keep `boardDBSchemaVersion = 1` (`agents-plugin-tool/internal/wswhiteboard/board_db.go:23`), `FolderSchemaVersion = 2` (`agents-plugin-tool/internal/wswhiteboard/folders.go:28`), and `Entry.Kind = "panel"` (`folders.go:657-668`), avoiding old-binary refusal on schema bump (`board_db_import.go:131-133`, `folders.go:336-395`).
  - Add an optional `mockup` JSON metadata field on `Entry` (omitted when empty) AND store a readable Markdown fallback in `panels.body` (`Entry.Text`, e.g., recap + clickable relative path link). Old binaries preserve unknown JSON fields losslessly in `Entry.unknown` (`entry_json.go:10-44`) across reads and zip re-exports (`board_db_archive.go:280-294`), render `panels.body` cleanly via `renderPanel` (`panel_diff.go:192-194`, `HasBody()` at `stream_view.go:60`), and reopening on the new binary recovers the full mockup metadata losslessly.
  - Zip export bundles sibling mockup files under a dedicated non-topic top-level directory `_mockups/<entry-or-panel-id>/<rel-subpath>`. Old `extractArchive` (`folders_archive.go:165-196`) extracts regular non-dot files under relative paths and old `FolderStore.Open`/`OpenBoardDB` (`folders.go:327-398, 609-650`) ignores non-`tN` directories safely (T1).
- **`wsdashboard` `/fs/mock/` static iframe serving & lazy mount — T0:**
  - Same-binary dashboard HTTP route and frontend assets (`fs_capability.go`, `dashboard-frames.js`). Existing `/fs/cap/` and `/fs/view/` routes keep strict `connect-src 'none'` unchanged.
- **Shipped playbooks (`lead-mockup`, `mockup-writer`), `tier-role: mockup-writer`, skills, and `wsflow` mirrors — T0:**
  - Default configs and older binaries do not invoke `mockup-writer`, and `renderTierBindingCompat` (`mcp/tier_routing.go:85-100`) resolves defaults to standard `<size>` / `<harness>.<size>`. `config-manifest.json`, `dashboard-config-manifest.json`, `wsi18n`, and `dashboard-settings-tiers.js` discover roles dynamically from `rsrc/` (`mcp/tier_routing.go:16-37`, `wsrsrc/tier_roles.go:25-60`).
  - Documented downgrade caveat: if a user manually wires another built-in node in global `agent.tier_routing` to target `mockup-writer`, an older binary's `TierGraph.validate()` (`wsconfig/tier_routing.go:367-370`) rejects unknown target node names; overriding `mockup-writer` itself is safe (`TierNodeUser`).

## Constraints

- **Required Implementation Conventions manuals (read before editing):**
  - `ai-docs/manuals/shipped-surface-boundary.md`
  - `ai-docs/manuals/skill-authoring.md`
  - `ai-docs/manuals/wsflow-mirroring.md`
  - `ai-docs/manuals/release-compatibility.md`
  - `ai-docs/manuals/performance.md`
  - `ai-docs/manuals/dashboard-style.md`
- **Security boundary for `/fs/mock/`:**
  - `fsCapMAC` for `/fs/mock/` MUST sign `scopeDir` in addition to `(part, replyID, cleanRoot, expiry)`, and `handleFSMock` MUST enforce `openContained(filepath.Join(s.Project, scopeDir), subRel)` plus `dotDenied` / `capDenied` against `s.Project`. An `interactive-mock` token MUST NOT read files outside its own mockup folder.
  - Relax `connect-src 'none'` to `connect-src <prefix>` and allow `Sec-Fetch-Dest: empty` / `json` (with `Access-Control-Allow-Origin: null` + `Vary: Origin` for `Origin == "null"` or `sameOrigin`) ONLY on `/fs/mock/`, never on `/fs/cap/` or `/fs/view/`.
  - Iframe `sandbox` MUST NEVER include `allow-same-origin`.
- **Capability TTL & storage hygiene:**
  - Never persist expiring capability URLs (15m TTL) in SQLite or rendered board HTML. Emit lazy panel placeholders (`data-mockup-path` / `data-panel-entry`) and mint fresh `/fs/mock/` URLs on mount/open/remount in `dashboard-frames.js` (mirroring `preview.js` lazy minting and preserving `unmountFrames` memory hygiene).
- **Archive & fallback boundaries:**
  - Mockup zip collection must enforce non-dot, containment inside `scopeDir`, and bounded entry count/bytes well inside the `128 MiB` archive cap.
  - When serving a mockup panel from a loaded board (`whiteboard.load`) whose workspace lacks the original files on disk, serve from the extracted/stored `_mockups/` bundle cleanly (with the same CSP/sandbox protections).
- **Shipped-surface boundary:**
  - `lead-mockup`, `mockup-writer`, and `whiteboard` playbook text MUST remain strictly downstream-first and host-neutral (`{{.McpNamespace}}`, `{{.SkillNamespace}}`), with no references to `devenv`-only manuals or repo posture.
- **Verification environment:**
  - On Windows hosts with AV (V3), run all Go tests, `go vet`, `gofmt`, and `wsrsrc` regen commands on remote `w5090` (never local Windows `go test`).

## Phases

### Phase 1: Whiteboard multi-file HTML mockup panel (`wb:panel src`), directory-scoped `/fs/mock/` serving, and zip bundling

Implement the runtime foundation so `<wb:panel src=".../index.html">` seamlessly mounts single-file and multi-file full HTML mockups on the whiteboard with T1/T0 compatibility and strict directory isolation.

1. **`wswhiteboard` auto-classification & T1 persistence:**
   - Update `applyPanel` (`wswhiteboard/entries.go`) so `sourced && fullDocument(body)` classifies the panel as a full/multi-file HTML mockup (deriving `scopeDir` and entry subpath from `src`, keeping UTF-8/text validation on the entry HTML and keeping inline `fullDocument` rejection unchanged).
   - Add optional `mockup` JSON metadata on `Entry` without bumping `boardDBSchemaVersion` (1) or `FolderSchemaVersion` (2), and populate `panels.body` with a clean Markdown fallback link for old-binary T1 readability.
   - Update `ExportArchive` / `LoadArchive` / `BoardDB` (`board_db_archive.go`, `folders_archive.go`) to bundle sibling mockup assets under `_mockups/<id>/<rel>` within strict containment and size caps, and preserve fallback lookup when serving loaded boards without local workspace files.
2. **`wsdashboard` `/fs/mock/` route & lazy iframe mounting:**
   - Add directory-scoped `/s/<rid>/fs/mock/<cap>/...` in `wsdashboard/fs_capability.go` (and session-authenticated mint route such as `/s/<rid>/board/mockup?entry=...`) with `scopeDir`-bound HMAC, `openContained`, `connect-src <prefix>`, and `Origin: null` `Sec-Fetch-Dest: empty/json` CORS/CSP rules; keep `/fs/cap/` and `/fs/view/` unchanged.
   - Render mockup panel placeholders in `wswhiteboard/render.go` and mount/refresh them lazily in `dashboard-frames.js` (respecting `<details>` open, `frameNear`, `unmountFrames`, TTL refresh, and clean iframe height/sizing without requiring `doc-frame.js` inside author mockups).
3. **Verification:**
   - Go unit & `-race` tests on `w5090` covering: `wb:panel src` full-HTML auto-detection vs inline rejection, T1 unknown-field round-trip and old-binary `panels.body` fallback, `_mockups/` zip export + load + archive-fallback serving, `/fs/mock/` directory traversal / cross-dir / `/api/*` / evil-`Origin` refusal, `fetch('./data.json')` & ES module headers, and `GOOS=windows go vet/build ./...`.
   - Local Node tests verifying `dashboard-frames.js` lazy mount, TTL refresh, unmount cleanup, and `dashboard.js` bundle syntax.

### Phase 2: `lead-mockup` & `mockup-writer` playbooks, `tier-role` inventory, skills, `wsflow` mirrors, and `whiteboard` doc updates

Depends on Phase 1 landing so the shipped playbooks and `whiteboard` documentation match live multi-file `wb:panel src` behavior.

1. **Playbooks & docs (`agents-plugin/rsrc/`):**
   - Author `agents-plugin/rsrc/lead-mockup/lead-mockup.md` (guides lead to classify `visual` vs `interactive-mock`, agree scope/options, render & spawn `mockup-writer` with `tier_override`, mount `ai-docs/assets/mockups/YYMMDD/<topic>/index.html` via `wb:panel src`, and present design decisions on the whiteboard).
   - Author `agents-plugin/rsrc/mockup-writer/mockup-writer.md` (`kind: render`, `delegates: true`, `role: worker`, `tier: medium`, `tier-role: mockup-writer`, `tier-parent: implement:*`, `includes: [worker-stop-protocol]`, supporting `visual` and `interactive-mock` modes, `explore` subagent research, minimal `YYMMDD/<topic>/index.html` convention, and returning entry path + design decisions).
   - Update `agents-plugin/rsrc/whiteboard/whiteboard.md` to document multi-file full-HTML `wb:panel src` support and `visual` vs `interactive-mock` workflow pointers.
   - Apply `ai-docs/manuals/skill-authoring.md` invariant checklist and `ai-docs/manuals/shipped-surface-boundary.md` checks to every added/edited line.
2. **Skills, `wsflow` mirrors, and manifests:**
   - Create `agents-plugin/skills/lead-mockup/SKILL.md` and `agents-plugin-wsflow/skills/lead-mockup/SKILL.md` (`<ws-preload playbook="lead-mockup">`).
   - Run the full regen chain on `w5090`: `TestRegenerateShippedManifest`, `TestRegenerateWsflowRsrcMirror`, and `TestGenerateRealSkillsManifest`.
   - Update `shippedTierRoleInventory` in `agents-plugin-tool/internal/wsrsrc/tier_roles_test.go` (`"mockup-writer": {"implement:*", "medium"}`) and `EXPECTED_SKILLS` in `agents-plugin-wsflow/tests/test_wsflow_skill_bundle.py`.
3. **Verification:**
   - `w5090`: `GOOS=windows go vet ./...`, `go test -race ./internal/wsrsrc/... ./internal/mcp/... ./internal/wspackage/... ./internal/wsdoc/...`, `package-assets --check`.
   - Local Python tests: `pytest agents-plugin-wsflow/tests/`.
