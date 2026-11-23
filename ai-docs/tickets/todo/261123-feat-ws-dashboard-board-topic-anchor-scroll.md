---
title: "보드 스크롤을 '보는 토픽' 기준으로 고정"
---

# Anchor the dashboard board scroll to the current topic

## Background

The right-side whiteboard board preserves an exact outer-board pixel reading position across updates. It captures and restores the reading position, scans exposure with uncached `checkVisibility` calls on some paths, and measures pre- and post-update rectangles for growth and FLIP animations. The board has a shared probe snapshot, but the live-panel and exposure-unit scans still call `wbHistory.rendered` directly, and `exposed` checks `rendered` again (`dashboard-board.js:1112,1165,1473`, `stream-view.js:149,167,472`).

In the owner's 2026-11-23 11:21 trace, the longest main-thread stall was 1,643 ms in `onRegion`, through `boardStageSnap`, `boardKeep`, and `boardExposureUnits`. `checkVisibility` accounted for 1,463 ms with 233 forced layouts. Of roughly 4.46 s sampled on the board, exposure and visibility took 1.67 s, and scroll and geometry took 2.40 s, including 1.14 s in `boardKeep`. History loading took 1.25 s and was already incremental. Keep its demand-driven loading model.

## Decisions

The owner confirmed the scroll model on the whiteboard at 11:34 on 2026-11-23.

- **(1425)=a.** One card represents one whiteboard topic (`section.topic[data-topic]`). Entry panels, history, and frames scroll inside the card. Rejected: one panel per card, which would create too many cards and frequent snap moves.
- **(1426)=b.** Cards retain their content height up to the usable board scrollport height below the sticky toolbar. Overflow scrolls inside the card. Short cards stack so several can share the screen. The outer scroll state is the current topic's key, with its top aligned to the usable scrollport top. Resizing topics above or below it leaves that alignment intact. Rejected: fixed full-height slots per topic, which lose information density when short topics are adjacent.
- **(1427)=a, canonicalize.** Apply canonical insertion and reorder immediately while retaining the current topic by key. Only that topic can stage updates or contribute to the pending-update badge. All other topics update freely, including off-screen topics. If the current topic closes or leaves the list, choose the next topic, or the previous topic if there is no next topic. Rejected: auto-follow the newest topic, which would move the reader away from the selected topic.
- **(1428)=a.** At a card body's scroll edge, stop the first wheel gesture and let the next gesture in that direction select the next or previous topic. Reuse the pinned gallery's boundary and momentum gate so residual events in the same gesture cannot carry the reader into another topic. Rejected: immediate chaining and navigation available only through buttons or keys. Provide visible previous and next controls because an iframe can own wheel and keyboard input. (`cards.js:1091,1124`.)
- **Frontend ownership and compatibility.** A native Claude UI worker implements the change, following the owner's 10:40 rule to split frontend and backend work. Compatibility is T0. The update changes browser-side behavior and leaves server, route, and store contracts intact. There is no backend part or phase. A backend phase would introduce changes outside this scroll model.

## Constraints

- Follow [Frontend Minimal Updates](../../manuals/performance.md#frontend-minimal-updates) and [Measuring](../../manuals/performance.md#measuring) in `ai-docs/manuals/performance.md`. Patch changed topics by key, batch writes before reads, reuse probes within a read pass, and let the UI settle without ongoing frame work. A change confined to the size of a non-current topic must trigger zero outer-board geometry reads.
- Cap the card with the usable `#bcol` board scrollport height after allowing for the sticky toolbar. Preserve the viewport-relative fixed positioning of maximized topics and entry panels. Place any new containment outside their fixed containing-block ancestry. (`dashboard-board.css:130,234`.)
- Keep the history reading anchor for prepends inside each card. Keep lazy history fill and gaps, loaded-row and fold restore, and near-view history hydration. Scope history work to affected or nearby topics. (`stream-navigation.js:107,144,203,233,266`; `stream-view.js:181,286`.)
- Keep frame grafting and reuse, including batched height reports and near/far frame lifecycle. Frame size changes can update the affected card body without moving the outer reading position. (`dashboard-board.js:749,818,845`; `dashboard-frames.js:93,429`.)

## Prior Art

Source references were checked on `develop` at `23bb1f2be`. Dashboard assets live under `agents-plugin-tool/internal/wsdashboard/assets/`, and `stream-*` assets under `agents-plugin-tool/internal/wswhiteboard/assets/`.

- `#bcol` hosts the board region (`dashboard.tmpl:29`). The pinned gallery provides keyed pane placement and activation, a clipped viewport, and a topic wrapper that keeps the header stationary above an `overflow: auto` body with overscroll containment. Use those patterns for the board while preserving short topic heights. (`cards.js:40,646,874,911,938,1305`; `gallery.js:239,246`; `dashboard-rail.css:120,147,150,152`.)
- The pending badge and its manual release already use `boardPendingBadge`, `boardApplyPending`, and `boardHome`. Replace the frozen visible interval and board-wide exposure checks with current-topic staging. Preserve the manual release and admit the newest revision when leaving a held topic. (`dashboard-board.js:1132,1185,1272,1310,1363,1412,1471`.)

## Open Decision Queue

Last ID: (1435)

(1430) [open] [technical] A layout-derived trailing runway using the usable scrollport and final card over a fixed viewport-height spacer - the last short topic can reach the top without adding a full viewport of blank space or reading geometry on each update.

(1431) [open] [technical] CSS overflow anchoring scoped to the current topic, with key-based placement on selection and canonical order changes, over explicit key-and-offset correction on topic resize - this gives ordinary size changes a path with zero outer-board geometry reads.

(1432) [open] [technical] Previous and next controls in the sticky board toolbar over controls in each card header - a single visible pair remains reachable when a frame owns input and keeps short cards compact.

(1433) [open] [technical] The gallery's existing independent scroll and wheel behavior in docked panes over adding board topic snaps inside those panes - each pane already has a body scroller and the gallery owns page transitions.

(1434) [open] [technical] Select the first available topic on a fresh load or asset reload over adding a replacement reload snapshot - this retires the geometry restore without changing persisted state.

(1435) [open] [technical] Keep existing board tools and standalone cards reachable outside the topic snap sequence over inserting non-topic snap stops - wheel navigation and removal fallback can use one ordered list of topic keys.

## Phases

### Phase 1: Switch the right-side board to topic-keyed scrolling

After the queue is settled, implement and verify the board layout, selection, and update handling together in the dashboard frontend and its shared view helpers. Install a stable scroll owner for each card body, including short cards. `scrollerOf` and its snapshot version currently recognize an `overflow: auto` owner only when it overflows, with the `.gal-pbody` exception. Mark the board bodies explicitly in both paths so ownership survives transitions into or out of overflow. (`stream-view.js:106,475`.)

Replace outer-board pixel capture and restore, reload geometry restoration, board growth and FLIP rect passes, and above-topic resize compensation. Keep any `keep` and `reading` behavior used by inner history scroll owners. Exclude only the migrated `#bcol` from the generic pane pixel snapshot and restore. (`dashboard-board.js:75,82,139,171,896,950,984,1021`; `stream-view.js:181,286`; `dashboard-frames.js:214,224`; `dashboard.js:357,374`.)

Use the current key to decide whether a board update needs staging before inspecting exposure. Limit exposure probes to the current card and clip them to both the card and the usable board scrollport. Preserve the pending badge and manual release for the current card. When a topic or history jump resolves, snap to its containing topic first, then scroll inside that card to the target. Preserve the history loader's target resolution through lazy gaps. (`stream-view.js:130,167,472`; `dashboard-frames.js:284`; `stream-navigation.js:2,43,80`.)

#### Verification

- Run each touched Node test file with `node --test <file>`. Run only those files and skip the full npm suite. Cover short-card stacking, tall-card inner scroll and toolbar clearance, key retention on insertion and reorder, next/previous fallback on closure or removal, non-current updates, pending and manual release behavior, and the wheel gate in both directions. Exercise momentum events as part of the same gesture and verify the controls still work when a frame has focus.
- On a board update fixture, count outer-board geometry reads, forced layouts, and DOM operations before and after a message that changes only a non-current topic's size. Exercise topics above and below the current card, including late frame height reports. Record counts per update and visible latency per `## Measuring`. Require zero outer-board geometry reads and an unchanged current key and top alignment. Collect any alignment assertion reads outside the measured update window.
- Capture a before/after LOOK of the same board showing adjacent short topics, a capped long topic, and toolbar and navigation controls. Also exercise the last short topic, a lazy-history jump and prepend, a reused frame, and maximized topic and entry-panel overlays.
- Run Go tests only on `w5090`. Substitute the worker, minute budget, worktree, and log-directory values below, create the local log directory, and retain the result with the phase evidence.

  ```bash
  bash D:/sys_lock.sh remote w5090 run <who> <min> --sync '<worktree>' -- 'export PATH=$HOME/sdk/go/bin:$PATH; cd agents-plugin-tool && python3 scripts/generate-package-assets.py && go test ./internal/wsdashboard/... ./internal/wswhiteboard/...' > D:/Temp/<purpose>/go-test.log
  ```
