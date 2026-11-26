---
title: "화이트보드 다파일 HTML 목업 자동 정적 서빙 및 lead-mockup·mockup-writer 플레이북 추가"
related:
  261119-feat-ws-whiteboard-frame-css-isolation: "soft overlap on whiteboard iframe styling; its frameCSSFile slimming applies to srcdoc panels, while /fs/mock/ mockup documents are served standalone and receive no injected frame stylesheet"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 1acd0c5d8f567f0c
sage-review-completeness-reviewed: 1acd0c5d8f567f0c
completed: 2026-11-26
---

# Automatic Static Serving for Multi-File Whiteboard HTML Mockups and `lead-mockup` / `mockup-writer` Playbooks

## Background

Whiteboard panels currently support `wb:panel src="..."` only for single-file HTML/SVG/Markdown fragments: `applyPanel` rejects full HTML documents (`<!DOCTYPE` / `<html`) via `fullDocument(body)`, and the browser mounts panel HTML inside an opaque-origin `srcdoc` iframe (`dashboard-frames.js`). As a result, leads cannot mount multi-file static mockups (`index.html` + `styles.css` + `app.js` + `mock-api.js` + `data.json` + fonts/images) on the whiteboard without manually bundling or falling back to external file links.

In addition, there is no dedicated shipped playbook pair for mockup creation: leads have to improvise prompt structure, folder conventions, visual vs. interactive-mock distinctions, and `explore` delegation when asking a worker to produce mockups.

This ticket adds:
1. Automatic static iframe serving for full/multi-file HTML mockups via the unchanged `wb:panel src="..."` lead syntax. At post time the mockup folder is snapshotted into a single zip BLOB inside `.board.sqlite`; a capability route (`/fs/mock/`) serves it from an in-memory LRU, and the bundle travels with zip export/load.
2. Shipped `lead-mockup` and `mockup-writer` playbooks (`tier-role: mockup-writer`, `tier-parent: implement:*`), skill shims, `wsflow` mirrors, and updated `whiteboard` playbook documentation.

## Decisions

### 1. Zero-cognitive-load `wb:panel src` backed by automatic directory-scoped static iframe serving (`(1658)=a`)
- The lead call syntax remains unchanged: `<wb:panel name="..." recap="..." src="ai-docs/assets/mockups/YYMMDD/<topic>/index.html"/>`.
- When `wb:panel` receives `src` (`sourced == true`) and `fullDocument(body)` is true, the server automatically classifies the panel as a full/multi-file HTML mockup instead of rejecting `<!DOCTYPE`/`<html`. Inline `<wb:panel>` bodies (without `src`) continue to reject full HTML documents.
- The browser renders the panel inside a sandboxed iframe served via a dedicated capability route (`/s/<rid>/fs/mock/<cap>/...`), so ES module `import`, `mock-api.js`, `fetch('./data.json')`, local CSS, fonts, and images all work without a bundler. The route serves the post-time snapshot (Decision 5), never live workspace files.
- **Supersedes (`(1674)=a`, owner-confirmed 2026-11-26):** owner decision `(1394)=a` (merge 59e8fb0f, "snapshot include over live iframe embed ... no new serving surface") and the c44f5f78 refusal of a `src` file that starts with `<!doctype`/`<html`. The supersession covers full-document `src` files only; fragment `src` snapshots keep their current behavior unchanged.
- Desktop-app same-origin isolation stays mandatory (owner note on `(1674)`): the iframe uses `sandbox` with `allow-scripts` and never `allow-same-origin`, so its origin is opaque (`Origin: null`).
- **Rejected:**
  - `(1674)=b`: Keeping `(1394)=a` and dropping the serving surface (collapses back to the rejected `(1658)=b`/`(1658)=c`).
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
  - Add an optional `mockup` JSON metadata field on `Entry` (omitted when empty; carries at least the root file and the bundle `sha256`) AND store a readable Markdown fallback in `panels.body` (`Entry.Text`, e.g., recap + the `src` path). Old binaries preserve unknown JSON fields losslessly in `Entry.unknown` (`entry_json.go:10-44`) across reads and zip re-exports (`board_db_archive.go:280-294`), render `panels.body` cleanly via `renderPanel` (`panel_diff.go:192-194`, `HasBody()` at `stream_view.go:60`), and reopening on the new binary recovers the full mockup metadata losslessly.
  - Add `CREATE TABLE IF NOT EXISTS mockup_bundles (entry_id TEXT PRIMARY KEY REFERENCES entries(entry_id) ON DELETE CASCADE, root_file TEXT NOT NULL, sha256 TEXT NOT NULL, bundle BLOB NOT NULL, byte_size INTEGER NOT NULL, file_count INTEGER NOT NULL, created_at TEXT NOT NULL)` to `boardDBSchema` (`board_db.go:104-141`). An old binary never creates or reads it.
  - **`ON DELETE CASCADE` is required for T1.** An old binary's `LoadArchive` runs `DELETE FROM entries` (`board_db_archive.go:110-114`) with `PRAGMA foreign_keys=ON` (`board_db.go:424`) and does not know `mockup_bundles`. The cascade lets that delete succeed without FK errors and leaves no stale bundle for a reused `entry_id`. The `sha256` on `Entry` JSON double-guards the binding, and the new binary also clears `mockup_bundles` explicitly in its own `LoadArchive`. Elsewhere `wswhiteboard` only inserts into `entries` (no `REPLACE`/upsert), so no other old-binary path orphans a bundle.
  - Zip export writes one member per bundle, `_mockups/<entryID>.zip`, directly from `mockup_bundles`. An old binary's `extractArchive` (`folders_archive.go:165-196`) extracts it as a regular non-dot file, and its `FolderStore.Open`/`OpenBoardDB` (`folders.go:327-398, 609-650`) ignores non-`tN` paths (T1).
  - T1 loss on old binaries: an old binary renders the Markdown fallback instead of the mockup, and its own `ExportArchive` omits `mockup_bundles`. A board that round-trips through an old-binary export therefore keeps the panel and its metadata but loses the snapshot. The new binary then renders the Markdown fallback with a "mockup snapshot unavailable" note instead of an iframe.
- **`wsdashboard` `/fs/mock/` static iframe serving & lazy mount — T0:**
  - Same-binary dashboard HTTP route and frontend assets (`fs_capability.go`, `dashboard-frames.js`). Existing `/fs/cap/` and `/fs/view/` routes keep strict `connect-src 'none'` unchanged.
- **Shipped playbooks (`lead-mockup`, `mockup-writer`), `tier-role: mockup-writer`, skills, and `wsflow` mirrors — T0:**
  - Default configs and older binaries do not invoke `mockup-writer`, and `renderTierBindingCompat` (`mcp/tier_routing.go:85-100`) resolves defaults to standard `<size>` / `<harness>.<size>`. `config-manifest.json`, `dashboard-config-manifest.json`, `wsi18n`, and `dashboard-settings-tiers.js` discover roles dynamically from `rsrc/` (`mcp/tier_routing.go:16-37`, `wsrsrc/tier_roles.go:25-60`).
  - Documented downgrade caveat: if a user manually wires another built-in node in global `agent.tier_routing` to target `mockup-writer`, an older binary's `TierGraph.validate()` (`wsconfig/tier_routing.go:367-370`) rejects unknown target node names; overriding `mockup-writer` itself is safe (`TierNodeUser`).

### 5. Copy-on-post snapshot stored as one zip BLOB in `.board.sqlite`, served from an in-memory LRU (`(1675)=b`, owner-confirmed 2026-11-26)
- **Write path.** At `wb:panel src` time, when `sourced && fullDocument(body)`:
  - Read the `src` file's directory (`scopeDir`) once, recursively, under the existing `ReadPanelSource` path rules: containment in the session root, the Lstat component walk refusing any symlink/junction, `O_NOFOLLOW`/identity pin, regular files only, and non-dot names.
  - Enforce the file-count cap (256) and the bundle byte cap (8 MiB uncompressed; owner range 4-8 MiB; Decision 6).
  - Pack the files into an in-memory zip and insert the row into `mockup_bundles` inside `updateLocked`'s existing transaction on the held `.board.sqlite` handle.
  - No file is created on disk. Every post or re-post is a new entry with its own immutable bundle, so a later edit to the folder needs a new post. This matches the existing `src` contract ("the stored panel holds content, not the path").
- **Read path.** `/s/<rid>/fs/mock/<cap>/<rel>` serves members from a Go in-memory LRU of decoded bundles:
  - Cache key: `(boardDir, board.Instance(), entryID, sha256)`, so `LoadArchive` or a reopen never serves a stale entry. The `sha256` component and `LoadArchive` eviction are added by Decision 8, because `Instance()` does not change on an in-place `LoadArchive`.
  - Total-bytes bound: 64 MiB of decoded bytes (owner range 32-64 MiB; Decision 6).
  - A cache hit costs zero file syscalls and zero SQLite queries; a cold miss costs one `SELECT` on the held handle.
  - Workspace files are never read at serve time, and a loaded board (`whiteboard.load`) serves through exactly the same path.
- **Export / load.**
  - `ExportArchive` (and the pre-load backup, Decision 9) writes `_mockups/<entryID>.zip` from `mockup_bundles` in memory.
  - The new binary's `readBoardArchive` does not extract `_mockups/` to the temp dir. It reads each member's bytes directly from the archive reader. Both load paths, `LoadArchive` and `LoadNewBoardDB` (Decision 9), insert them into `mockup_bundles` inside the same DB transaction that writes `entries`.
  - A bundle whose `entryID` has no loaded entry, or whose `sha256` does not match the entry's metadata, is skipped.
- **Rejected:**
  - `(1675)=a`: Serving live workspace files (earlier panel versions change or break when the folder is edited or deleted, and loaded boards need a separate fallback path).
  - `(1675)=c`: Live files plus a post-time content hash and a "changed since post" indicator.
  - Copying the snapshot as loose files under the board directory (owner: no scattered loose files; `ai-docs/manuals/performance.md` Type 4 whiteboard SQLite store).

### 6. Concrete cap constants (`(1683)`, technical)
- Bundle byte cap: **8 MiB**, measured as the total uncompressed member bytes, so it also bounds the decoded size the LRU holds. File-count cap: **256** members. LRU total-bytes bound: **64 MiB**, counting decoded (uncompressed) bytes.
- 8 MiB over 4 MiB: both are inside the owner's range. Visual mockups that carry images or fonts fit, the 64 MiB LRU still holds at least eight worst-case bundles, and the 128 MiB archive cap still fails loudly rather than truncating (Constraints).
- 256 files over a tighter count: a hand-written or `mockup-writer` mockup is tens of files. The count cap only refuses accidental `node_modules`-style trees, and the byte cap is the binding limit.
- The tests use these same constants.
- **Rejected:** 4 MiB bundle cap / 32 MiB LRU (tighter memory, but realistic image-bearing mockups would be refused).

### 7. Settled names and mint route (`(1684)`, technical)
- Go field `Entry.Mockup` (type `*MockupMeta`: `Root string`, `SHA256 string`), JSON key `mockup` with `omitempty`.
- Mint route: `GET /s/<rid>/board/mockup?entry=<entryID>`, session-authenticated. It looks up the entry's bundle and answers `302` with `Cache-Control: no-store` to `/s/<rid>/fs/mock/<cap>/<root>`. This mirrors the existing `/s/<rid>/file?path=` → `/fs/view/<cap>/<path>` redirect (`wsdashboard/file_route_test.go:254-256`). Relative member URLs inside the mockup then resolve against the minted `/fs/mock/<cap>/` prefix.
- An unknown entry, a non-mockup entry, or a missing bundle answers `404`.
- Redirect over a JSON mint response: an iframe `src` can point at the mint route directly, so no capability URL is ever written into board HTML, and a remount re-mints for free.
- **Rejected:** a JSON `{url}` mint endpoint (needs an extra fetch step in `dashboard-frames.js` with no gain over the redirect).

### 8. LRU staleness guard (`(1685)`, technical)
- `BoardDB.instance` is immutable after open (`board_db.go:49-50`, assigned only at `:412`). `LoadArchive` swaps contents in place on the same `BoardDB` (`board_db_archive.go:51-142`), and loaded entries keep their archived `e<seq>` IDs. So `(boardDir, Instance(), entryID)` alone can name a different panel after a load.
- The cache key adds the bundle `sha256`. The capability carries it, a cached bundle is served only when its `sha256` matches, and a miss re-reads the row and answers `404` when the row's `sha256` differs from the token.
- `LoadArchive` also evicts that board's cache entries, to free memory; correctness does not depend on the eviction.
- This keeps the owner's (1675)=b guarantee ("LoadArchive or a reopen never serves a stale entry"). It corrects the premise that `Instance()` changes on load; the storage and serving design is unchanged.
- **Rejected:** eviction alone (it races a concurrent cold miss and leaves correctness on a hook that is easy to miss).

### 9. Both archive load paths and the pre-load backup carry bundles (`(1686)`, technical)
- `readBoardArchive` (`board_db_archive.go:145`) returns the decoded `State` plus the `_mockups/` bundle bytes keyed by `entryID`. It reads `_mockups/` members from the zip reader and never extracts them.
- `LoadArchive` (`:51`) and `LoadNewBoardDB` (`:178`; used by `whiteboard.load` when the session has no board, `mcp/whiteboard_tools.go:408`) both insert the bundles in the transaction that writes `entries`, under the per-bundle cap and the orphan / `sha256`-mismatch skip.
- `backupLegacyLocked` (`:194`) also writes `_mockups/<entryID>.zip` into the `<dir>.bak-*` folder, mirroring export, so a restored backup keeps its snapshots. The backup folder is already a sibling legacy-layout copy outside the live store, so this adds no loose file to the live board directory.
- **Rejected:** backups without bundles (recovering from a mistaken load would silently lose every mockup).

### 10. Mockup frames survive topic morphs (`(1687)`, technical)
- `frameKeep` (`dashboard-board.js:887`) keeps an existing frame only when `data-doc-key` and `data-srcdoc` match, and skips frames without `data-srcdoc` (`:873-905`). A mockup placeholder would therefore remount on every update to its topic, re-minting the capability and losing `interactive-mock` state.
- The mockup placeholder carries `data-doc-key` and `data-mockup-sha` (the bundle `sha256`). `frameKeep` matches mockup frames on that pair.
- `dashboard-tracker.js` `paletteBodyText` (`:587-595`) reads the placeholder's Markdown fallback text, so command-palette search still covers mockup entries.
- **Rejected:** letting mockups remount (it breaks review of interactive mocks mid-session).

### 11. Bundle member and root restrictions (`(1688)`, technical)
- Only an `.html`/`.htm` `src` whose body is a full document becomes a mockup. A `.md`, `.svg`, or other `src` containing `<html` stays refused as today (c44f5f78).
- `scopeDir` must be a strict subdirectory of the session root. A `src` directly in the session root is refused, with a message to move the mockup into its own folder (the convention is `ai-docs/assets/mockups/YYMMDD/<topic>/`).
- Members use an extension allowlist: `.html .htm .css .js .mjs .json .svg .png .jpg .jpeg .gif .webp .avif .ico .woff .woff2 .ttf .otf .md .txt`. Other non-dot files are skipped and do not count toward the caps. A reference to a skipped file then 404s on `/fs/mock/`, which the `whiteboard`/`mockup-writer` docs state.
- The walk stops and refuses as soon as the file-count or byte cap is exceeded.
- `Content-Type` on `/fs/mock/` follows the same allowlist.
- **Rejected:** bundling every non-dot regular file (configs, keys, or data near the mockup would travel in every export).

### 12. `wb:change` on a mockup panel is refused (`(1689)`, technical)
- A patch rebuilds the body from `latest.Text` (`entries.go:238`), which for a mockup is the Markdown fallback, and the new `Entry` (`:289`) does not copy `mockup`. A patch would silently turn the mockup into a Markdown panel.
- `wb:change` on a mockup panel is refused, with a message to re-post `<wb:panel src>` instead.
- **Rejected:** patching the fallback text while keeping `mockup` (the fallback would drift from the snapshot it describes).

### 13. Bundle bytes travel beside the State, not in it (`(1690)`, technical)
- `applyPanel` mutates a cloned `*State` inside `updateLocked`'s `fn`, and `boardWriteDelta` derives the rows to write from the State diff (`board_db_write.go:49-150`).
- The collected bundle travels in a write-scoped side channel, a pending-bundles slice on the update call, that is excluded from the State diff and comparison. It is inserted right after the `entries` insert (`:138`) in the same transaction; rows are append-only.
- **Rejected:** a bundle field on `State`/`Entry` (it would enter the diff, the clone, and every comparison, and leak into JSON).

### 14. Iframe sizing (`(1691)`, technical)
- The mockup iframe uses a fixed default height (`70vh`) in a wrapper the user can resize vertically.
- No height script is injected into served documents, so served bytes stay exactly the snapshot bytes and the `/fs/mock/` CSP needs no inline-script exception.
- **Rejected:** a server-injected height-reporting script (it modifies the author's document and widens the CSP for an opaque-origin frame).

### 15. Capability refresh policy for kept mockup frames (`(1692)`, technical)
- A mockup frame re-mints its `/fs/mock/` capability only when it mounts or remounts: `<details>` reopen, scroll back into range after `unmountFrames`, a `sha256` change, or a user reopen. A TTL timer never reloads a visible frame that `frameKeep` kept, so Decision 10's state preservation holds.
- An `interactive-mock` left open past the 15-minute TTL may fail later lazy loads (`fetch`, dynamic `import()`, images). The `whiteboard` and `mockup-writer` docs say to reopen a stale mock, and `mockup-writer` prefers loading assets up front.
- **Rejected:** a longer-lived `/fs/mock/` capability (it diverges from the shared 15-minute `fs_capability` TTL contract for a convenience case); a periodic TTL reload (it destroys the state Decision 10 protects).

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/skill-authoring.md (declared for agents-plugin/rsrc/, agents-plugin/skills/, agents-plugin-wsflow/skills/)
- Convention: ai-docs/manuals/wsflow-mirroring.md (declared for agents-plugin/rsrc/, agents-plugin/skills/, agents-plugin-wsflow/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/config-settings-ui.md (declared for agents-plugin/config-manifest.json, agents-plugin/dashboard-config-manifest.json, agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*)
- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/, ai-docs/assets/mockups/)
- **Required Implementation Conventions manuals (read before editing):**
  - `ai-docs/manuals/shipped-surface-boundary.md`
  - `ai-docs/manuals/skill-authoring.md`
  - `ai-docs/manuals/wsflow-mirroring.md`
  - `ai-docs/manuals/release-compatibility.md`
  - `ai-docs/manuals/performance.md`
  - `ai-docs/manuals/dashboard-style.md`
- **Out of scope:** viewing arbitrary external `https://...` webpages in panels (owner note on `(1674)`).
- **Security boundary for `/fs/mock/`:**
  - The `/fs/mock/` capability MAC MUST bind the `entryID` and bundle `sha256` in addition to `(part, replyID, expiry)`. A token MUST resolve only members of its own bundle: no filesystem access at serve time, and no other entry's bundle.
  - Resolve `<rel>` by exact lookup in the bundle's normalized member map. Refuse empty, absolute, backslash, `..`, and dot-prefixed segments. Serve `Content-Type` by extension (`.js`/`.mjs` as `text/javascript` for ES modules, `.json` as `application/json`).
  - Post-time collection reuses the `ReadPanelSource` containment rules (Prior Decisions 661270de, a244e328, b8ebd7fd), so an `interactive-mock` bundle can never contain a file outside its own `scopeDir`.
  - Relax `connect-src 'none'` to `connect-src <prefix>` and allow `Sec-Fetch-Dest: empty` / `json` (with `Access-Control-Allow-Origin: null` + `Vary: Origin` for `Origin == "null"` or `sameOrigin`) ONLY on `/fs/mock/`, never on `/fs/cap/` or `/fs/view/`.
  - Iframe `sandbox` MUST NEVER include `allow-same-origin`.
- **Capability TTL & storage hygiene:**
  - Never persist expiring capability URLs (15m TTL) in SQLite or rendered board HTML. Emit lazy panel placeholders (`data-doc-key` + `data-mockup-sha`, Decision 10) and mint fresh `/fs/mock/` URLs through the Decision 7 route on mount/open/remount in `dashboard-frames.js` (refresh policy: Decision 15) (mirroring `preview.js` lazy minting and preserving `unmountFrames` memory hygiene).
- **Archive & storage boundaries:**
  - Post-time collection enforces non-dot names, the member extension allowlist, a `scopeDir` strictly below the session root (Decision 11), containment inside `scopeDir`, the 256-file cap, and the 8 MiB bundle cap (Decision 6).
  - Many bundles together must still fit inside the `128 MiB` archive cap (`folders_archive.go:30`). An export that would exceed it fails with the existing archive-cap error rather than silently dropping bundles.
  - Archive load reads `_mockups/<entryID>.zip` members under the same per-bundle cap and never extracts them to disk.
  - `performance.md` Type 4: zero new loose files on disk, zero file syscalls on cache hit, bounded LRU memory.
- **Shipped-surface boundary:**
  - `lead-mockup`, `mockup-writer`, and `whiteboard` playbook text MUST remain strictly downstream-first and host-neutral (`{{.McpNamespace}}`, `{{.SkillNamespace}}`), with no references to `devenv`-only manuals or repo posture.
- **Verification environment:**
  - On Windows hosts with AV (V3), run all Go tests, `go vet`, `gofmt`, and `wsrsrc` regen commands on remote `w5090` (never local Windows `go test`).

## Prior Decisions

- c44f5f78 (2026-11-19, commit): "Full-document files: REFUSED, not extracted. A src file starting with <!doctype or <html is refused ... telling the author to save a fragment" — bearing: contradiction-candidate; superseded for full-document `src` by `(1674)=a` (Decision 1)
- 59e8fb0f (2026-11-19, commit): "Owner (1394)=a: snapshot include over live iframe embed or guidance-only; smallest change, preserves board history, no new serving surface." — bearing: contradiction-candidate; superseded for full-document `src` by `(1674)=a` (Decision 1); its board-history value is kept by `(1675)=b` (Decision 5)
- 59e8fb0f (2026-11-19, commit): "Compatibility T0: stored payload unchanged; an older daemon refuses the unknown src attribute per command (wsxmlcmd checkAttrs) and writes nothing." — bearing: constrains
- 661270de (2026-11-20, commit): "close the TOCTOU symlink/junction gap by opening with O_NOFOLLOW on Unix (panel_src_unix.go), validating the opened *os.File handle ..." — bearing: constrains
- a244e328 (2026-11-19, commit): "Windows junction escape fixed by an Lstat component walk that refuses any symlink/junction; refusals name the session root." — bearing: constrains
- b8ebd7fd (2026-11-22, commit): "panel_src: a failed Windows identity pin now returns the existing changed-file error" — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | wswhiteboard/entries.go, panel_src.go, entry_json.go, render.go, board_db.go, board_db_write.go, board_db_archive.go; wsdashboard/fs_capability.go, assets/dashboard-frames.js, assets/dashboard-board.js, assets/dashboard-tracker.js; agents-plugin/rsrc/, skills, wsflow mirrors |
| scope.surface | cross-module | wswhiteboard, wsdashboard, wsrsrc, shipped playbooks; new Entry mockup JSON field, mockup_bundles table, and /fs/mock/ route |
| scope.new_public_symbol | yes | exported Entry.Mockup field (*MockupMeta, JSON `mockup`; Decision 7) and new playbooks lead-mockup, mockup-writer |
| scope.new_type_contract | yes | optional mockup metadata on Entry (entry_json.go), mockup_bundles table (board_db.go boardDBSchema), entryID+sha256-bound capability MAC (fs_capability.go#L192) |
| scope.test_surface | existing | wswhiteboard/entries_test.go, board_db_archive_test.go, folders_archive_test.go; wsdashboard/fs_capability_test.go, board_frame_reuse_test.go; wsrsrc/tier_roles_test.go |
| complexity.reuse_points | confirmed | ReadPanelSource containment rules (panel_src.go#L29), updateLocked transaction (board_db_write.go#L49), BoardDB.Instance (board_db_write.go#L27), preview.js lazy minting |
| complexity.side_effect_risk | high | new CSP/CORS relaxation and archive format addition beside strict /fs/cap/ and /fs/view/ routes |
| risk.correctness | high | post-time bundling, cascade/stale-binding guards, LRU keying, lazy TTL remount, and archive bundle import are all new and interact |
| risk.fit | moderate | supersedes c44f5f78 / (1394)=a for full-document src (owner-confirmed (1674)=a); snapshot semantics kept by (1675)=b |
| risk.test | moderate | traversal and CSP tests need w5090 and browser-level lazy-mount checks; cannot run locally on Windows |
| risk.security_or_contract | high | new capability route with connect-src relaxation for interactive mocks; T1 compat contract on stored boards |

## Phases

### Phase 1: Whiteboard multi-file HTML mockup panel (`wb:panel src`), SQLite bundle snapshot, `/fs/mock/` serving, and archive round-trip

Implement the runtime foundation so `<wb:panel src=".../index.html">` mounts single-file and multi-file full HTML mockups on the whiteboard from an immutable post-time snapshot, with T1/T0 compatibility and strict isolation.

1. **`wswhiteboard` classification, bundling & T1 persistence:**
   - Update `applyPanel` (`wswhiteboard/entries.go`) so `sourced && fullDocument(body)` classifies the panel as a mockup instead of refusing it:
     - only for an `.html`/`.htm` `src` whose `scopeDir` is a strict subdirectory of the session root (Decision 11);
     - derive `scopeDir` and the root file from `src`;
     - keep UTF-8/text validation on the entry HTML;
     - keep the inline `fullDocument` rejection and fragment `src` behavior unchanged.
   - Collect `scopeDir` under the `ReadPanelSource` rules, the extension allowlist, and the caps, stopping early on a cap (Decision 11). Then pack an in-memory zip.
   - Carry the zip through the write-scoped pending-bundles side channel, outside the State diff, and insert it into the new `mockup_bundles` table (FK `ON DELETE CASCADE`) right after the `entries` insert in `updateLocked`'s transaction (Decision 13).
   - Refuse `wb:change` on a mockup panel with a re-post message (Decision 12).
   - Store the optional `mockup` metadata (root file, `sha256`) on `Entry` and the Markdown fallback in `panels.body`. Do not bump `boardDBSchemaVersion` (1) or `FolderSchemaVersion` (2).
   - `ExportArchive` and `backupLegacyLocked` emit `_mockups/<entryID>.zip` per bundle.
   - `readBoardArchive` returns the bundles beside the `State` without extracting them. The new binary's `LoadArchive` clears `mockup_bundles`, then both `LoadArchive` and `LoadNewBoardDB` insert the bundles in the transaction that writes `entries`, under the per-bundle cap and the `sha256`/entry match (Decision 9). `LoadArchive` evicts the board's LRU entries (Decision 8).
2. **`wsdashboard` `/fs/mock/` route, LRU & lazy iframe mounting:**
   - Add `/s/<rid>/fs/mock/<cap>/<rel>` in `wsdashboard/fs_capability.go`, with the session-authenticated `302` mint route `GET /s/<rid>/board/mockup?entry=<entryID>` (Decision 7):
     - `entryID`+`sha256`-bound capability MAC;
     - members served from the bounded in-memory LRU keyed by `(boardDir, Instance(), entryID, sha256)` (Decision 8);
     - exact member lookup with segment refusals, and allowlist extension-based `Content-Type`;
     - `connect-src <prefix>` and `Origin: null` `Sec-Fetch-Dest: empty/json` CORS/CSP rules on this route only, with `/fs/cap/` and `/fs/view/` unchanged.
   - Render in `wswhiteboard/render.go` either of two forms:
     - a mockup panel placeholder carrying `data-doc-key`, `data-mockup-sha`, and the Markdown fallback text;
     - when the bundle is missing, the Markdown fallback plus a "snapshot unavailable" note.
   - Mount and refresh placeholders lazily in `dashboard-frames.js`:
     - respect `<details>` open, `frameNear`, and `unmountFrames`, and re-mint only on mount/remount, never reloading a visible kept frame on TTL (Decision 15);
     - set `sandbox="allow-scripts ..."` without `allow-same-origin`;
     - use a fixed `70vh` default height in a vertically resizable wrapper, with no injected script (Decision 14).
   - Extend `frameKeep` in `dashboard-board.js` to keep mockup frames matched on `data-doc-key` plus `data-mockup-sha`, and make `dashboard-tracker.js` `paletteBodyText` read the placeholder's fallback text (Decision 10).
3. **Verification:**
   - Go unit & `-race` tests on `w5090` covering:
     - `wb:panel src` full-HTML detection vs inline rejection, with fragment `src` unchanged;
     - bundle collection refusals (symlink/junction, dot file, over count cap with early stop, over byte cap, non-regular file, `src` directly in the session root, a non-`.html`/`.htm` `src` with `<html` still refused), with non-allowlisted files skipped and uncounted;
     - a re-post creates a new immutable bundle, and an edit after posting does not change served bytes;
     - `wb:change` on a mockup panel is refused with the re-post message;
     - an old-binary-style `DELETE FROM entries` with `foreign_keys=ON` cascades `mockup_bundles` cleanly;
     - T1 unknown-field round-trip and `panels.body` fallback;
     - `_mockups/<entryID>.zip` export → load round-trip through both `LoadArchive` and `LoadNewBoardDB`, with no temp-dir extraction, plus `sha256`-mismatch and orphan skip;
     - the `.bak-*` backup written by `LoadArchive` holds `_mockups/` and reloads with its snapshots;
     - missing-bundle fallback render;
     - an LRU hit issues no SQLite query (counted through a test hook), and eviction stays under the bound;
     - after an in-place `LoadArchive` that reuses an `entryID` for a different bundle, the old token gets `404`, a fresh mint serves the new bundle, and the cache holds no stale entry;
     - `/fs/mock/` refuses `..`/absolute/backslash/dot segments, a cross-entry token, a `sha256`-mismatched token, `/api/*`, and an evil `Origin`;
     - `fetch('./data.json')` and ES module headers/`Content-Type`;
     - `GOOS=windows go vet/build ./...`.
   - Local Node tests: `dashboard-frames.js` lazy mount, re-mint on remount after expiry with no reload of a visible kept mockup frame, unmount cleanup, and sandbox attribute without `allow-same-origin`; `frameKeep` keeping a mockup frame across a topic morph with the same `sha256`, and remounting it on a different one; `paletteBodyText` on a mockup placeholder; and `dashboard.js` bundle syntax.
   - Done when every item above passes and the existing `TestPanelSrc*` fragment tests are unchanged.

### Result (9c0c462) - 2026-11-26

- **Landed (`9c0c462`, review fix `180629f`):**
  - `wswhiteboard`: Added `mockup.go` (`CollectMockupBundle`, `DecodeMockupZip`, `MockupContentType`, `ValidMockupRel`, strict subdirectory + non-dot + `ReadPanelSource` symlink/junction/handle-pin guards, extension allowlist skipping non-allowlisted files uncounted, early-stop 256 files / 8 MiB uncompressed cap, deterministic sorted zip + sha256, `mockupMaxZipBytes = MockupMaxBundleBytes + 1 MiB` pre-decode container headroom). Updated `entries.go`, `entry_json.go`, `store.go` (full-HTML `.html`/`.htm` `src` classification, Markdown fallback in `panels.body`, `Entry.Mockup`, `wb:change` refusal on mockup panels, `Outcome.MockupBundles` side channel with per-command rollback). Updated `board_db*.go` and `folders_archive.go` (`mockup_bundles` table with `ON DELETE CASCADE`, schema versions unchanged at 1/2, `CreateBoardDBWithBundles`/`UpdateWithBundles`/`ReadMockupBundle`/`MockupIndex`, `OnLoadArchive` callback, `_mockups/<entryID>.zip` export, backup, and direct zip-reader load without temp extraction). Updated `view.go`, `stream_view.go`, `render.go`, `render_topics.go`, `page-stream.css` (`70vh` resizable `.mockup-wrap`, lazy `.doc-frame.mockup-frame` placeholder with `data-doc-key`/`data-mockup-sha`/`data-mockup-src`/`data-fallback-text`, missing-bundle fallback note).
  - `mcp`: Wired `boardMockups`, `pendingMockupBundles`, `WhiteboardStores.SetOnLoadArchive` (dynamic closure so evictors installed after board open still fire), and `DashboardSessionSource` `BoardMockupSource` (`LookupMockup`, `ReadMockupBundle`, `SetMockupEvictor`, `servedViewInput` `MockupSrc`).
  - `wsdashboard` & frontend: Added `mockup_route.go` (`GET /s/<rid>/board/mockup?entry=` 302 `Cache-Control: no-store` mint redirect, `/s/<rid>/fs/mock/<cap>/<rel>` with MAC binding `(part, replyID, entryID, sha256, expiry)`, 64 MiB LRU keyed by `(boardDir, instance, entryID, sha256)`, `connect-src <prefix>`, `Sec-Fetch-Dest: empty/json`, `Access-Control-Allow-Origin: null` + `Vary: Origin`, `/fs/cap/` & `/fs/view/` unchanged), wired `page.go`, `agent_sessions.go`, `fs_capability.go`, `fs_routes.go`, `page_routes.go`, `daemon.go`, `daemon_start.go`, and updated `dashboard-frames.js`, `dashboard-board.js`, and `dashboard-tracker.js` (lazy mount/unmount without `allow-same-origin`, `frameKeepKey` preservation across morphs, `paletteBodyText` fallback search).
- **Decisions & adaptations:**
  - Updated `TestPanelSrcRefusals` full-document refusal subtests from `.html` to `.md`/`.svg` (Decision 1 supersedes c44f5f78 for full-HTML `.html`/`.htm` `src` while preserving refusal on non-HTML `src`; all fragment `TestPanelSrc*` tests remain unchanged).
  - Moved the POSIX `syscall.Mkfifo` test into `mockup_unix_test.go` (`//go:build !windows`) for clean `GOOS=windows` cross-compilation.
  - Added compound selector entries to `board_frame_lazy_test.go`'s minimal DOM shim so existing lazy-frame tests match the updated selector in `dashboard-frames.js`.
  - Fixed `code-review-lite` Important finding (`180629f`): widened raw pre-decode zip size cap in `extractArchiveWithMockups` and `readLegacyMockupBundles` from `8 MiB + 64 KiB` to `8 MiB + 1 MiB` (`mockupMaxZipBytes`) and added `TestMockupMaxCapIncompressibleArchiveRoundTrip`.
- **Verification:**
  - `cd agents-plugin-tool && go test -race ./internal/wswhiteboard/...`: PASS (`ok`, plus post-fix `go test -v -race -run 'TestMockup|TestArchive|TestBoardDBArchive|TestLoadArchive|TestPanelSrc' ./internal/wswhiteboard/...`: PASS)
  - `cd agents-plugin-tool && go test -race -run 'TestMockup|TestFS|TestFileRoute|TestBoard|TestDashboard|TestAccess|TestPage' ./internal/wsdashboard/...`: PASS (including Node JS tests for lazy mount, remount, unmount, `frameKeep`, `paletteBodyText`, and `dashboard.js` bundle syntax)
  - `cd agents-plugin-tool && go test -race -run 'TestWhiteboard|TestDashboard' ./internal/mcp/...`: PASS
  - `cd agents-plugin-tool && GOOS=windows go vet ./... && GOOS=windows go build ./...`: PASS

### Phase 2: `lead-mockup` & `mockup-writer` playbooks, `tier-role` inventory, skills, `wsflow` mirrors, and `whiteboard` doc updates

Depends on Phase 1 landing so the shipped playbooks and `whiteboard` documentation match live multi-file `wb:panel src` behavior.

1. **Playbooks & docs (`agents-plugin/rsrc/`):**
   - Author `agents-plugin/rsrc/lead-mockup/lead-mockup.md` (guides lead to classify `visual` vs `interactive-mock`, agree scope/options, render & spawn `mockup-writer` with `tier_override`, mount `ai-docs/assets/mockups/YYMMDD/<topic>/index.html` via `wb:panel src`, and present design decisions on the whiteboard).
   - Author `agents-plugin/rsrc/mockup-writer/mockup-writer.md` (`kind: render`, `delegates: true`, `role: worker`, `tier: medium`, `tier-role: mockup-writer`, `tier-parent: implement:*`, `includes: [worker-stop-protocol]`, supporting `visual` and `interactive-mock` modes, `explore` subagent research, minimal `YYMMDD/<topic>/index.html` convention, the Decision 11 rules (own subfolder, allowlisted file types only; references to other files 404), the Decision 15 note (load assets up front; reopen a stale mock), and returning entry path + design decisions).
   - Update `agents-plugin/rsrc/whiteboard/whiteboard.md` to document multi-file full-HTML `wb:panel src` support (the folder is snapshotted at post time and edits need a new post; `wb:change` is refused on mockups; only allowlisted file types are bundled; the mockup must sit in its own subfolder; reopen a mock left open past the capability TTL) and `visual` vs `interactive-mock` workflow pointers.
   - Apply `ai-docs/manuals/skill-authoring.md` invariant checklist and `ai-docs/manuals/shipped-surface-boundary.md` checks to every added/edited line.
2. **Skills, `wsflow` mirrors, and manifests:**
   - Create `agents-plugin/skills/lead-mockup/SKILL.md` and `agents-plugin-wsflow/skills/lead-mockup/SKILL.md` (`<ws-preload playbook="lead-mockup">`).
   - Run the full regen chain on `w5090`: `TestRegenerateShippedManifest`, `TestRegenerateWsflowRsrcMirror`, and `TestGenerateRealSkillsManifest`.
   - Update `shippedTierRoleInventory` in `agents-plugin-tool/internal/wsrsrc/tier_roles_test.go` (`"mockup-writer": {"implement:*", "medium"}`) and `EXPECTED_SKILLS` in `agents-plugin-wsflow/tests/test_wsflow_skill_bundle.py`.
3. **Verification:**
   - `w5090`: `GOOS=windows go vet ./...`, `go test -race ./internal/wsrsrc/... ./internal/mcp/... ./internal/wspackage/... ./internal/wsdoc/...`, `package-assets --check`.
   - Local Python tests: `pytest agents-plugin-wsflow/tests/`.
   - Content check: read the rendered `lead-mockup` and `mockup-writer` (via `playbook.read`, or the regenerated `wsflow` mirror) and confirm all of the following:
     - Both name the `visual` and `interactive-mock` modes; `interactive-mock` requires the separate `mock-api.js` adapter.
     - `mockup-writer`'s frontmatter matches Decision 3, and it returns the entry path plus design decisions.
     - The `whiteboard` text states the post-time snapshot rule.
     - Both `whiteboard` and `mockup-writer` state the Decision 11 subfolder and file-type allowlist rules, and the Decision 15 reopen-a-stale-mock note.
     - The shipped-surface boundary check finds no `devenv`-only manual or repo-posture reference.
   - Done when every command above passes, the content check holds, and `/lead-mockup` resolves as a skill in both `agents-plugin` and `agents-plugin-wsflow`.

### Result (c054d54) - 2026-11-26

- **Landed (`c054d54`):**
  - Authored downstream-first, host-neutral playbooks `agents-plugin/rsrc/lead-mockup/lead-mockup.md` and `agents-plugin/rsrc/mockup-writer/mockup-writer.md` (`kind: render`, `delegates: true`, `role: worker`, `tier: medium`, `tier-role: mockup-writer`, `tier-parent: implement:*`, `includes: [worker-stop-protocol]`), covering `visual` vs `interactive-mock` (with separate `mock-api.js`), `ExploreAgent` research, `ai-docs/assets/mockups/YYMMDD/<topic>/index.html`, Decision 11 subfolder/allowlist rules, Decision 15 TTL reopen note, and entry-path + design-decisions reporting.
  - Updated `agents-plugin/rsrc/whiteboard/whiteboard.md` with post-time snapshot rules, subfolder/allowlist/cap rules, `wb:change` refusal on mockups, TTL reopen note, and `lead-mockup` workflow pointers.
  - Added `agents-plugin/skills/lead-mockup/SKILL.md` and `agents-plugin-wsflow/skills/lead-mockup/SKILL.md`, regenerated `agents-plugin/rsrc/manifest.json`, `agents-plugin-wsflow/rsrc/` mirror & manifest, and `agents-plugin/skills/manifest.json`, and updated `shippedTierRoleInventory` (`tier_roles_test.go`) and `EXPECTED_SKILLS` (`test_wsflow_skill_bundle.py`).
- **Verification:**
  - `WS_REGEN_MANIFEST=1 go test ./internal/wsrsrc -count=1 -run TestRegenerateShippedManifest`: PASS
  - `WS_REGEN_WSFLOW_RSRC=1 go test ./internal/wsrsrc -count=1 -run TestRegenerateWsflowRsrcMirror`: PASS
  - `WSRSRC_REGEN_SKILLS=1 go test ./internal/wsrsrc -count=1 -run TestGenerateRealSkillsManifest`: PASS
  - `python3 agents-plugin-tool/scripts/generate-package-assets.py && python3 agents-plugin-tool/scripts/generate-package-assets.py --check`: PASS
  - `cd agents-plugin-tool && GOOS=windows go vet ./... && go test -race ./internal/wsrsrc/... ./internal/wspackage/... ./internal/wsdoc/...`: PASS
  - `cd agents-plugin-tool && go test -race -run 'TestPlaybook|TestTier|TestSkill|TestDashboardSettings|TestDashboardTierRouting' ./internal/mcp/...`: PASS
  - `python3 -m unittest discover -s agents-plugin-wsflow/tests -v`: PASS (22 tests, 2 Windows-only skipped)
  - Content & shipped-surface boundary check on regenerated `agents-plugin-wsflow/rsrc/{lead-mockup,mockup-writer,whiteboard}` and skills: PASS (zero `devenv`-only references; zero `ws/`/`ws:`/`ws.` leaks in `agents-plugin-wsflow`).


## Resolution (2026-11-26)

Completed Phases 1 and 2 on `impl/develop/said-shirt-crock` (`9c0c462`, `c054d54`, review fix `180629f`). Multi-file full-HTML `wb:panel src` snapshotting, T1 SQLite `mockup_bundles` persistence, archive/backup round-trip, `/s/<rid>/board/mockup` + `/s/<rid>/fs/mock/` serving with 64 MiB LRU, lazy iframe mounting & morph preservation, `lead-mockup` & `mockup-writer` playbooks, `ws`/`wsflow` skills, manifests/mirrors, and `whiteboard` doc updates are verified and reviewed.
