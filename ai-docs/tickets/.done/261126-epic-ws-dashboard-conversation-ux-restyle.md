---
title: "대화창 UX 리스타일 (도구 호출 자동 접힘 · Extra Motion · 화이트보드 전용 모드)"
related:
  261115-feat-ws-dashboard-conversation-rendering-consolidation: preserves existing .wbupdate-topic, mail, and user/lead rendering on the single conversation view
  261114-feat-ws-live-reasoning-summary: shares the #r-convlive live activity host for thinking/reasoning state display
  261126-feat-ws-chat-stream-interactive-question-cards: coexists in .wbupdate-topic cards inside the same single conversation view
  261126-feat-ws-unified-composer-input-component: preserves #csend and .cfoot placement beside the unified WsInput host
sage-review-design: required
completed: 2026-11-27
---

# Dashboard Conversation UX Re-Style (Collapsed Tool Summary, Extra Motion, Whiteboard-Only Mode)

## Scope

Improve the existing single dashboard conversation view (`.convo`, `#r-entries`, `#r-convlive`, composer footer, and bottom-left Settings popover) across three cohesive, orthogonal axes without creating a second conversation renderer:

1. **Axis A — Baseline Collapsed Turn Tool Summary & Added Tokens:** Automatically collapse completed lead tool calls into a single expandable summary row (`✓ N개 툴 호출 · 소요시간 [· 실패 K] · +Nk tok ▾`) across all harnesses, exposing a stable token-target DOM hook (`[data-conv-turn-tokens]`) and turn drawer structure for downstream children.
2. **Axis B — Unified `#r-convlive` Extra Motion Engine & Bottom-Left Settings Cleanup:** Introduce one extensible 2D Canvas live-motion host and tool/state motion registry inside `#r-convlive` (tool-category colors & 15fps sways, thinking/reasoning cloud bloom, elastic pop-in transitions, modern `휘잉—피크스톱—슈욱` 3-beat tension flights across 5 directional archetypes, dynamic top-right `[data-conv-turn-tokens]` convergence with green/red RPG floating `+m` token popups, frail sputtering jittery coral-red FAIL flights, inline hover `모션 끄기`, bottom-left `localStorage` `ws.dashboard.extraMotion` default ON, `prefers-reduced-motion` compliance, `0 rAF` when idle/OFF), and remove the obsolete top `레이아웃` (`자동`/`나란히`/`탭`) section from the bottom-left Settings popover.
3. **Axis C — Bottom-Right `화이트보드 전용` Mode Toggle:** Add a per-session `화이트보드 전용` toggle (default OFF) directly below the Send button in `.cfoot`; when ON, inject `<ws-ambient>` whiteboard-only guidance on send (hidden from the visible user message bubble) and fold plain-text assistant replies (`.msg.fromlead`) into the turn's collapsed summary drawer while keeping them expandable on click.

Approved visual reference: `ai-docs/assets/mockups/261125/whiteboard-chat-view/mockup.html` (v10, confirmed at `(28)=ok`).

## Non-Scope

- Creating a second conversation view renderer or a dual `화이트보드 중심 / 상세` view-mode switcher (`(10)`–`(12)` rejected).
- Altering `.wbeffects.wbupdate` / `.wbupdate-topic`, `.msg.user`, or mail delivery consolidation (`261115` Phase 1 stays intact).
- Adding backend `config_registry.go` / `dashboard-config-manifest.json` keys for client-only `Extra Motion` preferences (UI preferences remain browser `localStorage` in the bottom-left Settings popover).
- Implementing provider-side reasoning summary extraction (owned by `261114-feat-ws-live-reasoning-summary`; Axis B only provides the `#r-convlive` visual thinking state slot).
- Changing `<wb:answers>` wire format or breaking older daemon/client binaries (all changes stay **T0 transparent**).

## Child Tickets

- `261126-feat-ws-dashboard-conv-collapsed-tool-summary-tokens` — Axis A foundation: `wslive` per-turn `AddedTokens` capture + default collapsed 1-line expandable turn tool summary row + shared `[data-conv-turn-tokens]` and drawer DOM hooks (`todo/`, no prerequisites).
- `261126-feat-ws-dashboard-conv-whiteboard-only-mode` — Axis C: bottom-right `.cfoot` `화이트보드 전용` toggle (default OFF) + optional `whiteboard_first` `<ws-ambient>` prompt injection + folding `.msg.fromlead` into the turn summary drawer (`todo/`, `blocked-by: 261126-feat-ws-dashboard-conv-collapsed-tool-summary-tokens`).
- `261126-feat-ws-dashboard-conv-extra-motion-live-engine` — Axis B: unified `#r-convlive` Extra Motion 2D Canvas host + v10 tension-rhythm/pop-in/dynamic token `+m` engine + tool-category & thinking registry + inline hover `모션 끄기` + bottom-left `ws.dashboard.extraMotion` setting (default ON) + removal of obsolete bottom-left `레이아웃` section (`todo/`, `blocked-by: 261126-feat-ws-dashboard-conv-collapsed-tool-summary-tokens`).

## Cross-Child Decisions

1. **Single existing conversation view — zero dual renderer (`(10)`, `(12)`):** All three axes compose directly on the existing `.conv.centries` + `#r-convlive` + `.cfoot` + bottom-left Settings UI. Existing `.wbeffects.wbupdate` / `.wbupdate-topic` and `.msg.user` rendering stays untouched.
2. **Compatibility tier: T0 (transparent):** Every new backend JSON field (`AddedTokens`, `whiteboard_first`, optional live attributes) is additive and `omitempty`; client preferences (`ws.dashboard.extraMotion`, per-session `whiteboard_first` toggle state) stay browser-local; older clients/daemons work without degradation.
3. **Shared turn-summary DOM contract owned by Child 1 and consumed by Children 2 & 3:**
   - Every collapsed turn summary row renders a stable root (`[data-conv-turn-summary]`), an expandable drawer body (`[data-conv-turn-drawer]`) holding tool rows (and folded plain-text output when Child 2's `화이트보드 전용` is active), a failure count slot (`[data-conv-turn-fails]`), and a right-side token counter slot (`[data-conv-turn-tokens]`) whose live `getBoundingClientRect()` coordinates Child 3's `#r-convlive` canvas targets dynamically.
   - Neither Child 2 nor Child 3 creates a duplicate turn summary container.
4. **Single `#r-convlive` live-motion host & extensible registry (`(22)`, `(23)=b`, `(24)=a`, `(28)=ok`):**
   - All live states (`tool` across 5 tool categories + `thinking` + future `permwait`/`compacting` slots) share one `#r-convlive` canvas overlay and motion registry, with `Extra Motion` **default ON** (`ws.dashboard.extraMotion` in `localStorage`), instant inline hover `모션 끄기`, OS `prefers-reduced-motion: reduce` fallback, and strict `0 rAF` frames when idle or OFF (`ai-docs/manuals/performance.md`).
5. **Bottom-left Settings cleanup (owner explicit approval, 2026-11-26 22:18 KST):**
   - Remove the obsolete top `레이아웃` (`자동` / `나란히` / `탭`) section from the bottom-left Settings popover (dynamic layout is now the default) and place `Extra Motion` (`켜기` / `끄기`) in the exact same popover option-row style.
6. **Whiteboard-only toggle defaults & visibility (`(07)`, `(24)=a`, 2026-11-26 21:52 KST):**
   - `화이트보드 전용` sits at the right end of `.cfoot` directly below the Send button (`.csend`), **default OFF**, tracked per session.
   - When ON, the `<ws-ambient>` directive never appears in the user's visible message bubble, and folded `.msg.fromlead` output remains 100% inspectable inside the expandable turn summary drawer.
7. **Visual & performance manuals binding on every child:**
   - `ai-docs/manuals/dashboard-style.md` (borderless Material dark tonal surfaces `#141518` / `#1e2024` / `#2a2d33`, calm semantic accents, light/dark parity).
   - `ai-docs/manuals/performance.md` (zero idle rAF frames, minimal incremental DOM updates, zero disk I/O on interactive paths, preserve `.convo` scroll anchoring `84cd8692`).
   - `ai-docs/manuals/release-compatibility.md` (T0).

## Completion Criteria

- **Done:** All three child tickets (`261126-feat-ws-dashboard-conv-collapsed-tool-summary-tokens`, `261126-feat-ws-dashboard-conv-whiteboard-only-mode`, `261126-feat-ws-dashboard-conv-extra-motion-live-engine`) are implemented, verified (Go tests, Node DOM harnesses, Playwright dark/light visual captures matching `ai-docs/assets/mockups/261125/whiteboard-chat-view/mockup.html`), and closed to `.done/`.
- **Dropped:** Owner cancels the conversation UX re-style initiative before child implementation.
- **Deferred:** Additional bespoke `#r-convlive` Extra Motion choreography for `.permwait` / compaction / retry states (beyond the registry slot & calm fallback) and provider-side reasoning summary extraction (`261114-feat-ws-live-reasoning-summary`).


## Resolution (2026-11-27)

All three child tickets are completed, merged to `develop`, verified live, and closed in `.done/`:
- Child 1: `261126-feat-ws-dashboard-conv-collapsed-tool-summary-tokens` (`c7e84b338`)
- Child 2: `261126-feat-ws-dashboard-conv-whiteboard-only-mode` (`14fc53a49`)
- Child 3: `261126-feat-ws-dashboard-conv-extra-motion-live-engine` (`171eda325`, test fixes `d21104bab` / `bae854ef2`)

Post-merge live-feedback polish (`4b9191fdd`) and follow-ups (`cd4d87742` turn-summary token priority, `8cea0bbc2` I4 motion staleness, `50fe67a8e` session re-entry burst hotfix) have also landed on `develop`. Structural session re-entry freeze follow-up is tracked in `261127-bug-ws-dashboard-session-reentry-freeze`, and oversized-file splits (`runner_conv.go`, `dashboard-conv-motion.js`, `dashboard-conversation.{js,css}`) are consolidated in `261127-refactor-split-oversized-multi-responsibility-files`. Closed per owner decision `(33)=a`.
