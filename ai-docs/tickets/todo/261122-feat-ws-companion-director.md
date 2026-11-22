---
title: "컴패니언 선택형 사이드카 디렉터, 리드 전용 ws/emotion 및 full 모드 페르소나 가드레일"
parent: 261122-epic-werkschaft-lead-companion
blocked-by: 261122-feat-ws-companion-shell-engine
related:
  261121-research-werkschaft-lead-companion-rig: two-speed AI architecture and open budget/precedence questions
---

# Optional Sidecar Narrative Director, Pluggable Lead-Only `ws/emotion` & `full`-Mode Guardrail

## Background

The 0-token local behavior engine (`261122-feat-ws-companion-shell-engine`) handles 60 fps reactions, gaze, and deterministic HUD events at zero LLM cost. Some users may additionally opt into richer contextual commentary ("sidecar" short quips reacting to conversation deltas, direct lead-driven reactions via `ws/emotion`, or `dashboard.companion_mode: full` persona speaking-style hints in lead chat).

This slice keeps all LLM-assisted director features strictly optional, lead-only, and decoupled from the visual MVP.

## Decisions

- **Lead-only & opt-in posture:** Both `dashboard.companion_director: sidecar | lead-tool` and `dashboard.companion_mode: full` remain inactive when `dashboard.companion_mode` is `off` or `dashboard.companion_director` is `rules` (the defaults). Worker subagents never receive `ws/emotion` or companion persona guidance.
- **`full`-mode strict prose boundary:** Persona speaking-style hints injected into `User preferences` apply exclusively to conversational chat prose to the user — never to code, code comments, commit messages, tickets, manuals, or subagent prompts.
- **Compatibility Tier Verdict — T0 (transparent):** Adding a new opt-in lead-only MCP tool (`ws/emotion`) and optional daemon-to-shell quip/command events preserves all existing MCP tools and call shapes (`ai-docs/manuals/release-compatibility.md`, Scenario 1). Legacy clients never invoke `ws/emotion`; older daemons never emit companion director events.

## Open Decision Queue

- **(47) [policy]** What runtime budget and trigger cadence should `dashboard.companion_director: sidecar` enforce by default?
  - Options:
    - a) Event-triggered only (turn end, error, commit/merge, long idle) with a `small`-tier model and >= 30 s cooldown.
    - b) Periodic delta batching (e.g., every 15 s while active) with a `small`-tier model and token cap per hour.
    - c) Ship `lead-tool` (`ws/emotion`) first and defer the daemon-managed `sidecar` loop until usage demand is validated.
- **(48) [policy]** If a user enables `lead-tool` (`ws/emotion`) alongside local 0-token rules (or switches between `sidecar` and `lead-tool`), what precedence rule should apply?
  - Options:
    - a) Exclusive director mode (`dashboard.companion_director` selects exactly one of `rules`, `sidecar`, or `lead-tool`; `lead-tool` overrides local mood/quip for a short TTL before returning to ambient rules).
    - b) Layered override (`ws/emotion` always overrides `sidecar` for TTL, and `sidecar` overrides `rules` for TTL).

Last ID: (48)

## Constraints

Required manuals (per `AGENTS.md` `### Implementation Conventions`):
- `ai-docs/manuals/ws-mcp.md` — lead-only tool gating (`isLeadOnlyTool`) and MCP tool registration/testing.
- `ai-docs/manuals/wsflow-mirroring.md` — ensure `wsflow` product-mode compatibility if any shared manual/playbook text is touched.
- `ai-docs/manuals/release-compatibility.md` — additive T0 MCP tool and shell event only.
- `ai-docs/manuals/performance.md` — sidecar delta cache stays memory-first; zero extra process spawns per event when an in-process or pooled client path exists.
- `ai-docs/manuals/shipped-surface-boundary.md` — any injected persona guardrail text must be generic and downstream-neutral.

## Phases

### Phase 1: Director Event Contract, Lead-Only `ws/emotion` & Optional Sidecar/Persona Integration

- **Goals:**
  1. Define the bounded daemon-to-shell companion command payload (`{ move_to?, pose?, mood?, quip?, ttl_ms }`) delivered over the existing shell WebSocket stream, with safe fallback when `dashboard.companion_mode === "off"`.
  2. Implement the pluggable lead-only `ws/emotion` MCP tool (registered only when `dashboard.companion_mode !== "off"` and `dashboard.companion_director === "lead-tool"`, or gated cleanly at call time without breaking static tool schemas — finalize during `ready/` design review).
  3. Implement the settled sidecar delta-cached narrative director (per `(47)`) and arbitration TTL (per `(48)`), plus the `full`-mode `User preferences` speaking-style guardrail sourced from the active pack's optional `personaPrompt` field.
  4. Render compact, auto-dismissing flip-safe speech quip bubbles on the outer shell stage above the companion without blocking pointer events on underlying UI.
- **Deferred scope:** Multi-character dialogue or cross-session companion memory persistence.
- **Verification boundary:**
  - Targeted MCP unit tests verifying lead-only gating, `off`-mode inertness, and `ws/emotion` dispatch.
  - Targeted `wsdashboard` tests verifying command payload routing, TTL expiry, and quip sanitization (escaped text only, zero raw HTML injection).
