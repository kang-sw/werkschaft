---
title: "컴패니언 팩 사용자 소스(마켓플레이스) 설치: git/zip 소스, 안전한 압축 해제, 제작 가이드"
parent: 261122-epic-werkschaft-lead-companion
blocked-by: 261122-feat-ws-companion-director#phase1
related:
  261122-feat-ws-companion-pack-store: installs into its <config-home>/companions/ root, reuses its validator and caps, and calls its reload
  261122-feat-ws-companion-paper-doll: may grow the pack schema; the install allow-list follows the validator's file list (sibling branch)
  261122-feat-ws-companion-director: Phase 2 UI lives in its gear modal and uses its catalog surface field; persona stays untrusted data per its Decision 7
  261122-chore-ws-companion-official-art-pack: the official source lists only IP-clean packs such as this one
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
     This works on any host and needs no host API. It then downloads a zip
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
   - **Pack-level entry rules:** a pack directory holds at most 256 entries.
     Symlinks (Unix mode bits in the external attributes) and other
     non-regular entries are rejected, as are entries in nested
     subdirectories. Names that differ only by case are rejected, because
     macOS and Windows file systems ignore case. Every file name must be
     `pack.json` or pass `validCompanionRaster` (`companion_pack_schema.go:21`).
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
   - **Reload:** the pipeline calls the store's existing `reload`
     (`companion_pack_store.go:105-109`) and returns the new catalog.
   - **Guards:** an install refuses an `id` whose directory exists without
     an install sidecar, because that is a hand-made pack that only the
     user may replace. It also refuses an install that would push local
     packs past `companionMaxPacks`. Uninstall deletes only directories
     that carry a sidecar.

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
     (`performance.md` Rules 1 and 6; the Windows AV file-touch cost).
   - An update is offered when the source's resolved commit, archive
     sha256, or pack `sha256` differs from the sidecar. The `version`
     string is display-only. Rejected alternative: semver comparison. It
     trusts an author-controlled string and misses re-uploads that keep
     the same version.

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
     ranges stay allowed for home NAS hosting.
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
   - A validate endpoint (`POST /api/companion/packs/validate`, the same
     pipeline with no write) returns per-pack errors. The modal's import
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
      built-in and installed. Later official packs are added to it by a
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
    entry, a bad marketplace schema, an ID mismatch, a reserved ID, and
    a duplicate ID.
  - Acceptance cases: a self-contained bundle, a single top-level
    directory archive, ignored extra repository files, update over a
    sidecar directory, refusal over a hand-made directory, and the
    local-pack cap.
  - Crash safety: a leftover staging directory produces no catalog error
    and is cleaned up at the next install.
  - The `CompanionPack` suite passes with `-race`, and
    `GOOS=windows go -C agents-plugin-tool vet ./internal/wsdashboard`
    passes.
  - File-operation counts per install, per uninstall, and per warm
    request are reported (the warm count stays zero).

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
     an install needs no second download), install, check for updates,
     and update routes.
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
