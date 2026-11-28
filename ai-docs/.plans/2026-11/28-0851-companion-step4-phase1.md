# Plan: Step 4 Phase 1 — Naming Cleanup, Server-Enforced Posture Lock, Real-VRM Knock/Phone Choreography & Messenger Popup UI

## Context & Goal
Implement **Phase 1** of `261127-feat-ws-companion-layered-agents-and-naming` on branch `impl/develop/deed-proud-petal` (pooled worktree `/Users/kang-sw/.ws-worktrees/devenv/appraisal-scallion-print`), embodying owner-settled decisions `(146)`, `(148)=a`, `(149)`, `(150)=a`, and `(1727)=a`:
- **Compatibility tier:** `T0 transparent` (additive `ws/companion.*` MCP tools, additive `/api/companion/phone/*` HTTP routes & SSE events, pre-write posture-lock refusals in `callTool`, preserved `dashboard.companion_*` keys).
- **Performance (`ai-docs/manuals/performance.md`):** Posture state, Embodiment Lease, and bounded phone inbox ring stay **100% in-memory** (zero per-message/per-tick file I/O on interactive paths).
- **Visual design (`ai-docs/manuals/dashboard-style.md`):** Borderless Material tonal surfaces, Dark + Light theme parity, zero harsh `1px` borders, 100% responsive Variant D shell compatibility (popup mounted under `document.body`, anchored beside `#rail-companion-slot`).
- **Required manuals to read before editing:** `ai-docs/manuals/dashboard-style.md`, `ai-docs/manuals/performance.md`, `ai-docs/manuals/release-compatibility.md`, `ai-docs/manuals/shipped-surface-boundary.md`, `ai-docs/manuals/ws-mcp.md`.

## Work Items

### 1. Official Naming Cleanup (`(1727)=a`, T0)
- Remove remaining `pet` / `desktop pet` phrasing from:
  - `agents-plugin-tool/internal/wsdashboard/companion_legacy.go` comments (`L21, 25, 57, 70`) while keeping deprecated `dashboard.companion_mode` shim keys intact (`T0`).
  - `ai-docs/manuals/companion-pet-rigging.md` frontmatter `summary:` and prose (keep the filename unchanged so existing test citations in `companion_pack_guide_test.go` remain valid).
- Verify all `dashboard.companion_*` config keys and `ws/companion.*` surfaces use `Companion` / `컴패니언`.

### 2. Server-Enforced Posture Lock (`laptop | phone`), Embodiment Lease, Phone Inbox & `ws/companion.*` MCP Tools
Create dedicated clean files (do not bloat existing large files):
- `agents-plugin-tool/internal/wsdashboard/companion_phone.go` (and unit tests `companion_phone_test.go`):
  - In-memory `CompanionPhoneStore` (thread-safe, zero disk I/O):
    - 4-state lifecycle per active scope/global companion:
      - `laptop_ambient` (`posture = "laptop"`, `lease = free`)
      - `laptop_sleepy` (`posture = "laptop"`, `lease = free`)
      - `phone_active` (`posture = "phone"`, `lease = locked`)
      - `laptop_work` (`posture = "laptop"`, `lease = locked`)
    - Bounded in-memory message ring (cap 50 messages), unread user message queue, client unread count, and thin 1-line summary ring (cap 10) for Tier-1 backfeed.
    - Operations:
      - `EnqueueUserMessage(text)` -> appends user message, increments unread inbox, acquires/marks `phone_buzz`, broadcasts SSE `companion-phone` + `companion-command` (`phone_buzz`) on `/rail/events`.
      - `PhonePickup(owner)` -> atomically sets `posture = "phone"`, `state = "phone_active"`, locks Embodiment Lease to `owner`, drains/marks unread inbox as delivered, broadcasts SSE `companion-phone` + `companion-command` (`phone_pickup`), returns unread messages + recent context.
      - `PhoneRead(owner)` -> requires `posture == "phone"`; returns newly arrived unread messages + recent context.
      - `PhoneSend(owner, text, tone, confirmCard)` -> requires `posture == "phone"`; appends companion reply, increments client unread count if popup closed, broadcasts SSE `companion-phone` + expression reaction (`laugh`/`merong`/`annoyed`/etc.).
      - `PhonePutdown(owner, style, next)` -> requires `posture == "phone"` (idempotent no-op if already laptop on turn-end failsafe):
        - `style`: `"normal"` | `"facedown_merong"`
        - `next`: `"ambient"` (default: releases Embodiment Lease, records 1-line summary in Tier-1 backfeed ring, transitions to `laptop_ambient`) | `"work"` (keeps Embodiment Lease held, transitions to `laptop_work`).
        - Broadcasts SSE `companion-phone` + `companion-command` (`phone_putdown` / `phone_merong_facedown`).
      - `ReleaseOnTurnEnd(owner)` -> failsafe hook: if `owner` still holds `phone_active`, auto-emits `PhonePutdown(owner, "normal", "ambient")`; if `laptop_work`, releases lease to `laptop_ambient`.
      - `EmbodimentLeaseActive() bool` -> checked in `companion_director.go` (`directorFire`) so Tier-1 ambient ticks pause cleanly whenever the lease is locked (never overwriting phone or Tier-2 work animations!).
  - HTTP routes (beside `/api/companion/focus` in `companion_pack_routes.go` or `companion_phone.go`):
    - `GET /api/companion/phone/thread` -> JSON snapshot `{state, posture, leaseOwner, messages, unreadClient, unreadAgent, tier1Summary, rotationMeta}`
    - `POST /api/companion/phone/send` (`TrustedWrite`) -> calls `EnqueueUserMessage(text)`, returns updated message & state
    - `POST /api/companion/phone/mark-read` (`TrustedWrite`) -> clears `unreadClient`
    - `POST /api/companion/phone/reset-summary` (`TrustedWrite`) -> clears/rotates Tier-1 summary ring
    - SSE event `companion-phone` wired into `sse.go` (`regionRail`), `assets/dashboard-stream.js`, `assets/dashboard-lifecycle.js`, and `assets/shell.js` `KINDS`.
- `agents-plugin-tool/internal/mcp/companion_tools.go` (and `companion_tools_test.go`, wired via `server_schemas.go`, `server_dispatch.go`, `server_inventory.go`, and daemon proxy bridge in `cmd/ws-mcp/`):
  - Additive MCP tools (`T0`):
    - `companion_speak` (`text`, optional `ttl_ms`)
    - `companion_face` (`expression`, optional `ttl_ms`)
    - `companion_motion` (`gesture`, optional `prop`, optional `ttl_ms`)
    - `companion_phone_pickup`
    - `companion_phone_read`
    - `companion_phone_send` (`text`, optional `tone`)
    - `companion_phone_putdown` (optional `style`: `normal|facedown_merong`, optional `next`: `ambient|work`)
  - **Strict Server-Side Posture Lock in `Server.callTool` AND `Server.callDiscoveryTools`:**
    - When the caller's companion posture is `"phone"`: allow ONLY `companion_*` tools (`companion_phone_pickup`, `companion_phone_read`, `companion_phone_send`, `companion_phone_putdown`, `companion_speak`, `companion_face`, `companion_motion`) and `workflow_state`; **hard-refuse** all laptop/work tools (`daemon_*`, `mailbox_*`, `exec_*`, `git_*`, `tickets_*`, `agent_*`, `worktree_*`, `whiteboard_*`, `config_tune`, etc.) with a clear refusal message (`"Hands busy holding smartphone — call ws/companion.phone_putdown first to use your laptop."`).
    - When the caller's companion posture is `"laptop"`: **hard-refuse** `companion_phone_read` and `companion_phone_send` (`"Smartphone is on the desk — call ws/companion.phone_pickup first to check or reply."`).
  - **Phone Buzz Piggyback:** Alongside `applyMailboxPiggyback` (non-JSON, non-error tool responses only), append `[phone buzz: N unread]` when unread user phone messages exist for that session!

### 3. Real-VRM Cosmetic Glass Knock & Desk Smartphone 3D Choreography
- `agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion-vrm-colead.js` & `dashboard-companion-vrm.js` & `dashboard-companion-stage.js` & `companion_director.go`:
  - **Pure Cosmetic Glass Knock:**
    - Direct click on VRM character / `.ws-companion-hit` in `#rail-companion-slot` NEVER opens the messenger popup.
    - Emits pointer-origin glass ripple ring + soundless `"톡톡"` cue.
    - When awake (`laptop_ambient`): crisp startled jump + surprised face + brief glance at user; 3+ rapid knocks within ~4s trigger playful `annoyed_tap` pout + glass-tap motion.
    - When sleepy (`laptop_sleepy` / `zZz` idle): wake-up jolt (`"화들짝!"`) + wide-eyed reaction before smoothly settling back into laptop typing.
  - **3D Desk Smartphone Prop & Choreography (`dashboard-companion-vrm-colead.js`):**
    - Add minimalist 3D desk smartphone prop (`ws-colead-phone`) resting on the right side of the desk beside the laptop (distinct front screen panel + back camera module so face-up vs face-down is unmistakably readable on real VRM!).
    - Smooth FK bone & prop transitions:
      - `desk`: resting face-up beside laptop, subtle dark screen.
      - `phone_buzz`: screen lights up with bright notification glow + rapid vibration micro-shake + `"윙- ♪"` badge, character glances down-right toward phone!
      - `phone_pickup` / `phone_typing`: right arm lifts smartphone naturally beside laptop to chest/face height, head tilts toward phone screen, left hand relaxes from laptop, gentle thumb-typing micro-motion during thinking/typing!
      - `phone_putdown(normal)`: smoothly lowers phone face-up onto desk and returns both hands to laptop typing.
      - `phone_putdown(facedown_merong)`: adorable cheeky wink + playful smile/merong expression + `"메-롱"` floating badge -> flips smartphone **face-down** onto desk (camera module facing up, `"탁!"` cue) -> smoothly returns to laptop typing!
  - Register new VRM gestures/expressions cleanly in `dashboard-companion-vrm.js` AND mirror them in `companion_director.go` (`companionVrmExpressions`, `companionVrmGestures`) so zero vocabulary drift occurs.
  - Expose deterministic test hook methods on `window.WsCompanion` / co-lead controller (e.g. triggering knock, sleepy wake-up, `phone_buzz`, `phone_pickup`, `phone_typing`, `phone_putdown('normal')`, `phone_putdown('facedown_merong')`) so Playwright real-VRM tests can inspect every beat reliably.

### 4. Top-Right `#companion-chat-btn` & Compact KakaoTalk-Style Messenger Popup UI
- Follow the owner-approved mockup `ai-docs/assets/mockups/261128/companion-messenger-popup/` (already copied into this worktree — include those 4 text files in your commit!).
- Create clean dedicated assets:
  - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion-messenger.js`
  - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion-messenger.css`
  - Wire them in `pages.go`, `asset_source_test.go`, and `dashboard-companion-stage.js` / `dashboard-companion.css`.
  - Add all new UI strings to all 11 locale tables under `agents-plugin-tool/internal/wsi18n/tables/*.json` (and verify `go test ./internal/wsi18n` passes 100%).
- UI details:
  - `#companion-chat-btn`: `22x22` speech-bubble button in `.ws-companion-hit` beside `.ws-companion-gear` (`top: 2px; right: 28px`), with crisp red unread badge dot when `unreadClient > 0` (always visible when unread > 0 or popup open; calm hover-reveal otherwise).
  - Floating compact KakaoTalk-style popup (`~360x540px`, clamped to viewport) mounted directly under `document.body` (`position: fixed`, anchored beside `#rail-companion-slot`, zero clipping by `.rail` `overflow: hidden`, 100% compatible with Variant D resizable rail).
  - Header: mini status dot + live posture subtitle (`노트북 작업 중` / `스마트폰 알림!` / `한 손으로 폰 확인 중` / `메-롱! 폰 엎어둠`), Tier-1 `BoundedRotation` summary drawer toggle, `Pin` toggle, Settings jump (opens `#workflow-settings` directly to Companion section), Close button (`Esc` & outside-click dismiss when unpinned).
  - Thread: warm amber-espresso dark / butter-cream light user bubbles, borderless Material tonal companion bubbles with posture/action pills, typing indicator when `phone_active` / `laptop_work`, 1-Click confirmation card renderer (styled & wired cleanly for Phase 2 backend actions), composer (`Enter` sends via `POST /api/companion/phone/send`, `Shift+Enter` newline).

## Verification & Mandatory Real-VRM Visual QA
1. `python3 agents-plugin-tool/scripts/generate-package-assets.py --check`
2. `node --test internal/wsdashboard/frontend-companion*.test.js` (add unit tests for posture transitions, cosmetic knock, messenger popup anchoring/clamping)
3. `go test -count=1 ./internal/wsi18n`
4. `go test -count=1 ./internal/mcp -run "TestCompanion|TestPosture|TestTool"`
5. `go test -count=1 ./internal/wsdashboard -run "TestAsset|TestCompanion"` (including synthetic + real-VRM `WS_VRM_TEST_PHASE=1` Playwright browser tests!)
6. `GOOS=windows go vet ./...`
7. **Real-VRM 0.x + 1.0 High-DPI Visual QA (`WS_VRM_TEST_PHASE=1`, `WS_COMPANION_QA_SHOTS=/tmp/ws-step4-phase1-qa`)**:
   Capture and visually inspect ALL 4 contact sheets via `read_file`:
   - `01-vrm-knock-and-sleepy-wakeup.png`: cosmetic glass knock ripple/startle + sleepy wake-up jolt, confirming zero popup opens on direct click.
   - `02-vrm-phone-buzz-pickup-typing.png`: desk phone buzz glow + one-hand lift & thumb-typing pose on BOTH real VRM 1.0 and VRM 0.x (verify zero clipping, clean arm/hand/phone framing!).
   - `03-vrm-merong-facedown.png`: cheeky wink/merong expression + face-down flipped smartphone (`탁!`, camera bump up) on BOTH real VRM 1.0 and VRM 0.x!
   - `04-messenger-popup-dark-and-light.png`: `#companion-chat-btn` + floating KakaoTalk-style messenger popup beside `#rail-companion-slot` in Dark & Light themes, zero overflow/clipping, borderless Material hierarchy!
8. **Privacy/IP & Hygiene:** Confirm zero `.vrm`/`.png`/`.jpg`/`.webp` files staged or committed, and zero third-party character/model/creator/company names in `git diff`.
9. Commit cleanly on `impl/develop/deed-proud-petal` using `ws/git.commit` with explicit paths (including this plan file unchanged and the 4 text files in `ai-docs/assets/mockups/261128/companion-messenger-popup/`).
