---
title: "보드 스크롤을 '보는 토픽' 기준으로 고정"
---

# Anchor the dashboard board scroll to the current topic

## Background

The right-side whiteboard board preserves an exact outer-board pixel reading position across updates. It captures and restores the reading position, scans exposure with uncached `checkVisibility` calls on some paths, and measures pre- and post-update rectangles for growth and FLIP animations. The board has a shared probe snapshot, but the live-panel and exposure-unit scans still call `wbHistory.rendered` directly, and `exposed` checks `rendered` again (`dashboard-board.js:1112,1165,1473`, `stream-view.js:149,167,472`).

In the owner's 2026-11-23 11:21 trace, the longest main-thread stall was 1,643 ms in `onRegion`, through `boardStageSnap`, `boardKeep`, and `boardExposureUnits`. `checkVisibility` accounted for 1,463 ms with 233 forced layouts. Of roughly 4.46 s sampled on the board, exposure and visibility took 1.67 s, and scroll and geometry took 2.40 s, including 1.14 s in `boardKeep`. History loading took 1.25 s and was already incremental. Keep its demand-driven loading model.

## Decisions

The owner confirmed the scroll model on the whiteboard at 11:34 on 2026-11-23 and settled the implementation choices at 11:41–11:42.

- **(1425)=a.** One card represents one whiteboard topic (`section.topic[data-topic]`). Entry panels, history, and frames scroll inside the card. Rejected: one panel per card, which would create too many cards and frequent snap moves.
- **(1426)=b.** Cards retain their content height up to the usable board scrollport height below the sticky toolbar. Overflow scrolls inside the card. Short cards stack so several can share the screen. The outer scroll state is the current topic's key, with its top aligned to the usable scrollport top. Resizing topics above or below it leaves that alignment intact. Rejected: fixed full-height slots per topic, which lose information density when short topics are adjacent.
- **(1427)=a, canonicalize.** Apply canonical insertion and reorder immediately while retaining the current topic by key. Only that topic can stage updates or contribute to the pending-update badge. All other topics update freely, including off-screen topics. If the current topic closes or leaves the list, choose the next topic, or the previous topic if there is no next topic. Rejected: auto-follow the newest topic, which would move the reader away from the selected topic.
- **(1428)=a.** At a card body's scroll edge, stop the first wheel gesture and let the next gesture in that direction select the next or previous topic. Reuse the pinned gallery's boundary and momentum gate so residual events in the same gesture cannot carry the reader into another topic. Rejected: immediate chaining and navigation available only through buttons or keys. Provide visible previous and next controls because an iframe can own wheel and keyboard input. (`cards.js:1091,1124`.)
- **(1430)** Derive the trailing runway from the usable scrollport and final card so the last short topic can align at the top without a per-update geometry read. Rejected: a fixed viewport-height spacer, which adds a full viewport of blank space.
- **(1431)** Scope CSS overflow anchoring to the current topic, with key-based placement on selection and canonical order changes. This lets ordinary size changes leave the current topic in place with zero outer-board geometry reads. Rejected: explicit key-and-offset correction on topic resize.
- **(1432)** Place one pair of previous and next controls in the sticky board toolbar so they remain visible when a frame owns input and keep short cards compact. Rejected: controls in each card header.
- **(1433)** Docked panes retain the gallery's independent scroll and wheel behavior. Each pane already has a body scroller, and the gallery owns page transitions. Rejected: adding board topic snaps inside docked panes.
- **(1434)** Restore the current topic key after a reload or asset reload from tab-scoped `sessionStorage`. Select the first topic when the key is gone. Daemon restarts reload the page often, and saving one client-side key requires zero geometry reads and no server state (T0). Rejected: always selecting the first topic on reload.
- **(1435)** Keep existing board tools and standalone cards reachable outside the topic snap sequence so wheel navigation and removal fallback use a single ordered list of topic keys. Rejected: inserting non-topic snap stops.
- **(1437)=a.** Add outward lazy topic loading as Phase 2 of this ticket after reporting Phase 1's forced-layout and DOM-operation counts. The current-topic anchor lets placeholders above and below it change height without moving the reader. Render fewer topic bodies to reduce the DOM node count, which ranged from 154,000 to 429,000 with a GC sawtooth in the trace. Rejected: a separate follow-up ticket.
- **Frontend ownership and compatibility.** A native Claude UI worker implements both phases, following the owner's 10:40 rule to split frontend and backend work. Compatibility is T0. The update changes browser-side behavior and leaves server, route, and store contracts intact. There is no backend part or phase.

## Constraints

- Follow [Frontend Minimal Updates](../../manuals/performance.md#frontend-minimal-updates) and [Measuring](../../manuals/performance.md#measuring) in `ai-docs/manuals/performance.md`. Patch changed topics by key, batch writes before reads, reuse probes within a read pass, and let the UI settle without ongoing frame work. A change confined to the size of a non-current topic must trigger zero outer-board geometry reads.
- Cap the card with the usable `#bcol` board scrollport height after allowing for the sticky toolbar. Preserve the viewport-relative fixed positioning of maximized topics and entry panels. Place any new containment outside their fixed containing-block ancestry. (`dashboard-board.css:130,234`.)
- Keep the history reading anchor for prepends inside each card. Keep lazy history fill and gaps, loaded-row and fold restore, and near-view history hydration. Scope history work to affected or nearby topics. (`stream-navigation.js:107,144,203,233,266`; `stream-view.js:181,286`.)
- Keep frame grafting and reuse, including batched height reports and near/far frame lifecycle. Frame size changes can update the affected card body without moving the outer reading position. (`dashboard-board.js:749,818,845`; `dashboard-frames.js:93,429`.)

## Prior Art

Source references were checked on `develop` at `23bb1f2be`. Dashboard assets live under `agents-plugin-tool/internal/wsdashboard/assets/`, and `stream-*` assets under `agents-plugin-tool/internal/wswhiteboard/assets/`.

- `#bcol` hosts the board region (`dashboard.tmpl:29`). The pinned gallery provides keyed pane placement and activation, a clipped viewport, and a topic wrapper that keeps the header stationary above an `overflow: auto` body with overscroll containment. Use those patterns for the board while preserving short topic heights. (`cards.js:40,646,874,911,938,1305`; `gallery.js:239,246`; `dashboard-rail.css:120,147,150,152`.)
- The pending badge and its manual release already use `boardPendingBadge`, `boardApplyPending`, and `boardHome`. Replace the frozen visible interval and board-wide exposure checks with current-topic staging. Preserve the manual release and admit the newest revision when leaving a held topic. (`dashboard-board.js:1132,1185,1272,1310,1363,1412,1471`.)

## Phases

### Phase 1: Switch the right-side board to topic-keyed scrolling

Implement and verify the board layout, selection, and update handling together in the dashboard frontend and its shared view helpers. Install a stable scroll owner for each card body, including short cards. `scrollerOf` and its snapshot version currently recognize an `overflow: auto` owner only when it overflows, with the `.gal-pbody` exception. Mark the board bodies explicitly in both paths so ownership survives transitions into or out of overflow. (`stream-view.js:106,475`.)

Replace outer-board pixel capture and restore, reload geometry restoration, board growth and FLIP rect passes, and above-topic resize compensation. Keep any `keep` and `reading` behavior used by inner history scroll owners. Exclude only the migrated `#bcol` from the generic pane pixel snapshot and restore. (`dashboard-board.js:75,82,139,171,896,950,984,1021`; `stream-view.js:181,286`; `dashboard-frames.js:214,224`; `dashboard.js:357,374`.)

Save only the selected key in tab-scoped `sessionStorage` for board reload recovery. Resolve it against the available topics after a page or asset reload and fall back to the first available topic when no saved key resolves.

Use the current key to decide whether a board update needs staging before inspecting exposure. Limit exposure probes to the current card and clip them to both the card and the usable board scrollport. Preserve the pending badge and manual release for the current card. When a topic or history jump resolves, snap to its containing topic first, then scroll inside that card to the target. Preserve the history loader's target resolution through lazy gaps. (`stream-view.js:130,167,472`; `dashboard-frames.js:284`; `stream-navigation.js:2,43,80`.)

#### Verification

- Run each touched Node test file with `node --test <file>`. Run only those files and skip the full npm suite. Cover short-card stacking, tall-card inner scroll and toolbar clearance, key retention on insertion and reorder, next/previous fallback on closure or removal, non-current updates, pending and manual release behavior, and the wheel gate in both directions. Exercise momentum events as part of the same gesture and verify the controls still work when a frame has focus. Verify key-only restore after a page or asset reload and first-topic fallback when the saved key is absent from the board.
- On a board update fixture, count outer-board geometry reads, forced layouts, and DOM operations before and after a message that changes only a non-current topic's size. Exercise topics above and below the current card, including late frame height reports. Record counts per update and visible latency per `## Measuring`. Require zero outer-board geometry reads and an unchanged current key and top alignment. Collect any alignment assertion reads outside the measured update window. Report the Phase 1 counts in its Result before starting Phase 2.
- Capture a before/after LOOK of the same board showing adjacent short topics, a capped long topic, and toolbar and navigation controls. Also exercise the last short topic, a lazy-history jump and prepend, a reused frame, and maximized topic and entry-panel overlays.
- Run Go tests only on `w5090`. Substitute the worker, minute budget, worktree, and log-directory values below, create the local log directory, and retain the result with the phase evidence.

  ```bash
  bash D:/sys_lock.sh remote w5090 run <who> <min> --sync '<worktree>' -- 'export PATH=$HOME/sdk/go/bin:$PATH; cd agents-plugin-tool && python3 scripts/generate-package-assets.py && go test ./internal/wsdashboard/... ./internal/wswhiteboard/...' > D:/Temp/<purpose>/go-test.log
  ```

### Phase 2: Load visible topic bodies outward from the current topic

Start after Phase 1 has landed and its Result reports the forced-layout and DOM-operation counts. Fully render the current topic body while retaining demand-driven history loading inside it. For each direction in the ordered topic list, advance one topic at a time from the current topic. Hydrate the next topic body when the topic would be visible within the usable board scrollport, then use visibility after that hydration to evaluate the following topic. Stop each direction at the first topic that is not drawn. Use `IntersectionObserver` or equivalent visibility notifications to drive this work without geometry polling.

Keep topics beyond the drawn range as a header and lightweight placeholder. `content-visibility: auto` is acceptable where it supports that behavior. Topics that move far away may return to a placeholder. Preserve loaded history rows and fold state when their bodies return. Placeholder-to-content height changes above or below the current topic must preserve its top alignment.

#### Verification

- Run the touched Node test files with `node --test <file>`. Exercise outward hydration and the first invisible stopping point in each direction, resumption on visibility changes, and jumps that make a placeholder topic current. Verify that placeholder height changes above and below keep the current topic aligned. Exercise history and fold restoration if far-topic eviction is used.
- Use the same board fixture to compare the Phase 1 baseline with Phase 2. Report before/after DOM node counts and forced layouts for the same load, navigation, and update actions, and check that non-current size changes still cause zero outer-board geometry reads. Use a fixture with enough off-screen topics to show the reduction in mounted nodes.
- Capture a before/after LOOK at the same current topic and viewport. Show that visible short topics still stack and that hydration maintains the current topic's alignment. Use the Phase 1 `w5090` command for any Go verification.
