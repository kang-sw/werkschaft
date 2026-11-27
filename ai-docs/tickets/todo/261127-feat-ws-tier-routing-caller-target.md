---
title: "티어 라우팅: 호출자 하네스 직접 매핑 (caller.* / caller.<size>)"
---

# Tier Routing: Direct Caller-Harness Target Mapping (`caller.*` / `caller.<size>`)

## Background

Today `agent.tier_routing` (`agents-plugin-tool/internal/wsconfig/tier_routing.go`)
allows pointers to `<node>:<size|*>`, `<harness>.<size|*>`, or a `<bundle>`
name. When a user wants a tier node (e.g., `role`, `group`, or custom node) to
resolve dynamically to whichever harness the caller itself is running on —
either preserving the incoming size (`caller.*`) or mapping to a fixed size on
the caller's harness (`caller.<size>`) — there is no explicit `caller` target
syntax across the config schema or the Dashboard Settings UI.

In `agents-plugin-tool/internal/mcp/dashboard_tier_routing.go` (around lines
205–218), an unconfigured default pointer (`selector == "*"`) synthesizes an
implicit `Kind: "caller"` edge toward the active harness, and
`agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-tiers.js`
(around lines 639 and 653) treats `role` vs non-role primary pointers
inconsistently without an explicit `Caller harness` selector option in default
pointers or `+ exception` rows.

## Decisions

*(Settled on whiteboard; scheduled for `0.54.0`)*

- **Official `caller.*` / `caller.<size>` syntax — settled `(1708)=a` (T3):**
  - **Chosen:** Reserve `caller` as a first-class target keyword in
    `agent.tier_routing`, supporting both `caller.*` (keep requested size on
    the caller's harness) and `caller.<size>` (`small`, `medium`, `large`,
    `xlarge`) across both default (`*`) pointers and `+ exception` (`:<size>`)
    rows.
  - **Rejected:** Inferring caller routing solely from an omitted `*` entry
    (cannot express `caller.<size>` overrides or `+ exception` rows, and cannot
    override a role/node default back to caller explicitly).
  - **Compatibility tier — T3 (explicit owner approval for `0.54.0` minor bump; DO NOT land in `0.53.x` patch):**
    `0.51–0.53.1` binaries fail `wsconfig.LoadTierGraph` when an unknown target
    harness `caller` is persisted in `~/.ws/config.json`:
    - `agents-plugin-tool/internal/wsconfig/tier_routing.go:167-184` (`parseTierTarget` parses `caller.<size|*>` as `tierTargetHarness` with `name: "caller"`),
    - `agents-plugin-tool/internal/wsconfig/tier_routing.go:325-330` (`knownHarness` / `validateStoredHarness` rejects `"caller"` because it is not a registered backend harness),
    - `agents-plugin-tool/internal/wsconfig/tier_routing.go:367-378` (`TierGraph.validate` fails the config load with `unknown harness "caller"`),
    - `agents-plugin-tool/internal/wsconfig/tier_routing.go:541-543` and `0.51–0.53.1` `mcp/agent_tools.go:579-582` (now `mcp/tier_routing.go` / `mcp/config_tier_routing.go`) propagate the `LoadTierGraph` error on `agent.spawn` / `agent.tiers`.
    Because older `0.53.x` binaries cannot tolerate `caller.*` / `caller.<size>`
    saved in shared `~/.ws/config.json` without failing `LoadTierGraph`, this
    change **must** wait for the `0.54.0` minor boundary.
- **Dashboard Settings UI integration — settled `(1709)=ok`:**
  - **Chosen:** Expose a `Caller harness` option in `dashboard-settings-tiers.js`
    for both `Default pointer` (enabled for **all** node types including
    `role`) and `+ exception` rows, paired with an active size selector (`*`
    or fixed size `small`/`medium`/`large`/`xlarge`), and update the graph view
    in `dashboard_tier_routing.go` and `dashboard-settings-tiers.js` to render:
    - a caller terminal card,
    - non-`*` caller edges when exceptions or `caller.<size>` are used, and
    - `· caller` in the node preview summary.
- **Backlog scheduling — settled `(1714)=a`:**
  - **Chosen:** Capture in `todo/` now for the `0.54.0` cycle after `v0.53.2`
    ships.

## Constraints

- **Release compatibility (`ai-docs/manuals/release-compatibility.md`):**
  - T3 minor-bump boundary (`0.54.0` only; strictly forbidden in `0.53.x`
    patches). Ensure config validation, `wsagent` tier resolution, and daemon
    skew boundaries behave cleanly under `0.54.0`.
- **Dashboard settings catalog & i18n (`ai-docs/manuals/config-settings-ui.md`):**
  - Update `agents-plugin-tool/internal/wsi18n/tables/` (`en.json`, `ko.json`,
    and all locale tables) for new `js.settings.tier_routing.*` strings (e.g.,
    `Caller harness` label, caller card/preview text), run
    `python3 agents-plugin-tool/scripts/generate-package-assets.py --check`,
    and pass `go -C agents-plugin-tool test ./internal/mcp ./internal/wsi18n ./internal/wspackage -run 'Settings|Manifest'`.
- **Visual design (`ai-docs/manuals/dashboard-style.md`):**
  - Keep the caller terminal card, edge styling, and exception selector rows
    consistent with Werkschaft's borderless Material tonal surface tokens and
    existing tier-routing graph styling.
- **Shipped surface boundary (`ai-docs/manuals/shipped-surface-boundary.md`):**
  - Keep shipped `tier_routing` guidance, error messages, and UI strings
    downstream-generic with zero devenv-only references.

## Phases

### Phase 1: `wsconfig` & `mcp` Parser, Resolver, and Graph Support for `caller.*` / `caller.<size>`

1. **Keyword reservation & parsing (`agents-plugin-tool/internal/wsconfig/tier_routing.go`):**
   - Reserve `caller` as a target keyword (`tierTargetCaller` or explicit
     handling in `parseTierTarget` / `validateStoredHarness` / `TierGraph.validate`)
     so `caller.*` and `caller.<size>` validate cleanly while disallowing
     colliding custom node/bundle names named `caller`.
2. **Runtime resolution (`wsconfig` / `wsagent`):**
   - Resolve `caller.*` to `<callerHarness>.<requestedSize>` and
     `caller.<size>` to `<callerHarness>.<size>`, participating in hop-limit
     checks, cycle detection, and trace reporting.
3. **Dashboard graph projection (`agents-plugin-tool/internal/mcp/dashboard_tier_routing.go`):**
   - Emit explicit caller edges (both `*` and non-`*` exception/fixed-size
     edges) and caller terminal node metadata for the settings graph view.
4. **Verification:**
   - Unit tests in `wsconfig/tier_routing_test.go`, `wsagent/agent_tier_routing_test.go`,
     and `mcp/dashboard_tier_routing_test.go` covering `caller.*`,
     `caller.<size>`, `+ exception` overrides, reserved-keyword collision checks,
     and graph edge projection.

### Phase 2: Dashboard Settings UI Dropdowns, Graph Edges, and Translations

*Depends on Phase 1 backend/graph projection.*

1. **Tier editor controls (`agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-tiers.js`):**
   - Add `Caller harness` as a selectable target option in both `Default
     pointer` (for all node types including `role`) and `+ exception` rows,
     paired with the `*` / fixed-size selector.
   - Render the graph caller terminal card, non-`*` caller edges, and
     `· caller` preview summary cleanly.
2. **Translations (`agents-plugin-tool/internal/wsi18n/tables/`):**
   - Add `en.json` / `ko.json` (and any additional locale tables) entries for
     the new tier-routing caller labels and hints.
3. **Verification:**
   - Browser/UI and catalog parity tests (`wsdashboard/config_tier_routing_test.go`,
     `wsdashboard/tier_routing_browser_test.go`, `wsi18n`, `wspackage`) plus
     `generate-package-assets.py --check`.
