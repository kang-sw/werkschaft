# Plan: Update & Promote `261127-feat-ws-companion-layered-agents-and-naming` (`todo/` -> `ready/`)

## Goal
Update `ai-docs/tickets/todo/261127-feat-ws-companion-layered-agents-and-naming.md` on branch `impl/develop/promote-companion-layered-agents` (pooled worktree `/Users/kang-sw/.ws-worktrees/devenv/arrogant-entrench-jaybird`) with all owner-settled Step 4 architecture decisions, populate `## Route Facts`, run `ws/tickets.verify` and `ws/tickets.sage_gate(landing: "ready")`, and promote the ticket to `ready/` if unblocked (or report exact sage-gate requirement so the lead can run any required review cleanly).

## Settled Decisions to Incorporate into `## Decisions` & `## Phases`
1. **Left-Rail Bottom-Left Companion Handover — settled `(148)=a` (already live `344452e44`, T0):**
   - Upper `.rail-session-panel` holds Sessions + `Unregistered workspaces` + footer; `#rail-companion-slot` owns the dedicated bottom-left corner with `0px` vertical jitter and responsive `width: 100%` (compatible with Variant D `220–360px` resizable rail).
2. **Cosmetic Glass Knock, Top-Right Messenger Button & Smartphone Choreography — settled `(146)` & `(149)` (T0):**
   - Direct character click = purely cosmetic glass knock (startle when awake, 3+ rapid knocks -> playful pout/tap, wake-up jolt when sleepy; NEVER opens messenger).
   - Top-right `#companion-chat-btn` opens/toggles compact floating KakaoTalk-style messenger popup mounted directly under `document.body` (anchored beside `#rail-companion-slot`, viewport-clamped).
   - Sending a message triggers `phone_buzz` (desk phone light/vibration) -> `phone_pickup` (one-hand lift & check + thumb-typing while thinking) -> reply OR cheeky `"메-롱"` + flip phone face-down (`"탁!"`) back on desk if trivial.
3. **Server-Enforced Posture Lock (`laptop | phone`), Embodiment Lease & 4-State Lifecycle — settled `(149)` & `(150)=a` (T0):**
   - Full lifecycle: `laptop_ambient` (free lease, Tier-1 ambient ticks active) / `laptop_sleepy` (`zZz`) ↔ `phone_active` (`posture = "phone"`, lease locked, work tools hard-blocked by MCP server, thinking plays phone-typing gesture) ↔ `laptop_work` (`posture = "laptop"`, lease locked, `phone_read`/`phone_send` hard-blocked by MCP server, thinking/tools play intense laptop typing).
   - Technical decision: `ws/companion.phone_pickup` atomically switches `posture -> "phone"` AND returns unread inbox messages in 1 round-trip (`phone_read` optional for mid-hold polling), with turn-end auto-`phone_putdown(normal)` failsafe.
   - `applyMailboxPiggyback` appends `[phone buzz: N unread]` on non-JSON tool replies when unread messages arrive.
4. **Tier-1 Fast Triage (`EphemeralPool` + `ResponseSchema`) -> Tier-2 Laptop Escalation & Companion=Agent-Backed — settled `(150)=a`:**
   - Prerequisite `261127-feat-ws-ephemeral-lightweight-harness-pool` is `.done` (`EphemeralPool` `BoundedRotation` + `ResponseSchema` JSON validation is live).
   - On `phone_buzz`, warm-cached Tier-1 (`EphemeralPool` with strict `ResponseSchema` — zero MCP boot overhead, zero disk I/O) triages in 1 turn: trivial banter/merong handled directly by daemon-driven `phone_pickup -> phone_typing -> phone_send/phone_putdown(normal|facedown_merong)`; real work triggers `phone_putdown(work)` (keeping Embodiment Lease held) and wakes/steers Tier-2 Co-Lead (`Runner.Deliver` / `conv.steerMail`) in `laptop_work`, which uses `ws/daemon.*` work tools on laptop and `ws/companion.phone_{pickup,send,putdown}` to reply!
   - Companion Mode assumes agent-backed pipeline (`dashboard.companion_director` key kept `T0`-compatible with graceful cosmetic fallback if explicitly disabled or unconfigured).

## Updated 3-Phase Structure
- **Phase 1: Naming Cleanup, Server-Enforced Posture Lock, Embodiment Lease, Real-VRM Knock/Phone Choreography & Messenger Popup UI** (`companion_legacy.go`, `companion-pet-rigging.md`, `companion_phone.go`, `mcp/companion_tools.go`, `server_dispatch.go`/`server_inventory.go`, `dashboard-companion-vrm-colead.js`/`vrm.js`/`stage.js`/`messenger.{js,css}`, `/api/companion/phone/*`, SSE `companion-phone`, 11 locales, real-VRM 0.x/1.0 QA).
- **Phase 2: 2-Layer Hybrid Daemon & UI Control Bridge (`ws/daemon.curl`, `ws/daemon.dom|screenshot|ui`) & 1-Click Confirmation Gate** (reverse SSE-to-POST bridge over `regionRail` + `shell.js` `KINDS` + `POST /api/daemon/bridge-reply`, bearer-authenticated internal daemon API access, 1-click in-messenger approval card for high-impact mutations).
- **Phase 3: Tier-1 Fast Triage (`EphemeralPool` + `ResponseSchema`) & Tier-2 Hosted Co-Lead Escalation Wiring** (`ResponseSchema` plumbed through `CompanionEphemeralRequest` + `companionPoolAdapter`, Tier-1 triage loop separate from passive cooldown/focus drops, `Runner.Deliver`/`conv.steerMail` Tier-2 wakeup in `laptop_work`, thin 1-line summary backfeed to Tier-1, turn-end failsafe, end-to-end integration tests).

## Steps
1. Read `ws/convention.read` for ticket conventions and `ws/playbook.read(name: "ticket-fact-populator")` so `## Route Facts` matches the exact schema expected by `tickets.verify` and `tickets.sage_gate`.
2. Update `ai-docs/tickets/todo/261127-feat-ws-companion-layered-agents-and-naming.md`:
   - Clear stale `blocked-by: 261127-feat-ws-ephemeral-lightweight-harness-pool#phase1` (or note it is `.done` in `related:` — verify ticket convention on resolved `blocked-by`!).
   - Update `## Decisions`, `## Constraints` (including all matching Implementation Conventions manuals from `AGENTS.md`), `## Route Facts`, and `## Phases` (Phase 1, 2, 3).
   - Keep `title:` in Korean and the body in English.
3. Run `ws/tickets.verify` on the ticket.
4. Run `ws/tickets.sage_gate(stem: "261127-feat-ws-companion-layered-agents-and-naming", landing: "ready")` using this worktree's session key:
   - If `sage_gate` allows immediate move (`allow` / `proceed`), call `ws/tickets.move(to: "ready")` and commit cleanly with `ws/git.commit` (ticket + plan file unchanged).
   - If `sage_gate` returns a review requirement (e.g., `run_sage_review`), commit the updated `todo/` ticket (including `## Route Facts` and any `sage-review:` frontmatter) cleanly and report the exact `sage_gate` output to the parent lead so the lead can run the sage reviewer!
