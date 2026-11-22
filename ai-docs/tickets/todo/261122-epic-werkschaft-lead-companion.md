---
title: "Werkschaft 앱 스코프 리드 컴패니언(데스크톱 펫): T0 설정, BYO 팩 스토어, 셸 오버레이 엔진 및 공식 아트 팩"
sage-review-design: required
related:
  261110-epic-ws-dashboard-agent-host: host dashboard shell and session rail surface
  261121-research-werkschaft-lead-companion-rig: prototyping findings, v3.1 technical validation, and Outcome Ledger
---

# Werkschaft App-Scoped Lead Companion (Desktop Pet)

## Scope

Deliver the opt-in **Werkschaft Lead Companion (desktop pet)** feature as a modular, pack-driven visual companion mounted once on the persistent Werkschaft dashboard outer shell (`dashboard.tmpl` / `shell.js`), defaulting strictly to **`off`**:

- **T0 opt-in configuration & live settings UI:** Scalar `dashboard.companion_*` keys mirrored across `ws` and `wsflow`, inspectable in Dashboard Settings, and broadcast live to the shell without page reloads.
- **Memory-first embedded & BYO pack serving (`performance.md`):** Serve an IP-clean embedded default pack alongside user-supplied local packs under `<config-home>/companions/<pack-id>/pack.json` with zero per-frame or per-request disk I/O.
- **App-scoped shell overlay & v3.1 Doll-Maker Canvas engine:** Persist one companion instance across session switches, anchor by default on the left session rail bottom divider (above `.rws-unreg` / `.rfoot`) so dense session panes stay unobstructed, subscribe to a lightweight active-view context/landmark relay from the active session iframe, and animate 4 front-facing poses (`stand`, `typing`, `perched`, `dangle`) with 2-track hybrid expressions and engine-shared props (`prop-drone`, `prop-mail`, `prop-grab`, `prop-typing-desk`).
- **Optional slow-path narrative director & `ws/emotion`:** Pluggable sidecar director and lead-only `ws/emotion` tool kept strictly separate from the 0-token visual MVP.
- **Official ship art polish gate:** Decouple final art refinement from engine delivery so official shipped art passes explicit human visual sign-off against the v3.1 craft gates.

## Non-Scope

- Enabling the companion by default for any user or package (`ws` and `wsflow` both default `dashboard.companion_mode` to `off`).
- Side-view walking articulation (`W. Side-Walk`) or bare-skin knee/hip joint cuts in v1/v3.1.
- Shipping any third-party or community character assets inside the public repository.
- Modifying agent orchestration, ticket workflows, or session lifecycle semantics based on companion state.

## Child Tickets

Implementation order: Children 1 and 2 have no prerequisites and can run in parallel; Child 3 delivers the complete 0-token visual MVP once Children 1 and 2 land; Children 4 and 5 proceed independently after their prerequisites.

- `261122-feat-ws-companion-config-settings` — T0 `dashboard.companion_*` config keys, `ws`/`wsflow` manifest parity, settings UI catalog + `wsi18n`, and live shell broadcast. (No prerequisite.)
- `261122-feat-ws-companion-pack-store` — Embedded default dev-fixture pack + `<config-home>/companions/` BYO pack registry with startup/reload in-memory caching, ETag/304, and path/symlink security guards. (No prerequisite.)
- `261122-feat-ws-companion-shell-engine` — App-scoped persistent shell overlay, default left-rail-bottom home anchor, v3.1 Doll-Maker Canvas compositor (including continuous knee `sway-pivot`), 0-token local behavior engine, and active-session iframe context/landmark relay. (Blocked by `261122-feat-ws-companion-config-settings`, `261122-feat-ws-companion-pack-store`.)
- `261122-feat-ws-companion-director` — Optional delta-cached sidecar narrative director, pluggable lead-only `ws/emotion`, and `full`-mode persona prose guardrail. (Blocked by `261122-feat-ws-companion-shell-engine`; holds Open Decision Queue for budget and precedence knobs.)
- `261122-chore-ws-companion-official-art-pack` — Final Style D D1 official ship art pack replacing the technical dev fixture, satisfying uniform canonical `head-egg` pixel diameter, continuous `sway-pivot` perched legs, cohesive gesture arms, and explicit owner visual approval. (Blocked by `261122-feat-ws-companion-pack-store`.)

## Cross-Child Decisions

- **Rigging contract authority:** `ai-docs/manuals/companion-pet-rigging.md` is the canonical v3.1 Doll-Maker schema and craft-gate reference across all children.
- **Zero-cost when `off`:** When `dashboard.companion_mode` is `off` (the default), the shell creates no canvas, runs no `requestAnimationFrame` loop, fetches no pack assets, and incurs zero runtime overhead.
- **T0 compatibility boundary (`release-compatibility.md`):** All companion settings are scalar string entries in `wsconfig`'s `overrides` map declared with existing `dashboard-config-manifest.json` fields (`v0.50.8` verified T0). Never introduce new top-level JSON members in `config.json` (which `v0.50.8` drops on rewrite) or new manifest struct fields (T3).
- **App-scoped shell mount & rail-bottom default home:** Mount once on the persistent outer shell (`shell.js` / `dashboard.tmpl`), surviving normal session switches, with default home resting on the left session rail bottom divider above `.rws-unreg` / `.rfoot`.
- **Windows AV file-touch discipline (`performance.md`):** Companion pack assets are Type 5 blobs indexed in memory at daemon startup and explicit user reload only; interactive frames and HTTP asset hits must not trigger per-request directory scans or disk reads.
- **Public repository IP boundary:** Only IP-clean original Werkschaft default assets and engine/schema code live in git; third-party/community packs live exclusively in local `<config-home>/companions/<pack-id>/`.
- **Dev fixture vs. ship art separation:** Engine children (`pack-store`, `shell-engine`) use an IP-clean v3.1 technical fixture so engine verification never blocks on art polish; final shipped art lands exclusively through `261122-chore-ws-companion-official-art-pack` with human visual approval.

## Completion Criteria

- **Done:** Children 1, 2, 3, and 5 are in `.done/` (delivering the complete opt-in 0-token app-scoped companion with human-approved official Style D D1 art and BYO pack support), and Child 4 is either in `.done/` or explicitly deferred to a follow-up epic.
- **Dropped:** If the owner decides to retire the desktop companion direction from Werkschaft.
- **Deferred:** Post-composite uniform `#1e2433` union-alpha silhouette outline shader R&D track (`companion-pet-rigging.md`) and multi-character simultaneous companions.
