---
title: "컴패니언 VRM 팩 스토어, 단일 .vrm 드롭인 및 공식 opt-in 레지스트리 지원"
parent: 261122-epic-werkschaft-lead-companion
related:
  261126-research-ws-companion-hybrid-vrm-bust-spike: verified findings, compatibility audit, and owner decisions (10)-(14)
  261122-feat-ws-companion-pack-store: existing ws-companion/v1 memory-first 2D pack store
  261122-feat-ws-companion-pack-sources: existing marketplace source fetch/install pipeline and official source
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
     Required fields: `schema`, `id` (must match directory name when present), `name`, `model` (flat `.vrm` filename matching `^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}\.vrm$`). Optional fields: `thumbnail` (`.webp`/`.png`), `vroidSourceUrl` (`https://` URL only), `persona` (reusing the shipped `CompanionPersona` shape `{name, voice, quipLanguage}` with the same schema bounds as `ws-companion/v1`, so `companion_director.go` persona voice works unchanged), `expressionMap` (open map of Werkschaft expression ID to preset/custom blendshape binding), `animations` (map of pose/gesture ID to flat `.vrma` filename), `framing` (optional bust camera tweaks), and open extension fields (preserved/ignored by validator).
   - Rejected: a separate `<config-home>/companions-vrm/` directory (duplicates pack IDs, `dashboard.companion_pack` resolution, and source install/uninstall plumbing without adding compatibility safety beyond `schema` discrimination), and a new `{name, prompt}` persona shape (breaks reuse of shipped `CompanionPersona`).

2. **Compatibility Tier Verdict — T1 (Display-Degraded, Patch-Safe) per `ai-docs/manuals/release-compatibility.md`:**
   - **Old daemon reading a VRM pack written by the new daemon:** `scanLocal` / `loadCompanionLocal` / `readCompanionLocalFile` (`companion_pack_store.go:161-270`, `companion_pack_file_unix.go:15-30`, `companion_pack_file_windows.go:14-63`) are strictly read-only (`O_RDONLY`/`GENERIC_READ`). `validateCompanionManifest` (`companion_pack_schema.go:14,220-222`) rejects `"ws-companion-vrm/v1"` for that single pack via `snapshot.addError` (`companion_pack_store.go:211-213`) without incrementing `cached` (`L215`) and preserves `"default"` plus all valid 2D packs (`L135-144,216,332-335`).
   - **Old daemon reading `dashboard.companion_pack` pointing at a VRM pack ID:** `dashboard-companion-packs.js:142-179` detects the missing ID (`404`) and falls back to rendering `"default"` without writing `/api/config/set`; `dashboard-companion-settings.js:71-83,109,255-263` preserves the stored config value until explicit user change. Reopening with the new daemon restores the untouched VRM pack and setting.
   - **Old daemon reading a marketplace containing a VRM entry:** `parseCompanionMarketplace` / `parseCompanionMarketplaceEntry` (`companion_pack_zip.go:480-593`) ignore unknown entry fields (`T0`) and isolate per-entry zip/schema rejections (`companion_pack_install.go:449-482`) without affecting sibling 2D entries. `assets/companion/official/marketplace.json` is embedded per binary so old daemons never read new embedded official VRM entries.
   - **Config keys:** No new top-level `config.json` members or manifest struct fields; `dashboard.companion_pack` and `dashboard.companion_sources` are unchanged (`T0`).

3. **Single-file `.vrm` drop-in import (`(11)=a`):**
   - Add `POST /api/companion/packs/import-vrm` (accepting raw `.vrm` upload or `multipart/form-data` with `.vrm` file and optional `id`/`name`), plus allow `/api/companion/packs/validate` and `/api/companion/packs/import` to accept `.zip` archives containing `"ws-companion-vrm/v1"` packs.
   - `.vrm` binary validation before staging:
     - Check GLB magic header (`0x46546C67` = `"glTF"`, version `2`, declared byte length matching payload).
     - Parse only the first GLB JSON chunk (chunk type `0x4E4F534A` = `"JSON"`) and verify that `extensions.VRM` (VRM 0.x) or `extensions.VRMC_vrm` (VRM 1.0) is present. Reject arbitrary non-VRM GLBs and malformed chunks.
     - Derive a clean ASCII slug `id` from the provided name/filename (sanitized to `^[a-z0-9][a-z0-9_-]{0,63}$`, collision-suffixed if needed) and synthesize a minimal `"ws-companion-vrm/v1"` `pack.json` + `.ws-install.json` sidecar, then install atomically via the existing `.companion-staging/` swap and reload the in-memory snapshot.
   - Rejected: requiring users to hand-author `pack.json` and zip it for ordinary VRoid Studio exports (breaks frictionless ecosystem drop-in).

4. **Memory-first serving & safe `.vrm` size caps (`ai-docs/manuals/performance.md`):**
   - Keep `ws-companion/v1` 2D raster caps unchanged (`2 MiB` raster, `16 MiB` 2D pack).
   - Add dedicated VRM caps:
     - `companionVrmAssetCap = 32 << 20` (`32 MiB` per `.vrm` file)
     - `companionVrmaAssetCap = 4 << 20` (`4 MiB` per `.vrma` file)
     - `companionVrmPackCap = 40 << 20` (`40 MiB` per VRM pack)
     - `companionCacheCap = 128 << 20` (`128 MiB` total in-memory pack cache across local packs; `companionUploadCap` / `companionSourceCap` / `companionPackZipCap` updated consistently to `64 MiB` / `64 MiB` / `40 MiB`).
   - Serve `.vrm` and `.vrma` through `GET /api/companion/packs/{id}/assets/{file}` with `Content-Type: model/gltf-binary`, `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'none'; sandbox`, `ETag`, and `?rev=<rev>` `Cache-Control: private, max-age=31536000, immutable` from the in-memory snapshot (zero per-request disk I/O).
   - Expose `"renderer": "2d" | "vrm"` and `"schema"` on each pack summary in `GET /api/companion/packs` AND expose `renderer` to `companion_director.go` (`companionPackFor`) so both the frontend and the agent director know whether a pack is 2D or VRM before fetching assets or deriving allowed vocabulary.

5. **Official opt-in VRM asset pack registry & UI CTA (`(13)=a`):**
   - Keep the Go binary free of embedded `.vrm` binaries.
   - Extend `assets/companion/official/marketplace.json` schema-compatibly (keeping `"schema": "ws-companion-marketplace/v1"`) so official VRM entries can carry optional `"format": "vrm"` and `"vroidSourceUrl"` metadata (and handle cleanly when the official VRM release URL is configured vs empty/not-yet-published).
   - Add two clear controls to the companion settings/sources UI (`dashboard-companion-settings.js`, `dashboard-companion-sources.js`, `wsi18n` EN/KO):
     - **Import `.vrm` file** (file picker + drag-and-drop onto the modal calling `POST /api/companion/packs/import-vrm`, immediately selecting the newly installed pack).
     - **Official VRM character download (opt-in)** button wired to the official marketplace source (`POST /api/companion/sources/fetch` / `install`), shown prominently when no VRM pack is installed yet (with graceful "coming soon / not yet published" disabled or informative state if the official registry has no published VRM URL yet).

## Constraints

- Follow `ai-docs/manuals/release-compatibility.md` (T1 verdict above; no T3/minor bump; preserve all existing `ws-companion/v1` behavior and tests).
- Follow `ai-docs/manuals/performance.md` (Type 5 memory-first blobs; lazy first-request scan; zero disk I/O on daemon boot with `companion_enabled=false` and zero per-request disk reads on warm asset hits; retain `O_NOFOLLOW`/`openat` on Unix and `OBJ_DONT_REPARSE`/`NtCreateFile` on Windows).
- Follow `ai-docs/manuals/config-settings-ui.md` and `ai-docs/manuals/dashboard-style.md` for all settings/sources UI additions and `wsi18n` labels.
- Never embed third-party or community `.vrm` files in the repository, and never put character/model/company/artist names in tracked files or tests.

## Phases

### Phase 1: VRM Pack Schema, Store, Single-File `.vrm` Import & Official Opt-In UI

Implement `"schema": "ws-companion-vrm/v1"` support end to end in the Go companion pack store, HTTP routes, zip/marketplace installer, and dashboard settings/sources modal:

1. **Schema & GLB/VRM header validation (`companion_pack_schema.go`, `companion_pack_vrm.go`):**
   - Parse and validate `"ws-companion-vrm/v1"` manifests (`id`, `name`, `model`, optional `thumbnail`, `vroidSourceUrl`, `persona`, `expressionMap`, `animations`, `framing`) with strict flat filename checks and unknown-field pass-through.
   - Implement a pure-Go minimal GLB header + first-JSON-chunk inspector verifying `glTF` v2 magic, length bounds, and presence of `extensions.VRM` or `extensions.VRMC_vrm` (and `extensions.VRMC_vrm_animation` for `.vrma`), returning detected VRM version (`"0.x"` | `"1.0"`).
2. **Memory-first store, asset serving & director pack exposure (`companion_pack_store.go`, `companion_pack_routes.go`, `companion_director.go`, `companion_director_test.go`):**
   - Load both `"ws-companion/v1"` and `"ws-companion-vrm/v1"` packs into `companionSnapshot` with per-type asset/pack caps (`32 MiB` `.vrm`, `4 MiB` `.vrma`, `40 MiB` VRM pack, `128 MiB` cache cap) using the existing symlink-safe file openers.
   - Include `"renderer": "2d" | "vrm"` and `"schema"` in `GET /api/companion/packs` summaries, and expose `renderer` (and `CompanionPersona`) through `companionPackFor` in `companion_director.go` (preserving existing 2D pack behavior and adding unit test coverage in `companion_director_test.go`; the VRM vocabulary ID tables themselves are owned by `261126-feat-ws-companion-vrm-engine`).
   - Serve `.vrm` and `.vrma` from `GET /api/companion/packs/{id}/assets/{file}` with `model/gltf-binary`, `nosniff`, sandbox CSP, and immutable `?rev=` caching.
3. **Single-file `.vrm` drop-in & `.zip` VRM install (`companion_pack_zip.go`, `companion_pack_install.go`, `companion_pack_install_routes.go`, `companion_pack_source_routes.go`):**
   - Allow `.vrm` and `.vrma` in archive validation when the pack manifest declares `"schema": "ws-companion-vrm/v1"` (while keeping `.webp`/`.png`-only enforcement on `"ws-companion/v1"` packs).
   - Add `POST /api/companion/packs/import-vrm` to validate a single uploaded `.vrm` file, synthesize `pack.json` + `.ws-install.json`, stage and atomically install under `<config-home>/companions/<id>/`, reload snapshot, and return the installed pack summary.
   - Preserve optional `"format"` and `"vroidSourceUrl"` metadata on marketplace entries in `GET/POST /api/companion/sources/*`.
4. **Settings & Sources Modal UI (`dashboard-companion-settings.js`, `dashboard-companion-sources.js`, `wsi18n`):**
   - Add local `.vrm` file picker + drag-and-drop import zone calling `POST /api/companion/packs/import-vrm` and switching `dashboard.companion_pack` to the imported ID on success.
   - Add opt-in "Official VRM Character" download action backed by the `official` marketplace source (displaying a clear badge/state if no published official VRM entry is live yet) and show a `VRM` format badge beside VRM packs in the selector/source list.
   - Keep zero WebGL / zero extra asset fetches when `dashboard.companion_enabled` is `false`.
5. **Verification:**
   - Unit tests (`go test ./internal/wsdashboard/...`) covering: valid minimal synthetic VRM 0.x & 1.0 GLBs, invalid/truncated/non-VRM GLB rejection, `.vrm`/`.vrma` size-cap and symlink rejection, single-file `.vrm` drop-in import & atomic reload, `.zip` VRM pack import & uninstall, `model/gltf-binary` + `ETag`/`304` + `nosniff` route headers, and T1 compatibility test verifying a `"ws-companion/v1"`-only validator skips a `"ws-companion-vrm/v1"` pack without modifying disk or blocking sibling 2D packs.
   - `GOOS=windows go vet ./...` and existing `agents-plugin-tool` test suite pass.
