---
title: "컴패니언 내장 기본 팩 및 로컬 BYO 팩 스토어·HTTP 서빙 (메모리 우선 · Windows AV 최소화)"
parent: 261122-epic-werkschaft-lead-companion
related:
  261121-research-werkschaft-lead-companion-rig: v3.1 pack schema and IP boundary decisions
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 986c9295fe7d1f82
sage-review-completeness-reviewed: 986c9295fe7d1f82
---

# Companion Embedded Default Pack & Local BYO Pack Store (Memory-First HTTP Serving)

## Background

The Werkschaft Lead Companion decouples its Canvas engine from character artwork via a uniform `pack.json` + WebP bundle (`ai-docs/manuals/companion-pet-rigging.md`). The daemon needs a safe, cross-platform, Windows-antivirus-friendly pack registry and HTTP serving surface that provides:
1. An IP-clean embedded default pack (`default`) shipped inside `agents-plugin-tool` so the feature works out of the box once opted in (initially backed by the v3.1 technical fixture, drop-in replaceable by `261122-chore-ws-companion-official-art-pack`).
2. User-authored or community BYO packs loaded locally from `<config-home>/companions/<pack-id>/pack.json` — strictly outside the public repository.

## Decisions

- **Canonical BYO pack root (`<config-home>/companions/`):** Resolve the user pack directory alongside `wsconfig.GlobalPath` (`opts.ConfigHome > WS_CONFIG_HOME > ~/.ws/companions/`), NOT under `wsstate`'s ephemeral daemon cache root (`~/.cache/ws@...`). Rationale: user-created character packs are durable user configuration/assets that must survive cache purges and respect `WS_CONFIG_HOME` in tests and portable setups.
- **Storage Type, Lazy First-Use Scan, Atomic Reload & Cache/Memory Bounds (`performance.md`):**
  - Classify pack manifests and WebP/PNG assets as **Storage Type 5 (Opaque blobs / plain files)** read by both users and the daemon.
  - **Initial scan trigger (unified across Epic, Manual, and Pack Store):** Scan `<config-home>/companions/` **once lazily on the first `/api/companion/packs*` request** (never eagerly at daemon startup, avoiding Windows AV startup scans for the `off`-by-default majority and avoiding boot-time coupling to Child 1's config), and rescan only on an explicit `TrustedWrite` `POST /api/companion/packs/reload` action. *Rejected alternative:* eager scan at daemon startup.
  - Store the active catalog, content-hash revision tags, validated raw `pack.json` bytes, and asset bytes in an immutable snapshot swapped via `atomic.Pointer` on reload so concurrent HTTP readers never block or see torn state; superseded snapshots are garbage-collected after in-flight responses finish (no multi-generation retention).
  - **Revision, Cache-Control & Reload Recovery Contract:**
    - When `?rev=<current-rev>` matches the active pack revision, respond with `200` (or `304 Not Modified` on matching `If-None-Match`), strong `ETag: "<current-rev>"`, and `Cache-Control: private, max-age=31536000, immutable`.
    - When an explicit `?rev=<stale-or-unknown>` does **not** match the active pack revision (e.g., a reload occurred between fetching `pack.json` and an uncached asset layer), return **`409 Conflict`** with `X-Companion-Current-Rev: <current-rev>` and `Cache-Control: private, no-store` (or `404` if the pack/file was removed) so consumers never mix revision A manifests with revision B layers and instead re-fetch the catalog/manifest at the fresh revision.
    - When `?rev=` is omitted, respond with the current bytes, strong `ETag: "<current-rev>"`, and `Cache-Control: private, no-cache`.
    - **Warm-read I/O measurement boundary:** Steady-state warm reads perform **zero pack-store filesystem calls** (`stat`/`open`/`readdir` under `<config-home>/companions/`). Existing dashboard auth middleware (`localhost_auth.go:56-78`, `access.go:67-113`, `wsstate/read_cache.go:26-40`) performs its own credential-file `Stat` on requests; verify zero pack-store I/O and report auth middleware I/O separately.
  - **Explicit memory & pack-count caps (embedded `default` always loads):**
    - Max local BYO packs: `32` (scanned in ascending lexicographical directory order; excess directories are skipped and reported in the catalog's `errors[]`).
    - Max `pack.json` size: `512 KiB`; max single asset file size: `2 MiB`; max per-pack total bytes (manifest + all referenced assets): `16 MiB`; max total in-memory BYO byte cache across all local packs: `64 MiB`.
    - Exceeding any per-file, per-pack, or the `64 MiB` total cache cap rejects that pack with a structured entry in `errors[]` without evicting earlier loaded packs or affecting `default`.
- **Catalog & `autoMatch` HTTP Contract (`GET /api/companion/packs[?harness=<h>&model=<m>]`):**
  - Response JSON shape:
    ```json
    {
      "packs": [{ "id": "default", "name": "...", "schema": "ws-companion/v1", "source": "embedded|local", "rev": "<sha256-prefix>", "presets": ["..."], "autoMatch": { "harnesses": ["..."], "models": ["..."] } }],
      "errors": [{ "id": "<dir-name>", "path": "<rel-path>", "message": "..." }],
      "resolvedAuto": { "packId": "default", "matchedBy": "model|harness|default" }
    }
    ```
  - **Visible-session `auto` resolution:** Each outer shell supplies its currently visible session's `(harness, model)` via optional `?harness=&model=` query parameters (re-resolving on session switch or after reload; the global `dashboard.companion_pack` setting stays `"auto"`). On sessionless routes (dashboard root, `/settings`) or when both query parameters are absent/empty, `resolvedAuto` deterministically returns `{ "packId": "default", "matchedBy": "default" }`.
  - **Deterministic scoring:** Optional `autoMatch: { harnesses?: string[], models?: string[] }` (bounded ASCII string arrays). Score valid local packs (`+2` for case-insensitive `models` substring match, `+1` for case-insensitive exact `harnesses` match; `0` = non-match), break ties by ascending lexicographical `pack.id`, and fall back to embedded `"default"` on no match. *Rejected alternative:* regex/glob matching (ReDoS/complexity risk) or filesystem-order tie-breaking.
- **Extensible, Safety-Only `ws-companion/v1` Pack Schema Validation & Graceful Degradation:**
  - Conform strictly to the canonical `ws-companion/v1` single-file `pack.json` contract (`ai-docs/manuals/companion-pet-rigging.md`): one `pack.json` per pack (no separate user-global `scenarios.json` in v1, preserving minimal file touches on Windows AV). Require `"schema": "ws-companion/v1"` exclusively across both the embedded `default` fixture and local BYO packs (no legacy schema split).
  - Validate **safety and structural invariants only** — never enforce closed name whitelists on `poses`, `gestures`, `expressions`, `props`, `fx`, `motions`, or `scenarios`, and **do not reject unknown extension JSON fields** (avoid `DisallowUnknownFields`; serve raw validated JSON bytes so unknown extension fields pass through untouched and future additions remain T0).
  - Required structural checks: `"schema": "ws-companion/v1"`, valid `id` matching directory name, required `poses.stand` with at least one layer, bounded `canvas` dimensions (`64..2048`), smart mode inference (`file` -> `static`, `line`/`jacketFill`/`accentFill`/`skinFill`/`detail` -> `semantic-split`, `frames` -> frame loop with bounded `fps` `0.1..30`), valid `motions` / layer `motion` shapes (string ref, `{ use, phase? }`, `{ oscillate }`, `{ keyframes, loop? }`, plus legacy shorthand aliases), safe declarative `when` guard keys (`streak`, `windowMs`, `idleMs`, `hourRange`, `routeKind`, `pose`), and every referenced asset path across `head`, `hairs`, `poses`, `gestures`, `expressions`, `props`, and `fx` passes strict filename/existence/size checks.
  - Minimal BYO packs need only `schema`, `id`, `name`, `canvas`, and `poses.stand.layers: [{ "file": "stand.webp" }]` (the manual's formatted example spans 9 lines: `ai-docs/manuals/companion-pet-rigging.md#L103-L113`); missing optional poses, gestures, expressions, or props fall back cleanly via `fallback` chains to `stand` and engine-shared defaults.
  - Malformed user packs record a structured validation error in `errors[]` without crashing the daemon or breaking other packs.
- **Security, Reserved IDs & Access Controls (Raster-Only Assets):**
  - **Reserved selector IDs (`default`, `auto`):** Reject any local BYO directory named `"default"` or `"auto"`, exclude it from `packs[]`, and record a structured validation error in `errors[]` so local directories can never shadow the embedded `default` pack or collide with the `auto` selector. *Rejected alternative:* allowing local `default/` to shadow embedded `default`.
  - Validate `<pack-id>` against `^[a-z0-9][a-z0-9_-]{0,63}$` (excluding reserved `"default"` and `"auto"` for local packs) and `<file>` against strict relative **raster-only** filenames (`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}\.(webp|png)$` with zero `..`, path separators, or null bytes). **Reject `.svg` pack assets in v1** to eliminate same-origin active-SVG `<script>` XSS without maintaining an SVG sanitizer. *Rejected alternative:* serving `image/svg+xml` from the dashboard origin.
  - Reject symlinks and non-regular files on disk scans (`os.Lstat` / `EvalSymlinks` boundary check within pack root).
  - Guard read routes with the standard dashboard auth/cookie middleware (`access.go`) and reload route with `TrustedWrite` (local + same-origin); set `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'none'; sandbox`, and exact raster `Content-Type` (`image/webp` or `image/png`) headers on asset responses.
- **Compatibility Tier Verdict — T0 (transparent):** Adds only new HTTP routes (`/api/companion/packs`, `/api/companion/packs/{id}/pack.json`, `/api/companion/packs/{id}/assets/{file}`, `POST /api/companion/packs/reload`) consumed strictly by the matching dashboard frontend bundled in the same daemon binary. No existing MCP tool, `/mcp` hello, or shared on-disk store changes; older clients never call these routes, and new binaries ignore unknown extension keys in `pack.json`.

## Constraints

Required manuals (per `AGENTS.md` `### Implementation Conventions`):
- `ai-docs/manuals/performance.md` — zero startup disk scan (lazy first-request scan only); zero pack-store `stat`/`open`/`readdir` on steady-state warm hits (report existing auth middleware credential-file `Stat` separately); report measured file-operation counts on cold load vs. warm hit.
- `ai-docs/manuals/release-compatibility.md` — additive HTTP routes only (T0).
- `ai-docs/manuals/shipped-surface-boundary.md` — embedded default pack and error messages must be IP-clean and downstream-neutral.
- `ai-docs/manuals/companion-pet-rigging.md` — conform strictly to the v3.1 `pack.json` schema and engine-shared prop contract.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

## Prior Art

- In-memory ETag/304 pattern: `agents-plugin-tool/internal/wsdashboard/tracker_answers.go#L168-L183` and `agents-plugin-tool/internal/wsdashboard/tracker_answers.go#L231-L241` (uses `no-cache`, not revision-keyed immutable caching).
- Safe local file validation & security headers: search `attachment_route.go` and `access.go` (`TrustedWrite`) in `agents-plugin-tool/internal/wsdashboard/`.
- Config home resolution: search `GlobalPath` in `agents-plugin-tool/internal/wsconfig/global.go`.
- Route registration & embedded assets: search `daemon_http.go` and `pages.go` in `agents-plugin-tool/internal/wsdashboard/`.

## Prior Decisions

- 261122-chore-ws-companion-official-art-pack (2026-11-22, Decisions): "Art-engine decoupling & incremental extensibility: Replace only the embedded `default` pack assets and `pack.json`" — bearing: constrains
- 261122-epic-werkschaft-lead-companion (2026-11-22, Cross-Child Decisions): "Rigging contract authority: `ai-docs/manuals/companion-pet-rigging.md` is the canonical v3.1 Doll-Maker schema and craft-gate reference across all children." — bearing: constrains
- 261122-feat-ws-companion-shell-engine (2026-11-22, Decisions): "zero pack fetches, zero `<canvas>` allocation, zero `requestAnimationFrame` loop, and zero iframe relay overhead." — bearing: supports
- 261121-research-werkschaft-lead-companion-rig (2026-11-22, commit): "Captured app-scoped persistent outer-shell mount, default left session rail bottom anchor, verified T0 scalar overrides compatibility against v0.50.8, and memory-first BYO pack storage under <config-home>/companions/." — bearing: supports
- 261122-feat-ws-companion-config-settings (2026-11-22, Decisions): "`dashboard.companion_mode`: enum `off | visual | full`, default `"off"` (in both `ws` and `wsflow`)." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | Dashboard HTTP registration, embedded fixture, registry and tests under agents-plugin-tool/internal/wsdashboard/; config resolution in agents-plugin-tool/internal/wsconfig/global.go. |
| scope.surface | public-interface | Adds dashboard HTTP catalog, manifest, asset and reload routes, and pack schema. |
| scope.new_public_symbol | yes | CompanionPackStore is named in Phase 1. |
| scope.new_type_contract | yes | ws-companion/v1 pack JSON and catalog, asset and reload HTTP responses. |
| scope.test_surface | new-files | Phase 1 calls for unit and HTTP coverage of a new CompanionPack implementation. |
| complexity.reuse_points | confirmed | wsconfig.GlobalPath, dashboard TrustedWrite, tracker ETag helper and pages.go embedding exist. |
| complexity.side_effect_risk | high | Reload scans user-controlled files and replaces cached catalog state while HTTP requests run. |
| risk.correctness | high | Validation, symlink safety, revision caching, reload and cross-platform paths interact. |
| risk.fit | high | New schema and routes must match companion compositor and settings children and the canonical rigging manual. |
| risk.test | high | Security, reload races, cache behavior and zero-I/O guarantees require meaningful cross-platform coverage. |
| risk.security_or_contract | high | Authenticated routes serve user-controlled image and JSON content and must enforce path and size boundaries. |

## Phases

### Phase 1: Pack Schema, Memory-First Registry, HTTP Routes & Embedded Default Fixture

- **Goals:**
  1. Define Go types and a safety-only validator for the `ws-companion/v1` `pack.json` schema (`ai-docs/manuals/companion-pet-rigging.md`): open `map[string]` dictionaries for `poses` (`stand` required, `layers[]`, optional `fallback`), `gestures` (`pose`, `replaces[]`, `layers[]`), `expressions` (`decal` vs Track 1 parametric fields), `props`, `fx`, `motions` (reusable `{ oscillate }` / `{ keyframes }`), `hairs`, `presets`, and declarative `scenarios` (1-beat shorthand + `steps[]` + declarative `when` guards + `disabled`), plus smart layer-mode inference and optional `autoMatch` rules. Serve validated raw JSON bytes so unknown extension fields pass through intact (T0).
  2. Implement a lazy, memory-first `CompanionPackStore` that resolves `<config-home>/companions/` via `wsconfig`, merges the embedded `default` pack (an IP-clean minimal `ws-companion/v1` technical fixture + engine-shared props `drone`, `mail`, `grab`, `prop-typing-desk`) with local BYO packs, computes content-hash revisions, and serves bytes from memory on steady-state hits.
  3. Wire HTTP routes on the dashboard daemon:
     - `GET /api/companion/packs[?harness=<h>&model=<m>]` (catalog JSON `{ packs, errors, resolvedAuto }`, resolving `auto` from visible-session query params with `"default"` fallback on absent params / sessionless routes)
     - `GET /api/companion/packs/{id}/pack.json` (revision-tagged JSON with `ETag` / `304`, `409 Conflict` + `X-Companion-Current-Rev` on stale `?rev=`, preserving valid extension fields)
     - `GET /api/companion/packs/{id}/assets/{file}` (revision-tagged raster image bytes with `ETag` / `304`, `409 Conflict` + `X-Companion-Current-Rev` on stale `?rev=`, `nosniff`, CSP sandbox, strict raster-only filename/symlink guards)
     - `POST /api/companion/packs/reload` (`TrustedWrite` guard; atomically rescans `<config-home>/companions/` and swaps snapshot + revision tags)
  4. Write unit & HTTP tests covering minimal BYO pack acceptance (the manual's formatted example spans 9 lines: `ai-docs/manuals/companion-pet-rigging.md#L103-L113`), reserved `default`/`auto` local directory rejection, `.svg`/path-traversal/symlink/oversized-file/64 MiB-total-cap rejection, invalid `pack.json` isolation, custom pose/gesture/expression/prop/fx/motion/multi-step-scenario acceptance, unknown extension-field passthrough, `auto` harness/model scoring + tie-break + no-session fallback, `ETag`/`304` hits, concurrent reload between manifest fetch and uncached asset fetch (`409 Conflict` + `X-Companion-Current-Rev`), and zero pack-store filesystem calls after warm load (measured separately from auth middleware).
- **Deferred scope:** Final human-approved official Style D D1 art pack (`261122-chore-ws-companion-official-art-pack`) and browser Canvas rendering (`261122-feat-ws-companion-shell-engine`).
- **Verification boundary:**
  - `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go -C agents-plugin-tool test ./internal/wsdashboard -run 'CompanionPack'`
  - Confirm `GOOS=windows go -C agents-plugin-tool vet ./internal/wsdashboard` passes with no platform-specific path bugs.
