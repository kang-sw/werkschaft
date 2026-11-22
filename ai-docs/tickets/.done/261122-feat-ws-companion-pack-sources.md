---
title: "컴패니언 팩 사용자 소스(마켓플레이스) 설치: git/zip 소스, 안전한 압축 해제, 제작 가이드"
parent: 261122-epic-werkschaft-lead-companion
blocked-by: 261122-feat-ws-companion-director#phase1
related:
  261122-feat-ws-companion-pack-store: installs into its <config-home>/companions/ root, reuses its validator and caps, and calls its reload
  261122-feat-ws-companion-paper-doll: may grow the pack schema; the install allow-list follows the validator's file list (sibling branch)
  261122-feat-ws-companion-director: Phase 2 UI lives in its gear modal and uses its catalog surface field; persona stays untrusted data per its Decision 7
  261122-chore-ws-companion-official-art-pack: the official source lists only IP-clean packs such as this one
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 125b2a064343bd72
sage-review-completeness-reviewed: 125b2a064343bd72
completed: 2026-11-22
---

# Companion Pack User Sources (Marketplace Install): Git/Zip Sources, Safe Extraction, Authoring Guide

## Background

The pack store (`261122-feat-ws-companion-pack-store`, done) loads
user packs that already sit in `<config-home>/companions/<id>/`. Users
have to copy those files in by hand. The owner wants users to add
**pack sources** the way plugin marketplaces work: the user adds a source,
looks through the packs it lists, and installs one with a click.
Werkschaft hosts and curates no third-party packs.

Owner decisions (2026-11-22, confirmed):

- **(110)=a:** sources are added only by the user, as plugin
  marketplaces work. Werkschaft runs no central registry of third-party
  links and curates nothing from third parties. The official source lists
  only our own IP-clean packs.
- **(111), owner note 19:24 KST:** a source is a git repository link
  **or** a zip file. A zip uses the same marketplace JSON format, so one zip
  can carry a whole marketplace and its packs (for example, a fan bundle
  shared from a personal cloud drive).

Lead-settled technical decisions follow. Each one gives its reason. They
were auto-confirmed under the owner's standing directive.

## Decisions

1. **Packs hold data only.** An installable pack contains `pack.json`
   and the PNG/WebP files it references. It contains no SVG, HTML, JS, or
   other executable or active content. An install rejects any file in a
   pack directory that is not `pack.json` or a raster referenced by it, and
   stores nothing from that pack. The store already serves only rasters
   (`companion_pack_schema.go:16-17`, `companion_pack_routes.go:58-70`),
   and an unreferenced file would never be served. Rejecting such files
   keeps installed directories exact and bounded. Rejected alternative:
   dropping unknown files with a warning. A pack author would never learn
   that the pack is malformed.

2. **Three source kinds feed one zip pipeline.**
   - (a) **Git repository URL** (`https://<host>/<path>[#<ref>]`; the default
     ref is `HEAD`). The daemon resolves the ref to a commit SHA through
     the smart-HTTP ref advertisement
     (`GET <repo>/info/refs?service=git-upload-pack`, a pkt-line parse).
     A non-`HEAD` ref matches `refs/heads/<ref>` first, then
     `refs/tags/<ref>` (the peeled `^{}` commit for an annotated tag); a
     full 40-hex SHA is used as given. This works on any host and needs
     no host API. It then downloads a zip
     archive pinned to that commit. It tries a fixed, ordered list of two
     common archive URL patterns (`<repo>/archive/<sha>.zip` and
     `<repo>/-/archive/<sha>/<name>-<sha>.zip`), and the first response
     that is a zip wins. A host that matches neither pattern can still be
     used as a zip URL. The daemon never runs `git clone` and never uses
     a git library. This keeps out the symlink, submodule, and
     hook-execution class of git client vulnerabilities, and keeps one
     extraction code path. Rejected alternative: a per-host API client.
     It is host-specific and would name vendors in code.
   - (b) **HTTPS URL to a zip.** A URL whose path ends in `.zip` is
     fetched directly.
   - (c) **Local zip import** from the dashboard: the browser sends the
     file bytes. This kind is needed because consumer cloud-drive share
     links often return an HTML interstitial page instead of a direct
     download. A zip URL whose response is not a zip (checked by its
     magic bytes) fails with a hint to download the file and import it
     locally.
   - All three kinds hand one in-memory zip to the same pipeline
     (Decision 4).

3. **Marketplace zip layout and schema.** The zip root holds
   `marketplace.json`. The name follows the existing plugin marketplace
   file name (`.claude-plugin/marketplace.json`). A zip whose root lacks
   the file but holds exactly one top-level directory with it, as host
   archives do, is read from that directory. Shape:
   ```json
   {
     "schema": "ws-companion-marketplace/v1",
     "name": "Example Packs",
     "owner": { "name": "Example Author", "url": "https://example.invalid/" },
     "packs": [
       { "id": "sample-a", "name": "Sample A", "version": "1.0.0",
         "author": "Example Author", "license": "CC-BY-4.0",
         "description": "optional, <= 280 chars", "path": "packs/sample-a" },
       { "id": "sample-b", "name": "Sample B", "version": "0.3.1",
         "author": "Example Author", "license": "CC-BY-4.0",
         "url": "https://example.invalid/sample-b.zip",
         "sha256": "<64 lowercase hex>" }
     ]
   }
   ```
   - Each entry has exactly one of `path` (a directory inside this zip;
     self-contained bundles are allowed) and `url` + `sha256` (an
     external pack zip whose root, or single top-level directory, is the
     pack).
   - `id` must match `companionPackID` (`companion_pack_schema.go:16`),
     must not be `default` or `auto` (`companion_pack_store.go:190`), must
     equal the pack's `pack.json` `id`, and must be unique in the manifest.
   - `marketplace.json` may be at most 256 KiB and may list at most 32 packs
     (`companionMaxPacks`, `companion_pack_store.go:25`).
   - String fields are capped (`name`, `author`, `license` 64; `version` 32;
     `description` 280). Control characters are rejected.
   - Readers refuse a different `schema` value and ignore unknown fields.
     The rule matches `ws-companion/v1` (`companion_pack_schema.go:108-127`),
     so additions to the manifest stay compatible.
   - Root entries other than `marketplace.json`, and entries outside the
     listed pack directories (for example a repository's README, license,
     or source art), are **ignored**. They are never decompressed or stored.
     They still count toward the entry cap and must pass the name-safety
     checks in Decision 4.

4. **Safe extraction pipeline.** All steps work on the in-memory zip. Any
   failure rejects that pack, or the whole zip for zip-level failures, with
   a structured error.
   - **Open:** `archive/zip.NewReader` over a `bytes.Reader`. Only Store and
     Deflate entries are accepted. Encrypted entries are rejected.
   - **Name safety (zip-level, every entry):** names are UTF-8; NUL bytes,
     backslashes, absolute paths, drive letters, and `..` segments are
     rejected; names are normalized with `path.Clean` and the cleaned
     name must equal the stored name; duplicate names are rejected. The
     central directory may hold at most 4,096 entries.
   - **Directory entries:** a name ending in `/` is a directory entry, as
     host archives list them (`repo-<sha>/`, `repo-<sha>/packs/`). It is
     tolerated and skipped: the name-safety checks run on the name with the
     trailing slash stripped, and it never triggers the nested-subdirectory
     or non-regular-entry rejections. Only a regular file entry under a
     subdirectory of a pack directory is a nested-subdirectory rejection.
     Directory entries count toward the 4,096 entry cap.
   - **Pack-level entry rules:** a pack directory holds at most 256 entries.
     Symlinks (Unix mode bits in the external attributes) and other
     non-regular entries are rejected, as are entries in nested
     subdirectories. Names that differ only by case are rejected, because
     macOS and Windows file systems ignore case. Every file name must be
     `pack.json` or pass `validCompanionRaster` (`companion_pack_schema.go:21`).
     Windows reserved device names (`con`, `prn`, `aux`, `nul`,
     `com1`-`com9`, `lpt1`-`lpt9`, case-insensitive, with or without an
     extension) are rejected for both a pack `id` and a file name, because
     the validators accept them but the write step would fail or hit a
     device on Windows, and the pack would never load.
   - **Caps:** the store's own caps apply: `pack.json` 512 KiB, asset 2 MiB,
     pack 16 MiB, total 64 MiB (`companion_pack_store.go:26-29`). They
     count the bytes actually decompressed (through a `LimitReader`), not
     the header sizes. Per-entry compression ratio is at most 100:1
     (zip-bomb guard). Total decompressed bytes per install operation are
     at most 64 MiB.
   - **Image check:** PNG headers are decoded with the standard library's
     `image/png.DecodeConfig`. WebP uses a small RIFF/`VP8 `/`VP8L`/`VP8X`
     header parser that reads only the dimensions, so no new module
     dependency is added (`agents-plugin-tool/go.mod` has no image module).
     The format must match the extension, and both dimensions must be at
     most 4096. The browser does the full decode in its own decoder.
     Rejected alternative: adding an image module only for header checks.
   - **Manifest validation:** the pipeline calls the existing
     `loadCompanionPack` (`companion_pack_store.go:257-306`) with a read
     function backed by the zip entries. It therefore runs the same
     `validateCompanionManifest` (`companion_pack_schema.go:108`) and the
     same budget logic the scanner uses. A pack that installs is a pack
     that loads. The referenced file list it returns is the install
     allow-list (Decision 1), so schema growth (for example
     `261122-feat-ws-companion-paper-doll`) carries over automatically,
     as long as the new files remain rasters.
   - **Write:** the pack is written to
     `<config-home>/.companion-staging/<random>/<id>/`. Staging sits
     outside `companions/`, so a crash leaves nothing the scanner reports
     (the scanner records every non-ID directory as an error,
     `companion_pack_store.go:192-193`). Only `pack.json`, which keeps its
     original bytes because the store serves raw bytes, the validated
     raster names, and the sidecar of Decision 7 are written. Files are
     never named from zip paths. The staged directory then moves into
     place with an atomic rename. An update renames the old directory
     into staging first and deletes it after the swap. Staging
     directories left over from an earlier run are removed at the next
     install.
   - **Serialization:** one install mutex serializes install, update, and
     uninstall, so two operations on one ID cannot race. The rename swap and
     the following load also run under one acquisition of the store's
     private scan lock (a store-side method that swaps and loads, built on
     a locked variant of `reload`; the existing `reload` takes that lock
     itself and Go mutexes are not reentrant), so a lazy first scan or a user reload
     never sees the pack missing during the swap, and on Windows never
     collides with the scan's pinned handles.
   - **Reload:** the pipeline reloads through the store's `reload`
     (`companion_pack_store.go:105-109`) and returns the new catalog.
   - **Guards:** an install refuses an `id` whose directory exists without
     an install sidecar, because that is a hand-made pack that only the
     user may replace. It also refuses an install that would push local
     packs past `companionMaxPacks` (the count includes error directories,
     as the scan's `visited` does). Uninstall deletes only directories
     that carry a sidecar.
   - **Total cache budget:** the scan gives each pack the remaining part
     of the 64 MiB local cache (`companion_pack_store.go:25-29`) and
     drops a pack that exceeds it, in ascending ID order. So the
     install-time `loadCompanionPack` call gets the cache cap minus the
     bytes of the other installed local packs, read inside the serialized
     section from the store's current snapshot (an update excludes the
     replaced pack's own old bytes), and an install that would exceed it is refused
     with a structured error. The refusal also covers a new pack whose ID
     sorts earlier and would push an installed later pack out of the
     catalog. This keeps "a pack that installs is a pack that loads"
     true across the whole directory.

5. **Integrity and updates only on explicit action.**
   - An external pack `url` requires `sha256`. The hash is checked on the
     downloaded bytes before the zip opens.
   - A git source's integrity rests on HTTPS plus the pinned commit SHA.
     Host archives are not byte-stable over time, so they carry no
     sha256.
   - A zip source's integrity rests on HTTPS. The sidecar records the
     archive's sha256 for display and change detection.
   - Fetch, install, check for updates, and update each run only when the
     user clicks. There is no background polling, timer, or startup fetch
     (`performance.md` Rule 5, page load completes from memory, and its Windows AV file-touch cost).
   - The sidecar records `packSha256` for every installed pack: the
     store's content hash of the installed pack (`info.Rev` from
     `loadCompanionPack`). For an external `url` pack it also records the
     entry's `sha256` as `archiveSha256`.
   - Checking for updates re-fetches the source on the user's click. A
     changed resolved commit or archive sha256 only means the pack must be
     re-read. A pack shows "update available" only when its re-read content
     hash differs from the sidecar's `packSha256` (an external `url` pack:
     when the entry's `sha256` differs from the recorded `archiveSha256`).
     So an unrelated commit in a multi-pack repository raises no badge.
     The `version` string is display-only. Rejected alternative: semver
     comparison. It trusts an author-controlled string and misses
     re-uploads that keep the same version.
   - A `kind: local` install has no source to re-fetch: check and update
     are not offered for it. Importing a newer zip replaces it through the
     same install path.

6. **Pack text is untrusted.**
   - Marketplace strings, pack strings, and persona are untrusted data.
     Persona is handled by the director's length caps and quoting
     (`261122-feat-ws-companion-director` Decision 7). This ticket adds no
     prompt path.
   - All marketplace and pack strings are rendered with `textContent`.
     Links show the full URL, and only `https:` links are rendered as
     links.
   - The UI shows the source origin (the full URL, or "local file
     <name>") on add, fetch, and install. Every non-official source shows
     a warning, in i18n text, that Werkschaft does not review third-party
     packs.

7. **Config key and provenance storage.**
   - **`dashboard.companion_sources`:** a global-only string key, default
     `""`, holding a whitespace-separated list of source URLs (at most 16,
     each `https:` and at most 2,048 characters). Whitespace is used
     because a URL cannot contain it, and the value stays one scalar
     string. An invalid entry is shown in the modal as an error and
     skipped; it never fails the whole key.
   - The key is registered in `manifestGlobalOnlyKeys`
     (`mcp/dashboard_config.go:64-68`) and declared with existing manifest
     fields only, in all dashboard manifests.
   - Its catalog row carries `"surface": "companion-modal"` (director
     Decision 4). The modal renders a list editor that writes through the
     existing `POST /api/config/set {scope:"global"}`.
   - Local file imports are not sources and are not stored in the key.
   - **Install sidecar `<config-home>/companions/<id>/.ws-install.json`:**
     `{source, kind: git|zip|local, ref?, commit?, archiveSha256?,
     packSha256?, version, installedAt, official}`. Storage type: **Type 5
     plain file** (`performance.md` Storage Types). The pack directory is
     a user-visible file tree that users may copy or delete by hand.
     Keeping provenance in the same directory means deleting the pack
     deletes its provenance, and nothing can drift. The file is written
     once per install or update, so the delta-append rule has nothing to
     apply to. Rejected alternative: SQLite (Type 2). It would drift from
     hand-edited directories and needs a reconciliation scan.
   - The leading dot keeps the name outside the raster pattern and away
     from `pack.json`. The store's scan reads the sidecar, only during a
     scan or reload, never on a warm request. It adds an optional
     `origin` object to that pack's catalog entry and ignores a malformed
     sidecar (the pack still loads, with no origin).

8. **Network fetch.**
   - The daemon does all fetching: HTTPS `GET` only. The total timeout is
     60 s. Response caps: 1 MiB for the ref advertisement, 64 MiB for a
     source zip, 16 MiB for an external pack zip.
   - Redirects are followed only to `https:` URLs, at most 5 times.
   - No cookies, credentials, or referrer are sent, and the User-Agent is
     generic.
   - Direct connections refuse loopback, link-local, unspecified, and
     multicast addresses. The check runs at dial time in the dialer's
     `Control` hook, so DNS rebinding cannot get around it. Private
     ranges stay allowed for home NAS hosting. The dial-time address
     guard is a helper whose refused-range policy is a parameter, so a
     stricter caller (the planned web-tools fetch, which also refuses
     private ranges) can reuse it without a second copy.
   - When an environment proxy is set (`http.ProxyFromEnvironment`; this
     matters on corporate PCs), the proxy makes the connection and the
     address check does not apply.
   - All new routes require `TrustedWrite` (`companion_pack_routes.go:140-148`),
     so a LAN client can neither fetch nor install.
   - The browser never fetches source URLs.

9. **Downstream authoring kit (Phase 3).**
   - The daemon embeds a pack authoring guide (Markdown) and two JSON
     Schemas (`ws-companion/v1` `pack.json`, `ws-companion-marketplace/v1`),
     served at new read routes on the companion route family. The modal
     links them.
   - The validate endpoint (`POST /api/companion/packs/validate`, the same
     pipeline with no write) returns per-pack errors. It is built in
     Phase 1; Phase 3 documents it in the guide. The modal's import
     preview is its UI.
   - Under `shipped-surface-boundary.md`, the guide is shipped text and
     must stand alone. It names no ticket stem, repository path, commit
     hash, real character, product, or company. It uses example names and
     `example.invalid` URLs.
   - The repository-internal `ai-docs/manuals/companion-pet-rigging.md`
     stays the canonical rigging contract. The guide is a downstream-facing
     subset of it, and a drift test keeps the two consistent (Phase 3).
   - Rejected alternative: a `companion validate` CLI verb. The validate
     endpoint, the modal dry-run, and the JSON Schemas, which any offline
     schema tool can use, cover authors without widening the hosted CLI
     surface.

10. **The official source is built in.**
    - The daemon embeds a preregistered official marketplace manifest. It
      is not a config entry, so a user cannot delete it by accident, and
      it updates with the binary. It lists only Werkschaft's own IP-clean
      packs.
    - At first it lists only the embedded `default` pack, shown as
      built-in and installed. It has no sidecar, so uninstall is not
      offered for it. Later official packs are added to it by a
      release, with `url` + `sha256`.
    - Only this embedded source carries `official: true`. A user source
      can never claim the flag, whatever its manifest name.

11. **Compatibility tiers (`release-compatibility.md`).** All verdicts are
    T0.
    - **New HTTP routes** (source fetch, install, update, uninstall, local
      import, validate, guide, schemas): T0. They are additive and only the
      frontend bundled in the same binary calls them; the routes are
      registered next to the existing ones (`companion_pack_routes.go:15-20`).
    - **`dashboard.companion_sources` key:** T0. It is a scalar entry in
      the global `overrides` map, which is `map[string]string` both in
      this checkout and in v0.50.8 (`wsconfig/config.go:40`; the same line
      at tag `v0.50.8`), so an older binary keeps the value when it
      rewrites the file. The key adds no new manifest field, so it avoids
      scenario 11's T3 manifest-field boundary
      (`mcp/config_manifest.go:183`).
    - **Install sidecar and staging directory:** T0.
      - v0.50.8 has no pack store; `companion_pack_store.go` is absent at
        that tag.
      - The current store reads only `pack.json` and the files it lists
        (`companion_pack_store.go:272-292`), so a build without this ticket
        ignores `.ws-install.json`.
      - Staging sits outside `companions/` and never enters a scan.
    - **Catalog `origin` field:** T0. It adds an optional JSON member to
      the catalog response (`companion_pack_store.go:53-61`); only the
      bundled frontend reads it.
    - **`ws-companion-marketplace/v1` schema:** a new external contract.
      Its versioning rule (refuse other schema values, ignore unknown
      fields) is fixed in Decision 3.

## Non-Scope

- Hosting any pack, third-party or community, and choosing where future
  non-embedded official packs are hosted. Decision 10 ships the
  mechanism with only the embedded `default` listed.
- A central third-party registry, any curated or recommended list of
  third-party sources, and any telemetry about installs.
- Executable, scripted, SVG, HTML, or otherwise active pack content.
- Auto-update, background update checks, or update notifications that
  need polling.
- Authenticated sources (private repositories, tokens) and non-HTTPS
  sources.
- Installing a bare pack zip without a marketplace manifest. Authors
  wrap a single pack in a one-entry `marketplace.json`.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

Required manuals (per `AGENTS.md` `### Implementation Conventions`):
- `ai-docs/manuals/performance.md`:
  - Fetching or installing anything only on explicit action.
  - Zero added file operations on warm companion requests and while the
    companion is disabled.
  - The scan's sidecar read only during a scan or reload.
  - Report measured file-operation counts for one local import, one
    source install, and one uninstall.
- `ai-docs/manuals/release-compatibility.md`: Decision 11 verdicts;
  confirm them against the landed code.
- `ai-docs/manuals/shipped-surface-boundary.md`: the authoring guide,
  schemas, error messages, and i18n strings are shipped text (Decision 9).
- `ai-docs/manuals/config-settings-ui.md`: the new key goes into the
  manifests, the generator, the catalog row with `surface`, and all
  locale tables together.
- `ai-docs/manuals/companion-pet-rigging.md`: the guide stays a
  consistent subset; document source install in its BYO section.
- IP rule: no real character, product, model, or company appears in code,
  fixtures, tests, the guide, or this ticket. Test fixtures are
  generated, IP-clean rasters.

## Prior Art

- Pack loading and validation reused by the installer: search
  `loadCompanionPack`, `validateCompanionManifest`, and the caps in
  `companion_pack_store.go` / `companion_pack_schema.go`
  (`agents-plugin-tool/internal/wsdashboard/`).
- Pinned-handle disk reads and Windows long paths: `companion_pack_file_*.go`,
  `companion_pack_longpath.go`.
- Trusted write guard and route family: `TrustedWrite` in `access.go`;
  `registerCompanionPackRoutes`.
- Global-only key registration: `manifestGlobalOnlyKeys` in
  `mcp/dashboard_config.go`. Modal and catalog `surface` field: director
  Phase 1.
- Embedded asset serving pattern: `landing_guide.go` (`go:embed` plus
  hashed names).

## Prior Decisions

- a3b05466 (2026-11-22, commit): "Pin the local pack root and its child directories during the scan so renames or symlinks cannot redirect downloads across pack boundaries." — bearing: constrains
- 1afde23b (2026-11-22, commit): "companionExtendedPath mirrors os.addExtendedPrefix's rules (248 threshold ...); the Windows file applies it only to the root, after filepath.Abs" — bearing: constrains
- 261122-feat-ws-companion-pack-store (2026-11-22, commit b59f0c51): "Single self-contained pack.json per pack retained in v1 ... to minimize Windows AV file touches per performance.md." — bearing: constrains
- 2ce97830 (2026-11-22, commit): "Pack-store I/O: one local pack and raster requires 4 opens, 4 stats, and 1 ReadDir cold, zero warm" — bearing: constrains
- 261122-feat-ws-companion-director (2026-11-22, Decisions): "A settings-catalog row may carry "surface": "companion-modal"" — bearing: supports
- fa2b5d91 (2026-11-22, commit): "Use the packaged manifest rules and the global scalar overrides to keep scope and validation aligned with config.tune." — bearing: supports
- 261106-feat-ws-dashboard-browser-interaction (2026-11-07, commit ed832861): "Global-only through wsconfig.RegisterGlobalOnly ... exempted from builtinConfigKeySet (manifestGlobalOnlyKeys)" — bearing: supports
- 261112-feat-ws-mailbox-undelivered-notice (2026-11-13, commit 95f33550): "Keep ws, wsflow, and Pi manifest parity and resource mirrors" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsdashboard/ (new zip pipeline, fetcher, routes, sidecar, embedded guide and schemas), internal/mcp/dashboard_config.go, config manifests, locale tables, frontend modal |
| scope.surface | cross-module | new HTTP routes, the dashboard.companion_sources key, the catalog origin member and the ws-companion-marketplace/v1 contract cross wsdashboard, mcp, wsconfig and the frontend |
| scope.new_public_symbol | yes | dashboard.companion_sources key and its constant, new TrustedWrite routes on /api/companion/packs |
| scope.new_type_contract | yes | ws-companion-marketplace/v1 manifest, .ws-install.json sidecar, catalog origin object |
| scope.test_surface | new-files | new hostile-zip, fetcher and guide drift tests; existing companion_pack_store_test.go, companion_pack_routes_test.go and companion_pack_schema_test.go are extended |
| complexity.reuse_points | confirmed | loadCompanionPack, validateCompanionManifest, validCompanionRaster, reload and TrustedWrite read at companion_pack_store.go#L257-L306, companion_pack_schema.go#L21-L23, companion_pack_routes.go#L140-L148 |
| complexity.side_effect_risk | high | writes, renames and deletes pack directories under the user config home, and makes outbound network fetches |
| risk.correctness | high | zip safety, atomic swap, staging cleanup and update detection each have hostile or crash cases |
| risk.fit | moderate | Phase 2 needs the director gear modal and the catalog surface field (pending 261122-feat-ws-companion-director) |
| risk.test | high | many hostile-archive cases, TLS fetcher cases, and file-operation measurements on Windows-sensitive paths |
| risk.security_or_contract | high | untrusted archives and URLs, SSRF dial guard, and a new external marketplace schema contract |

## Phases

### Phase 1: Zip pipeline, installer, local import and validate endpoints

- **Goals:**
  1. The in-memory zip pipeline of Decisions 1, 3, and 4: marketplace
     parsing, name safety, pack-level rules, caps, image header check,
     validation through `loadCompanionPack`, staging write, atomic rename,
     reload, and leftover-staging cleanup.
  2. The install sidecar (Decision 7) and the catalog `origin` field, read
     during a scan.
  3. `TrustedWrite` routes on the companion family: local import (zip
     body, at most 64 MiB, with a chosen pack ID list), validate (dry run,
     no write), and uninstall (sidecar directories only). The routes are
     stateless: the browser sends the zip again on confirm, and the daemon
     never stores the uploaded zip on disk.
- **Deferred:** network sources, UI, guide.
- **Verification:**
  - Go tests with generated hostile zips. Each case must be rejected:
    traversal, absolute paths, backslashes, drive letters, a non-clean
    name, duplicate names, case collisions, a symlink entry, a nested
    directory, an unknown file, an unreferenced raster, SVG, an
    extension/format mismatch, oversized dimensions, entry-count and
    per-file, per-pack, and total caps, the ratio bomb, an encrypted
    entry, a bad marketplace schema, an ID mismatch, a reserved ID, a
    duplicate ID, a Windows reserved device name as an ID or file
    name, and a pack over the remaining total cache budget.
  - Acceptance cases: a self-contained bundle, a single top-level
    directory archive with directory entries (as a host archive lists
    them), ignored extra repository files, update over a sidecar
    directory, refusal over a hand-made directory, the local-pack cap,
    and concurrent installs of one ID serialized.
  - Crash safety: a leftover staging directory produces no catalog error
    and is cleaned up at the next install.
  - The `CompanionPack` suite passes with `-race`, and
    `GOOS=windows go -C agents-plugin-tool vet ./internal/wsdashboard`
    passes.
  - File-operation counts per install, per uninstall, and per warm
    request are reported (the warm count stays zero).

### Result (ba9c4981c) - 2026-11-22

Landed in 0bd0a5c01 (zip pipeline, installer, sidecar, catalog `origin`),
2577758a5 (validate, import and uninstall routes) and b6aa9ddb3 (a dry run
lists external `url` entries without downloading them).

- `companion_pack_zip.go` opens the zip in memory and applies the
  archive-level name rules, the entry, byte and ratio caps, and the
  pack-level rules. `prepare` validates each pack through
  `loadCompanionPack`. The pack's referenced asset set is the allow-list,
  and the name pre-filter calls the store's own `validCompanionRaster`, so
  no file list is copied. If the paper-doll sibling adds a file kind that is
  not a `.png`/`.webp` raster, it needs one line in `pack()` and in
  `companionCheckRaster` at integration.
- `companion_pack_install.go` stages each run under
  `<config-home>/.companion-staging/run-*`, which is outside `companions/`.
  It writes the pack and the `.ws-install.json` sidecar, then swaps the
  directory with renames and reloads. `installMu` is taken before `scanMu`,
  and `swapAndLoad` holds `scanMu` once for both renames and the load. The
  next install deletes any leftover staging runs. The scan reads the sidecar
  into the optional catalog `origin` member, and a malformed sidecar is
  ignored.
- Routes: `POST /api/companion/packs/validate`, `/import?name=&id=` (body at
  most 64 MiB, never written to disk), and `/{id}/uninstall` (sidecar
  directories only). All three are `TrustedWrite`.
- Tests:
  - `TestCompanionPackInstallRejectsHostileArchives` covers every hostile
    case listed above. Each case is pinned to its own rejection message, in
    both the dry run and the install. The cases are traversal, absolute,
    backslash, drive letter, non-clean, duplicate, case collision, symlink,
    nested directory, unknown file, unreferenced raster, SVG,
    extension/format mismatch, oversized dimensions, entry-count, file,
    pack and total caps, ratio bomb, encrypted, schema, ID mismatch,
    reserved ID, duplicate ID, and device names.
  - `TestCompanionPackInstallOperationAndCacheBudgets` covers the remaining
    total cache budget.
  - `Acceptance` covers the self-contained bundle, the single top-level
    directory with directory entries, ignored repository files, update over
    a sidecar directory, refusal over a hand-made directory, the local-pack
    cap, and serialized concurrent installs of one ID.
  - `CrashSafety`, `Uninstall`, `SidecarOrigin`, `RasterHeaders`,
    `ExternalEntry`, and the route tests (`TestCompanionPackRoutesRefuseUntrusted`,
    `ValidateRoute`, `ImportRoute`, `ImportRouteRejects`, `UninstallRoute`).
- File operations, measured by `TestCompanionPackInstallFileOperations`:
  - Install of one pack with one raster: create 3, write 3, mkdir 4,
    lstat 2, readdir 1, rename 1, remove 1, plus 5 scan opens.
  - Uninstall: lstat 3, mkdir 1, readdir 1, rename 1, remove 1, plus 1 scan
    open.
  - Warm request: 0.
- Verification: the `CompanionPack` suite with `-race` and
  `GOOS=windows go vet ./internal/wsdashboard` pass.
- Deviation: a dry run (validate, or a source listing) never downloads a
  `url` entry. It lists the entry as new or installed, or as update when the
  entry's `sha256` differs from the sidecar's `archiveSha256`. Only the
  install click downloads it. The guide documents this.
- Review fix (ba9c4981c):
  - The WebP VP8X canvas size now reads its three-byte fields with correct
    precedence. A 512-pixel-wide canvas used to read as 256, though the
    1..4096 verdict was unaffected.
  - The reserved device-name errors no longer name an operating system.

### Phase 2: Git and HTTPS sources, official source, modal Sources UI

- **Prerequisite:** director Phase 1 must have a Result: the gear modal
  and the catalog `surface` field (frontmatter `blocked-by`).
- **Goals:**
  1. The daemon fetcher of Decision 8, smart-HTTP ref resolution, the
     archive URL patterns, external pack `url` + `sha256`, and the source
     kind detection of Decision 2.
  2. The `dashboard.companion_sources` key per Decision 7, across the
     manifests, the generator, global-only registration, the catalog row,
     and all locale tables.
  3. The embedded official source of Decision 10.
  4. Source fetch (returns the listing and keeps the downloaded zip in a
     one-entry in-memory cache, at most 64 MiB, expiring after 10 min, so
     an install needs no second download; on a cache miss, expired or
     replaced by another source's fetch, the install click downloads
     again, which is still explicit user action), install, check for
     updates, and update routes.
  5. A modal Sources section:
     - the official source and user sources, with add and remove;
     - pack cards showing name, author, license, version, origin, and an
       installed or update-available badge;
     - install, update, and uninstall buttons;
     - local zip import with a validate preview;
     - the warnings of Decision 6.
- **Verification:**
  - Fetcher tests use an `httptest` TLS server for: an http redirect
    refused, more than 5 redirects refused, the size cap, the timeout, a
    loopback dial refused, a sha256 mismatch, a non-zip response hint,
    ref advertisement parsing, and archive pattern fallback.
  - Config, catalog, and i18n drift tests per `config-settings-ui.md`.
  - Frontend tests: list editing, warning shown for a non-official
    source, `textContent` rendering, and the import preview flow.
  - A headless Chromium capture of the Sources section.
  - Proof that no fetch happens at startup or while the companion is
    disabled.

### Result (1537de040) - 2026-11-22

Landed in:

- 8012e1a56: the `dashboard.companion_sources` key.
- a6a4a94aa: `internal/wsnetguard`.
- 526540323: the fetcher.
- e701246bc: the modal Sources section.
- fe34eb478: the source routes and the official source.
- f9fbc5903: the file-operation counts.
- afc6a4455 and 47e1796f3: the Chromium capture.
- 1537de040: the review fix, source verdicts from the daemon.

Details:

- **Dial guard.** `wsnetguard.Control(refuse)` is a `net.Dialer.Control` hook whose
  refused-range policy is a parameter. `RefuseLocal` (loopback, link-local,
  unspecified, multicast, IPv4-mapped forms included) is the companion
  policy. `RefuseLocalAndPrivate` is ready for the web-tools fetch.
- **Fetcher (`companion_pack_fetch.go`).**
  - `parseCompanionSource` detects the kind. A path ending in `.zip` is a
    zip source; a query is allowed and a fragment refused. Anything else is
    git, where the fragment names the ref and a query is refused.
  - A git source reads `info/refs?service=git-upload-pack` (protocol v0
    pkt-lines, at most 1 MiB). It resolves HEAD, a branch, a tag (peeled
    first) or a 40-hex ref, then tries two archive URL patterns for the
    pinned commit.
  - Caps: 64 MiB per source zip and 16 MiB per external pack. The
    external pack's `sha256` is checked before the zip opens.
  - Downloads are HTTPS GET only, with a 60 s total timeout per call, no
    cookie jar, a generic User-Agent, and the Referer dropped.
  - Redirects are followed at most 5 times, only to `https:` URLs without
    credentials.
  - `companionRoundTripper` sends each hop, redirect hops included, through
    one of two transports: the environment proxy when one applies, or the
    guarded direct transport. The transport is written out rather than
    cloned from `http.DefaultTransport`, so a hook set there cannot bypass
    the guard.
- **Config key.** `dashboard.companion_sources` is in all three manifests,
  is global-only, has a `companion-modal` catalog row, and has labels in
  all 11 locale tables. `parseCompanionSources` judges each entry with the
  fetcher's own rules. An invalid or duplicate entry carries an error and
  is skipped. At most 16 valid entries are used.
  - The companion config JSON carries these verdicts as `sourceEntries`.
    `CompanionConfig.MarshalJSON` computes them at encode time, so the
    struct stays comparable.
  - The modal shows the daemon's verdicts rather than its own prefix
    check. An entry the routes refuse (a git URL with a query, a zip URL
    with a fragment, an overlong ref, an unparsable URL) never looks usable
    and takes no slot.
  - A source listed only as an invalid entry answers 400 with the rule it
    breaks (review fix 1537de040).
- **Official source.** `assets/companion/official/marketplace.json` is
  embedded and currently lists no url packs. The source routes prepend the
  built-in `default` card in code (status `builtin`, no uninstall), because
  the marketplace parser reserves the `default` ID. Only this source
  produces `official: true`. A user manifest naming itself official stays
  non-official, as the test checks.
- **Routes.**
  - `POST /api/companion/sources/fetch` lists a source as a dry run and
    keeps its zip in a one-entry cache (10 min TTL, released by a timer).
    `/install` installs chosen IDs from the cached zip, or downloads again
    on a miss.
  - Checking for updates is a fetch: a listing marks a pack `update` per
    Decision 5. Updating is an install over the sidecar directory, so no
    separate update route exists.
- **Sources UI.** `dashboard-companion-sources.js` renders:
  - the official and user sources, with add and remove through
    `POST /api/config/set {scope:"global"}`;
  - pack cards with name, author, license, version, origin and a badge;
  - install, update and uninstall buttons;
  - a local zip import with a validate preview;
  - the third-party warning in i18n text;
  - the authoring links.

  All pack text is set through `textContent`, and only `https:` links are
  rendered as links.
- **No fetch at startup or while the companion is disabled.**
  - The only fetch call sites are the source route plan and the import
    route's external hook. Both run inside a request handler.
  - `TestCompanionPackSourceRoutesGate` asserts the TLS source server saw
    zero requests. It covers fetch and install answering 409 while the
    companion is disabled, an import of an external entry refused while
    disabled, unlisted sources answering 400, and the catalog and reload.
- **File operations (`f9fbc5903`).**
  - Cold source listing: readdir 1 plus 1 scan open.
  - Source install from the cached zip: the same as a local import.
  - Warm request: 0.
- **Tests.**
  - Fetcher: `TestCompanionPackFetch*` covers source parsing, ref parsing,
    git archive fallback, zip source, pack sha256 mismatch, the http
    redirect and more-than-5 redirect refusals, the caps, the timeout, a
    loopback dial refused, the proxy path, and the non-zip hint.
  - `TestGuard*` covers the guard.
  - Routes: `TestCompanionPackSourceRoutes{Gate,Zip,Official}`.
  - Config drift: the mcp config, catalog and settings tests, plus
    `wsi18n` and `wspackage`.
  - `TestCompanionConfigSourceEntriesJSON` covers the verdicts in the
    config JSON.
  - Node: `frontend-companion-sources.test.js` plus the companion and
    settings tests, 47 in total, including the verdict path and its
    local fallback.
  - The headless Chromium capture is `TestCompanionSourcesBrowser`. It
    shows the official listing, a local import preview of a generated zip
    whose names contain `<b>`/`<i>` rendered as text, import, uninstall,
    and the three authoring links answering 200. It aborts every request
    to any origin other than the daemon.
- **Deviations from the plan.**
  - Fetch and install act only on the official source or a valid entry of
    the configured list. Any other source answers 400.
  - The source routes answer 409 while the companion is disabled.
  - The import route's external hook refuses while the companion is
    disabled.

### Phase 3: Authoring guide, JSON Schemas, guide routes

- **Goals:**
  1. The embedded Markdown authoring guide covers:
     - the pack layout;
     - a minimal pack;
     - the marketplace manifest, both self-contained and with external
       URL + sha256;
     - hosting as a git repository or a zip;
     - the data-only rule and the caps;
     - how to validate.
  2. JSON Schemas for `pack.json` and `marketplace.json`, served with the
     guide. The modal links them.
  3. Update `companion-pet-rigging.md`'s BYO section to describe source
     install.
- **Verification:**
  - Drift tests:
    - the guide's example pack and example marketplace pass the real
      pipeline;
    - every required field in each schema matches what the validator
      requires;
    - the guide and schemas contain no ticket-stem pattern, `ai-docs/`,
      or repository-layout names (`shipped-surface-boundary.md`).
  - Route tests for the guide and the schemas, covering content type,
    `nosniff`, and auth.

### Result (ba9c4981c) - 2026-11-22

Landed in 2fc690180 (guide, schemas, routes and manual) and bf7047871 (the
guide's download limits pinned to the fetcher constants).

- **Shipped files.** These are embedded under `assets/companion/authoring/`:
  - `guide.md` covers the pack layout, a minimal pack, the marketplace
    manifest (self-contained and with external `url` + `sha256`), git and
    zip hosting, the data-only rule, a limits table, and how to validate
    (the import preview, the validate route, and the schemas). It also says
    a listing never downloads `url` packs.
  - `pack.schema.json` and `marketplace.schema.json`.
- **Routes.** `GET`/`HEAD /api/companion/authoring/{guide.md,
  pack.schema.json,marketplace.schema.json}` are read routes behind the
  dashboard's normal access handling. They send `text/markdown` or
  `application/schema+json`, `nosniff`, and `no-store`. The modal's Sources
  section links all three.
- **`companion-pet-rigging.md`.** The BYO section now describes source
  install and local import. It also gained a `pack.json` field reference,
  which the manual consistency test requires.
- **Drift tests (`companion_pack_guide_test.go`).**
  - `TestCompanionAuthoringExamplesPassPipeline`: the guide's
    `json example:<name>` blocks pass the real zip pipeline.
  - `ExamplesMatchSchemas`.
  - `PackSchemaMatchesValidator` and `MarketplaceSchemaMatchesParser`:
    required fields and refusals checked against the validator and parser.
  - `SchemaConstantsMatchValidators`.
  - `GuideCaps`: the limits table is checked against the Go constants,
    including `companionSourceCap`, `companionPackZipCap`,
    `companionFetchTimeout` and `companionRedirectMax`.
  - `KitStandsAlone`: no ticket stem, `ai-docs/`, repository path, Go
    identifier, commit hash, or vendor/character name. Every URL is
    `example.invalid` or the schema vocabulary.
  - `SchemaMatchesManual`.
  - `TestCompanionAuthoringRoutes`: content type, `nosniff`, method, and
    auth.
- **No real names.** The guide and schemas name no third-party character,
  model, product, or company. Review fix ba9c4981c removed an operating
  system name from the reserved device-name rule. The rule now lists the
  names and gives the reason.
