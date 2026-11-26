---
title: "컴패니언 VRM 팩 스토어, 단일 .vrm 드롭인 및 공식 opt-in 레지스트리 지원"
parent: 261122-epic-werkschaft-lead-companion
related:
  261126-research-ws-companion-hybrid-vrm-bust-spike: verified findings, compatibility audit, and owner decisions (10)-(14)
  261122-feat-ws-companion-pack-store: existing ws-companion/v1 memory-first 2D pack store
  261122-feat-ws-companion-pack-sources: existing marketplace source fetch/install pipeline and official source
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 3ae47d045ded44f8
sage-review-completeness-reviewed: 3ae47d045ded44f8
---

# Companion VRM Pack Store, Single-File `.vrm` Drop-In & Official Opt-In Registry

## Background

Following the validated pure-VRM bust spike (`261126-research-ws-companion-hybrid-vrm-bust-spike`, owner decisions `(10)=a`, `(11)=a`, `(12)=a`, `(13)=a`, `(14)=c^GO`), Werkschaft is adding a pure 3D VRM 0.x/1.0 companion track alongside existing `ws-companion/v1` 2D packs.

Today, `agents-plugin-tool/internal/wsdashboard/companion_pack_store.go` and `companion_pack_schema.go` accept only `"schema": "ws-companion/v1"` packs with raster `.webp`/`.png` assets (`2 MiB` per asset, `16 MiB` per pack, `64 MiB` cache). Typical VRoid Studio `.vrm` files are `5–25 MiB` GLB binaries. We need a memory-first, security-hardened VRM pack store that:
1. Coexists cleanly with `"ws-companion/v1"` 2D packs without breaking older daemons (**T1 display-degraded, patch-safe**).
2. Accepts **single-file `.vrm` drop-ins** (auto-generating a minimal VRM pack) as well as `.zip` VRM packs (with optional `expressionMap` overrides, `.vrma` clips, thumbnail, and persona metadata).
3. Keeps heavy `.vrm` models out of the Go binary by exposing an **opt-in official character download** through the existing `companion_sources` official marketplace pipeline alongside a local `.vrm` import button.

## Decisions

1. **Additive `"schema": "ws-companion-vrm/v1"` manifest coexisting with `"ws-companion/v1"`:**
   - Stored under the same `<config-home>/companions/<id>/` directory as 2D packs using the same `companionPackID` regex (`^[a-z0-9][a-z0-9_-]{0,63}$`).
   - Literal `pack.json` schema sketch:
     ```json
     {
       "schema": "ws-companion-vrm/v1",
       "id": "my-vrm-avatar",
       "name": "My Avatar",
       "model": "model.vrm",
       "thumbnail": "thumb.webp",
       "vroidSourceUrl": "https://...",
       "persona": { "name": "...", "voice": "...", "quipLanguage": "ko" },
       "expressionMap": {
         "oo": { "preset": "surprised", "weight": 0.95 },
         "happy": { "preset": "happy", "weight": 0.92 }
       },
       "animations": {
         "wave": "wave.vrma"
       }
     }
     ```
     Required fields: `schema`, `id` (must match directory name when present), `name`, `model` (flat `.vrm` filename matching `^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}\.vrm$`). Optional fields: `thumbnail` (`.webp`/`.png`), `vroidSourceUrl` (`https://` URL only), `persona` (reusing the shipped `CompanionPersona` shape `{name, voice, quipLanguage}` with the same schema bounds as `ws-companion/v1`, so `companion_director.go` persona voice works unchanged), `expressionMap`, `animations`, and open extension fields (preserved/ignored by validator).
   - Pinned shapes (validated safety-only; a violation rejects that single pack with a per-pack error and never blocks sibling packs):
     - `expressionMap`: at most 16 entries; key = Werkschaft expression ID matching `^[a-z][a-z0-9_]{0,31}$`; value = `{ "preset": <string, 1-64 chars> | "custom": <string, 1-64 chars>, "weight": <number 0..1, default 1> }` with exactly one of `preset`/`custom`. The expression ID keys are not enumerated here: the engine ticket owns the ID vocabulary and consumes the map.
     - `animations`: at most 16 entries; key matches the same ID regex; value is a flat `.vrma` filename matching `^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}\.vrma$` that exists in the pack.
     - `framing` is not a field of this schema: no consumer exists in the engine ticket, so it is not specified; an author-supplied `framing` is an unknown extension field and passes through untouched.
   - The summary in `GET /api/companion/packs` carries only `schema` and `renderer`; the frontend reads `model`, `expressionMap`, and `animations` from the raw manifest via the existing `pack.json` route.
   - Rejected: a separate `<config-home>/companions-vrm/` directory (duplicates pack IDs, `dashboard.companion_pack` resolution, and source install/uninstall plumbing without adding compatibility safety beyond `schema` discrimination), and a new `{name, prompt}` persona shape (breaks reuse of shipped `CompanionPersona`).

2. **Compatibility Tier Verdict — T1 (Display-Degraded, Patch-Safe) per `ai-docs/manuals/release-compatibility.md`:**
   - **Old daemon reading a VRM pack written by the new daemon:** `scanLocal` / `loadCompanionLocal` / `readCompanionLocalFile` (`companion_pack_store.go:161-270`, `companion_pack_file_unix.go:15-30`, `companion_pack_file_windows.go:14-63`) are strictly read-only (`O_RDONLY`/`GENERIC_READ`). `validateCompanionManifest` (`companion_pack_schema.go:14,220-222`) rejects `"ws-companion-vrm/v1"` for that single pack via `snapshot.addError` (`companion_pack_store.go:211-213`) without incrementing `cached` (`L215`) and preserves `"default"` plus all valid 2D packs (`L135-144,216,332-335`).
   - **Old daemon reading `dashboard.companion_pack` pointing at a VRM pack ID:** `dashboard-companion-packs.js:142-179` detects the missing ID (`404`) and falls back to rendering `"default"` without writing `/api/config/set`; `dashboard-companion-settings.js:71-83,109,255-263` preserves the stored config value until explicit user change. Reopening with the new daemon restores the untouched VRM pack and setting.
   - **Old daemon reading a marketplace containing a VRM entry:** `parseCompanionMarketplace` / `parseCompanionMarketplaceEntry` (`companion_pack_zip.go:480-593`) ignore unknown entry fields (`T0`) and isolate per-entry zip/schema rejections (`companion_pack_install.go:449-482`) without affecting sibling 2D entries. `assets/companion/official/marketplace.json` is embedded per binary so old daemons never read new embedded official VRM entries.
   - **Config keys:** No new top-level `config.json` members or manifest struct fields; `dashboard.companion_pack` and `dashboard.companion_sources` are unchanged (`T0`).

3. **Single-file `.vrm` drop-in import (`(11)=a`):**
   - Add `POST /api/companion/packs/import-vrm`, with the same write gate as the sibling `validate`/`import`/`uninstall` routes (`TrustedWrite`, serialized under `installMu`). Contract: either a raw body (`Content-Type: model/gltf-binary` or `application/octet-stream`) with optional `id` and `name` query parameters, or `multipart/form-data` with a `file` part and optional `id`/`name` fields. An explicit `id` that collides with an installed pack answers `409` (no overwrite; replacing is uninstall then import); a derived slug that collides is suffixed (`-2`, `-3`, ...). Other errors: `400` invalid/non-VRM/malformed GLB, `413` over `companionVrmAssetCap`, `415` unsupported content type. Success answers `200` with the installed pack summary (`schema`, `renderer`, `id`, `name`). Also allow `/api/companion/packs/validate` and `/api/companion/packs/import` to accept `.zip` archives containing `"ws-companion-vrm/v1"` packs.
   - `.vrm` binary validation before staging:
     - Check GLB magic header (`0x46546C67` = `"glTF"`, version `2`, declared byte length matching payload).
     - Parse only the first GLB JSON chunk (chunk type `0x4E4F534A` = `"JSON"`) and verify that `extensions.VRM` (VRM 0.x) or `extensions.VRMC_vrm` (VRM 1.0) is present. Reject arbitrary non-VRM GLBs and malformed chunks.
     - Derive a clean ASCII slug `id` from the provided name/filename (sanitized to `^[a-z0-9][a-z0-9_-]{0,63}$`, collision-suffixed if needed) and synthesize a minimal `"ws-companion-vrm/v1"` `pack.json` + `.ws-install.json` sidecar, then install atomically via the existing `.companion-staging/` swap and reload the in-memory snapshot.
   - Rejected: requiring users to hand-author `pack.json` and zip it for ordinary VRoid Studio exports (breaks frictionless ecosystem drop-in).

4. **Memory-first serving & safe `.vrm` size caps (`ai-docs/manuals/performance.md`):**
   - Keep `ws-companion/v1` 2D raster caps unchanged (`2 MiB` raster, `16 MiB` 2D pack): per-asset and per-pack caps are enforced per schema, so only the shared operation-level caps below change, and a 2D pack over `16 MiB` (or a 2D asset over `2 MiB`) is still rejected. Today `companionUploadCap` and `companionSourceCap` are already `64 MiB`; `companionPackZipCap` is `16 MiB` and `companionCacheCap` is `64 MiB`, so the net changes are `companionPackZipCap` to `40 MiB` and `companionCacheCap` to `128 MiB`. Both also widen the 2D external-download and cache budget; that is accepted (no per-pack 2D limit is loosened). The authoring guide's size table and its test pin (`companion_pack_guide_test.go`) follow the constants.
   - Add dedicated VRM caps:
     - `companionVrmAssetCap = 32 << 20` (`32 MiB` per `.vrm` file)
     - `companionVrmaAssetCap = 4 << 20` (`4 MiB` per `.vrma` file)
     - `companionVrmPackCap = 40 << 20` (`40 MiB` per VRM pack)
     - `companionCacheCap = 128 << 20` (`128 MiB` total in-memory pack cache across local packs; `companionUploadCap` / `companionSourceCap` / `companionPackZipCap` updated consistently to `64 MiB` / `64 MiB` / `40 MiB`).
   - Serve `.vrm` and `.vrma` through `GET /api/companion/packs/{id}/assets/{file}` with `Content-Type: model/gltf-binary`, `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'none'; sandbox`, `ETag`, and `?rev=<rev>` `Cache-Control: private, max-age=31536000, immutable` from the in-memory snapshot (zero per-request disk I/O).
   - Expose `"renderer": "2d" | "vrm"` and `"schema"` on each pack summary in `GET /api/companion/packs` AND expose `renderer` to `companion_director.go` (`companionPackFor`) so both the frontend and the agent director know whether a pack is 2D or VRM before fetching assets or deriving allowed vocabulary.

5. **Official opt-in VRM asset pack registry & UI CTA (`(13)=a`):**
   - Keep the Go binary free of embedded `.vrm` binaries.
   - Extend `assets/companion/official/marketplace.json` schema-compatibly (keeping `"schema": "ws-companion-marketplace/v1"`) so official VRM entries can carry optional `"format": "vrm"` and `"vroidSourceUrl"` metadata (and handle cleanly when the official VRM release is published vs not yet published). "Not yet published" means no `format: "vrm"` entry is listed (the embedded list is `"packs": []` today); an entry with an empty URL is never written, because the marketplace parser rejects an entry with neither `path` nor `url` and requires a `sha256` pin on a `url` entry. The published official artifact must therefore be a sha256-pinned zip pack containing a `ws-companion-vrm/v1` pack.
   - Add two clear controls to the companion settings/sources UI (`dashboard-companion-settings.js`, `dashboard-companion-sources.js`, `wsi18n` EN/KO):
     - **Import `.vrm` file** (file picker + drag-and-drop onto the modal calling `POST /api/companion/packs/import-vrm`, immediately selecting the newly installed pack).
     - **Official VRM character download (opt-in)** button wired to the official marketplace source (`POST /api/companion/sources/fetch` / `install`), shown prominently when no VRM pack is installed yet (with graceful "coming soon / not yet published" disabled or informative state if the official registry lists no `format: "vrm"` entry yet).

## Constraints

- Follow `ai-docs/manuals/release-compatibility.md` (T1 verdict above; no T3/minor bump; preserve all existing `ws-companion/v1` behavior and tests).
- Follow `ai-docs/manuals/performance.md` (Type 5 memory-first blobs; lazy first-request scan; zero disk I/O on daemon boot with `companion_enabled=false` and zero per-request disk reads on warm asset hits; retain `O_NOFOLLOW`/`openat` on Unix and `OBJ_DONT_REPARSE`/`NtCreateFile` on Windows).
- Follow `ai-docs/manuals/config-settings-ui.md` and `ai-docs/manuals/dashboard-style.md` for all settings/sources UI additions and `wsi18n` labels.
- Never embed third-party or community `.vrm` files in the repository, and never put character/model/company/artist names in tracked files or tests.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/)

## Prior Decisions

- 261122-epic-werkschaft-lead-companion (2026-11-26, Cross-Child Decisions): "Pure-VRM track alongside 2D compatibility (owner 2026-11-26, (10)=a, (11)=a, (12)=a, (13)=a, (14)=c^GO): Coexistence & T1 boundary. Existing ws-companion/v1 2D packs remain supported and untouched." — bearing: constrains
- 261126-research-ws-companion-hybrid-vrm-bust-spike (2026-11-26, Confirmed Decisions): "(13)=a: Separate runtime from official VRM binary: bundle only the lightweight lazy-loaded 3D runtime (~243 KiB gzip) ... host the 1 official .vrm asset externally" — bearing: constrains
- f8ab72f0 (2026-11-26, commit): "Changed ws-companion-vrm/v1 persona sketch to reuse CompanionPersona ({name, voice, quipLanguage}), added companion_director.go + companion_director_test.go ownership across pack-store and engine" — bearing: supports
- 261126-feat-ws-companion-vrm-engine (2026-11-26, Decisions): "T0 (transparent, patch-safe) for all frontend/route additions ... 2D ws-companion/v1 behavior is preserved unchanged" — bearing: supports
- 03d8efa1 (2026-11-23, commit): "Tests: stale backup never resurrects across update/uninstall/install; stale removal failure refuses install and uninstall; ambiguous backups refused untouched; partial recovery publishes restored pack" — bearing: constrains
- 261122-feat-ws-companion-pack-sources (2026-11-22, commit fe34eb47): "Part of 261122-feat-ws-companion-pack-sources Phase 2 (Goals 3-4, Decisions 5, 8, 10)." (official marketplace.json, install and source routes) — bearing: supports
- 261122-chore-ws-companion-official-art-pack (2026-11-26, c985fde8): "Owner (133)=c: official-art-pack deferred (stays in todo/, not dropped); the two VRM children replace it as the Done condition." — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | wsdashboard companion_pack_schema.go, companion_pack_store.go, companion_pack_routes.go, companion_pack_zip.go, companion_pack_install.go, companion_pack_install_routes.go, companion_pack_source_routes.go, companion_director.go, new companion_pack_vrm.go, assets/dashboard-companion-settings.js and dashboard-companion-sources.js, wsi18n |
| scope.surface | cross-module | new HTTP route POST /api/companion/packs/import-vrm, new pack summary fields renderer and schema, manifest schema ws-companion-vrm/v1, dashboard JS |
| scope.new_public_symbol | no | new Go symbols are package-internal (companionVrmAssetCap and similar); the HTTP route and JSON fields are the new interface, no exported Go symbol named |
| scope.new_type_contract | yes | ws-companion-vrm/v1 manifest struct, renderer field on pack summary and companionPackData, import-vrm request contract |
| scope.test_surface | existing | companion_pack_store_test.go, companion_pack_schema_test.go, companion_pack_install_test.go, companion_pack_routes_test.go, companion_director_test.go, frontend-companion-sources.test.js; new GLB fixture tests likely in new files |
| complexity.reuse_points | confirmed | companionPackID (companion_pack_schema.go#L17), CompanionPersona (companion_pack_schema.go#L85), companionStagingName swap (companion_pack_install.go#L22), companion_pack_file_unix.go/windows openers, handleCompanionAsset route |
| complexity.side_effect_risk | high | raises cache and zip caps shared with 2D packs, changes asset serving headers and director pack data, adds an upload path writing to config-home |
| risk.correctness | high | hand-written GLB header and JSON chunk parser on untrusted input, size-cap arithmetic across cache budget |
| risk.fit | moderate | fits the existing memory-first store but touches many files in one phase and a pending sibling ticket owns the VRM vocabulary |
| risk.test | moderate | needs synthetic VRM 0.x and 1.0 GLB fixtures, Windows vet and T1 old-validator compatibility test; no existing GLB test harness |
| risk.security_or_contract | high | untrusted binary upload, symlink and path safety, new CSP and content-type serving, persistent schema contract read by older daemons |

## Phases

### Phase 1: VRM Pack Schema, Store, Single-File `.vrm` Import & Official Opt-In UI

Implement `"schema": "ws-companion-vrm/v1"` support end to end in the Go companion pack store, HTTP routes, zip/marketplace installer, and dashboard settings/sources modal:

1. **Schema & GLB/VRM header validation (`companion_pack_schema.go`, `companion_pack_vrm.go`):**
   - Parse and validate `"ws-companion-vrm/v1"` manifests (`id`, `name`, `model`, optional `thumbnail`, `vroidSourceUrl`, `persona`, `expressionMap`, `animations`, with the pinned shapes and bounds of Decision 1) with strict flat filename checks and unknown-field pass-through.
   - Implement a pure-Go minimal GLB header + first-JSON-chunk inspector verifying `glTF` v2 magic, length bounds, and presence of `extensions.VRM` or `extensions.VRMC_vrm` (and `extensions.VRMC_vrm_animation` for `.vrma`), returning detected VRM version (`"0.x"` | `"1.0"`).
2. **Memory-first store, asset serving & director pack exposure (`companion_pack_store.go`, `companion_pack_routes.go`, `companion_director.go`, `companion_director_test.go`):**
   - Load both `"ws-companion/v1"` and `"ws-companion-vrm/v1"` packs into `companionSnapshot` with per-type asset/pack caps (`32 MiB` `.vrm`, `4 MiB` `.vrma`, `40 MiB` VRM pack, `128 MiB` cache cap) using the existing symlink-safe file openers.
   - Include `"renderer": "2d" | "vrm"` and `"schema"` in `GET /api/companion/packs` summaries, and expose `renderer` (and `CompanionPersona`) through `companionPackFor` in `companion_director.go` (preserving existing 2D pack behavior and adding unit test coverage in `companion_director_test.go`; the VRM vocabulary ID tables themselves are owned by `261126-feat-ws-companion-vrm-engine`).
   - Serve `.vrm` and `.vrma` from `GET /api/companion/packs/{id}/assets/{file}` with `model/gltf-binary`, `nosniff`, sandbox CSP, and immutable `?rev=` caching.
3. **Single-file `.vrm` drop-in & `.zip` VRM install (`companion_pack_zip.go`, `companion_pack_install.go`, `companion_pack_install_routes.go`, `companion_pack_source_routes.go`):**
   - Allow `.vrm` and `.vrma` in archive validation when the pack manifest declares `"schema": "ws-companion-vrm/v1"` (while keeping `.webp`/`.png`-only enforcement on `"ws-companion/v1"` packs). Archive validation currently rejects every non-raster file before `pack.json` is read (`companionArchive.pack`) and then runs the raster check on every asset (`prepare`), so both sites need a manifest peek and a per-schema branch (raster check for 2D, GLB/VRM inspector for VRM).
   - Add `POST /api/companion/packs/import-vrm` to validate a single uploaded `.vrm` file, synthesize `pack.json` + `.ws-install.json`, stage and atomically install under `<config-home>/companions/<id>/`, reload snapshot, and return the installed pack summary.
   - Preserve optional `"format"` and `"vroidSourceUrl"` metadata on marketplace entries in `GET/POST /api/companion/sources/*`.
4. **Settings & Sources Modal UI (`dashboard-companion-settings.js`, `dashboard-companion-sources.js`, `wsi18n`):**
   - Add local `.vrm` file picker + drag-and-drop import zone calling `POST /api/companion/packs/import-vrm` and switching `dashboard.companion_pack` to the imported ID on success.
   - Add opt-in "Official VRM Character" download action backed by the `official` marketplace source (displaying a clear badge/state if no published official VRM entry is live yet) and show a `VRM` format badge beside VRM packs in the selector/source list.
   - Keep zero WebGL / zero extra asset fetches when `dashboard.companion_enabled` is `false`.
   - Landing note: until `261126-feat-ws-companion-vrm-engine` lands, importing a VRM pack selects it but the 2D stage cannot render it and falls back to `default` without writing config; the UI must not claim an immediate visible switch (show the pack as installed and selected, with the VRM badge).

5. **Verification** (checkpoints: items 1-2 (Go store and serving) are complete when bullets (a)-(c) pass without the UI; item 3 (install paths) when (d)-(f) pass; item 4 (UI) when (g) passes):
   - Unit tests (`go test ./internal/wsdashboard/...`) covering:
     - (a) valid minimal synthetic VRM 0.x & 1.0 GLBs, invalid/truncated/non-VRM GLB rejection, and `expressionMap`/`animations` bound violations rejecting only that pack;
     - (b) `.vrm`/`.vrma` size-cap and symlink rejection, plus a regression that a 2D pack over `16 MiB` and a 2D asset over `2 MiB` are still rejected after the shared caps are raised;
     - (c) `model/gltf-binary` + `ETag`/`304` + `nosniff` + sandbox CSP route headers, `renderer`/`schema` on pack summaries, and `renderer` exposure through `companionPackFor` with existing 2D director behavior unchanged (`companion_director_test.go`);
     - (d) single-file `.vrm` drop-in import (raw and multipart, `409` on explicit-id collision, suffix on derived-slug collision, `TrustedWrite` gate) & atomic reload;
     - (e) `.zip` VRM pack import & uninstall, and 2D `.webp`/`.png`-only enforcement preserved;
     - (f) marketplace `format`/`vroidSourceUrl` passthrough and a T1 compatibility test verifying a `"ws-companion/v1"`-only validator skips a `"ws-companion-vrm/v1"` pack without modifying disk or blocking sibling 2D packs;
     - the guide-size-table test follows the new constants.
   - (g) Frontend tests (extend `frontend-companion-sources.test.js`): file picker/drop calls `import-vrm` and selects the new pack; the official CTA shows the not-yet-published state when no `format: "vrm"` entry exists and the install action when one does; the VRM badge renders; no WebGL and no extra asset fetches when `companion_enabled` is false.
   - `GOOS=windows go vet ./...` and existing `agents-plugin-tool` test suite pass.
