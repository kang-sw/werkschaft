---
title: "컴패니언 내장 기본 팩 및 로컬 BYO 팩 스토어·HTTP 서빙 (메모리 우선 · Windows AV 최소화)"
parent: 261122-epic-werkschaft-lead-companion
related:
  261121-research-werkschaft-lead-companion-rig: v3.1 pack schema and IP boundary decisions
---

# Companion Embedded Default Pack & Local BYO Pack Store (Memory-First HTTP Serving)

## Background

The Werkschaft Lead Companion decouples its Canvas engine from character artwork via a uniform `pack.json` + WebP bundle (`ai-docs/manuals/companion-pet-rigging.md`). The daemon needs a safe, cross-platform, Windows-antivirus-friendly pack registry and HTTP serving surface that provides:
1. An IP-clean embedded default pack (`default`) shipped inside `agents-plugin-tool` so the feature works out of the box once opted in (initially backed by the v3.1 technical fixture, drop-in replaceable by `261122-chore-ws-companion-official-art-pack`).
2. User-authored or community BYO packs loaded locally from `<config-home>/companions/<pack-id>/pack.json` — strictly outside the public repository.

## Decisions

- **Canonical BYO pack root (`<config-home>/companions/`):** Resolve the user pack directory alongside `wsconfig.GlobalPath` (`opts.ConfigHome > WS_CONFIG_HOME > ~/.ws/companions/`), NOT under `wsstate`'s ephemeral daemon cache root (`~/.cache/ws@...`). Rationale: user-created character packs are durable user configuration/assets that must survive cache purges and respect `WS_CONFIG_HOME` in tests and portable setups.
- **Storage Type & Zero-Per-Request-I/O Discipline (`performance.md`):**
  - Classify pack manifests and WebP assets as **Storage Type 5 (Opaque blobs / plain files)** read by both users and the daemon.
  - Scan `<config-home>/companions/` **once at daemon startup** (lazily on first companion request if companion mode starts `off`, so `off` mode incurs zero startup file touches!) and only rescan on an explicit `TrustedWrite` `POST /api/companion/packs/reload` action.
  - Cache validated pack metadata, content-hash revision tags, and small WebP payloads (or immutable revision-keyed byte cache bounded by a sane per-pack/total cap) in memory so `GET /api/companion/packs` and `GET /api/companion/packs/{id}/assets/{file}` return `200`/`304` with `ETag` and `Cache-Control: private, max-age=31536000, immutable` (when `?rev=` matches) performing **zero disk syscalls on steady-state reads**.
- **Strict v3.1 Pack Schema Validation & Graceful Degradation:**
  - Validate `pack.json` on load (required `stand` pose, bounded canvas dimensions, valid `bodyLayers[]` modes: `static`, `semantic-split`, `semantic-split-frames`, optional `pivot` + `motion` including `wave-pivot`, `bob-pivot`, `sway-pivot`).
  - Minimal BYO packs need only `stand` with a single `mode: "static"` layer; missing optional poses (`typing`, `perched`, `dangle`) or props fall back cleanly to `stand` and engine-shared props.
  - Malformed user packs record a structured validation error in the pack catalog without crashing the daemon or breaking other packs.
- **Security & Access Controls:**
  - Validate `<pack-id>` and `<file>` against strict allowlist grammars (`^[a-z0-9][a-z0-9_-]{0,63}$`, relative `.webp`/`.png`/`.svg` filenames with zero `..`, path separators, or null bytes).
  - Reject symlinks and non-regular files on disk scans; enforce per-file (`<= 2 MiB`) and per-pack (`<= 16 MiB`) size caps.
  - Guard read routes with the standard dashboard auth/cookie middleware (`access.go`) and reload route with `TrustedWrite` (local + same-origin); set `X-Content-Type-Options: nosniff` and safe `Content-Type` headers.
- **Compatibility Tier Verdict — T0 (transparent):** Adds new read-only HTTP routes (`/api/companion/packs`, `/api/companion/packs/{id}/pack.json`, `/api/companion/packs/{id}/assets/{file}`) and a new opt-in reload route (`POST /api/companion/packs/reload`). No existing MCP tool, `/mcp` hello, or shared store format changes; older clients never call these routes.

## Constraints

Required manuals (per `AGENTS.md` `### Implementation Conventions`):
- `ai-docs/manuals/performance.md` — zero startup disk scan while companion is unused/off; zero per-request `stat`/`open`/`readdir` on steady-state asset hits; report measured file-operation counts on cold load vs. warm hit.
- `ai-docs/manuals/release-compatibility.md` — additive HTTP routes only (T0).
- `ai-docs/manuals/shipped-surface-boundary.md` — embedded default pack and error messages must be IP-clean and downstream-neutral.
- `ai-docs/manuals/companion-pet-rigging.md` — conform strictly to the v3.1 `pack.json` schema and engine-shared prop contract.

## Prior Art

- Revision-keyed in-memory ETag/304 pattern: search `tracker_answers.go` in `agents-plugin-tool/internal/wsdashboard/`.
- Safe local file validation & security headers: search `attachment_route.go` and `access.go` (`TrustedWrite`) in `agents-plugin-tool/internal/wsdashboard/`.
- Config home resolution: search `GlobalPath` in `agents-plugin-tool/internal/wsconfig/global.go`.
- Route registration & embedded assets: search `daemon_http.go` and `pages.go` in `agents-plugin-tool/internal/wsdashboard/`.

## Phases

### Phase 1: Pack Schema, Memory-First Registry, HTTP Routes & Embedded Default Fixture

- **Goals:**
  1. Define Go types and validator for the v3.1 Doll-Maker `pack.json` schema (including `static`, `semantic-split`, `semantic-split-frames`, `pivot`, `motion` with `wave-pivot` / `bob-pivot` / `sway-pivot`, `hairs`, `faces`, `presets`, and optional `autoMatch` harness/model rules for `dashboard.companion_pack: auto`).
  2. Implement a lazy, memory-first `CompanionPackStore` that resolves `<config-home>/companions/` via `wsconfig`, merges the embedded `default` pack (an IP-clean minimal v3.1 technical fixture + engine-shared props `prop-drone`, `prop-mail`, `prop-grab`, `prop-typing-desk`) with local BYO packs, computes content-hash revisions, and serves bytes from memory on steady-state hits.
  3. Wire HTTP routes on the dashboard daemon:
     - `GET /api/companion/packs` (catalog of embedded + valid local packs + structured error list for invalid local packs, plus resolved `auto` match metadata)
     - `GET /api/companion/packs/{id}/pack.json` (revision-tagged JSON with `ETag` / `304`)
     - `GET /api/companion/packs/{id}/assets/{file}` (revision-tagged image bytes with `ETag` / `304`, `nosniff`, strict filename/symlink guards)
     - `POST /api/companion/packs/reload` (`TrustedWrite` guard; rescans `<config-home>/companions/` and refreshes revision tags)
  4. Write unit & HTTP tests covering path traversal rejection, symlink rejection, oversized file rejection, invalid `pack.json` isolation, `auto` harness matching, `ETag`/`304` hits, and zero disk re-reads after warm load.
- **Deferred scope:** Final human-approved official Style D D1 art pack (`261122-chore-ws-companion-official-art-pack`) and browser Canvas rendering (`261122-feat-ws-companion-shell-engine`).
- **Verification boundary:**
  - `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go -C agents-plugin-tool test ./internal/wsdashboard -run 'CompanionPack'`
  - Confirm `GOOS=windows go -C agents-plugin-tool vet ./internal/wsdashboard` passes with no platform-specific path bugs.
