---
title: "감사 수집 지점(필터 이전)과 하네스별 어댑터 (Claude → Codex → agy → Pi)"
parent: 261119-epic-ws-conversation-audit
blocked-by: 261119-feat-ws-audit-policy-event-envelope
related:
  261119-feat-ws-audit-ring-store: "sink: persists what this child captures; may proceed in parallel"
  261119-feat-ws-dashboard-audit-view: "consumer of the ring call ID (601), Phase 1"
  261119-feat-ws-audit-query-api: "consumer: derives request_id, ctx_before and ctx_after from meta.request (Decision 7)"
  261117-feat-ws-harness-capability-catalog: "per-harness@version fixture policy; compaction capability sources"
  261114-feat-ws-conversation-audit-event-store: "superseded source of (598) boundary sources, (601), and the non-blocking writer constraint"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: c545dd031fe981a0
sage-review-completeness-reviewed: c545dd031fe981a0
---

# Audit capture point and per-harness adapters

## Background

Second child of `261119-epic-ws-conversation-audit` ((1331)=a, 2026-11-19).
It turns each hosted harness's stream into the envelope of
`261119-feat-ws-audit-policy-event-envelope`, from a point that still sees
what the display path filters out, and it carries the ring call ID that
joins history rows to audit events.

Verified findings (codex.large read-only research, 2026-11-19):

- All four harnesses (Claude, Codex, agy, Pi) flow through the same `wsagent`
  event path (`agents-plugin-tool/internal/wsagent/stream.go:35-38,169-199`):
  one line mapper per backend, fed line by line.
- Tool call IDs and full args/results exist briefly and are dropped when
  converted for `wslive` display; only previews survive
  (`stream.go:436-463,535-577`; `wslive/toolsummary.go:9-15`). The batch
  tracker holds each call's `id`, but its wire form carries name, clocks,
  failure and an argument preview only.
- The holder filters Claude tool-input/thinking deltas and agy thought chunks
  before they reach the daemon (`wsagent/holder.go:139-141,228-246`). The
  Claude lead and agy launches pass their `RingSkip` patterns; skipped lines
  do not take a sequence or reach clients, though agy can emit a sequenced
  content-free pulse (agents-plugin-tool/internal/wsagent/claude.go#L476-L479,
  agents-plugin-tool/internal/wsagent/agy.go#L192-L196,
  agents-plugin-tool/internal/wsholder/holder.py#L1237-L1274).
  Full capture must tap before that filter.
- The superseded ticket's adapter order (Claude -> Pi -> Codex, "Codex is not
  parsed") is stale: all four adapters exist now.

## Decisions

1. **Adapter order: Claude -> Codex -> agy -> Pi** (parent epic harness order
   (25)). It replaces the stale Claude -> Pi -> Codex order. Each adapter maps
   onto the common envelope ((546)=a). Nothing harness-specific leaks outside
   `meta`/`raw`.
2. **Capture taps before the display reduction and before the holder
   ring-skip filter** (2026-11-19 finding). Where exactly is Decision 5
   ((1732)=a).
3. **Ring call ID ((601), carried).** The ring's per-call record gains an
   optional call ID, so a history row can open its audit events.
   - Today no ring type carries one (`wslive` `ToolCallArgs` holds `Name` and
     `ArgsPreview` only), and the history DOM only has `data-entry`.
   - `ToolCallArgs` gains `id` (omitempty), the same harness tool-call ID the
     audit store records as `call_id`, passed once through the shared
     wsaudit identifier function so both sides of the join carry the same
     normalized key whatever the envelope's metadata size, with literal and
     digested IDs in disjoint forms (ring-store Decision 3, identifier
     normalization). A batch whose calls carry IDs writes
     `CallArgs` even when no call had an argument preview. The `BatchCallCap`
     cap is unchanged.
   - Every adapter fills it where its events carry a call ID. Ring entries
     written before the change have no ID. The UI keeps today's pill for
     those ((602), child 5), with no time-window matching.
   - Additive optional field (T0). Ring readers ignore unknown fields. The
     ring's caps and `/history` behaviour do not change. Emitting the ID as
     `data-call` in the template belongs to child 5.
   - Rejected: a time-window join between ring rows and audit events, which
     is ambiguous for parallel calls and calls that start in the same
     millisecond.
4. **Model-seen cut boundary sources ((598), carried).** Claude's
   `compact_boundary` and Pi's `compaction_start`/`compaction_end` provide the
   boundary. Codex and agy sources are confirmed per adapter phase. agy's
   case is Decision 6 ((1736)=a).
5. **Capture point: a holder-side tee on a separate audit channel ((1732)=a,
   owner 2026-11-19).** The holder forwards each line its `RingSkip` pattern
   skips to the daemon on a separate, unsequenced, versioned audit channel,
   and still keeps those lines out of its own ring. The daemon's capture
   adapter reads them there, ahead of the display reduction.
   - Lines the holder skips while no daemon is attached are lost to the
     audit. This is accepted: it matches the audit's drop-on-error rule, and
     the holder ring stays small for replay. The audit channel has no replay
     and takes no sequence number.
   - Rejected: b, disabling `RingSkip` for audited sessions. Partial deltas
     would consume the holder ring's replay budget.
   - Rejected: c, a daemon-side tap after the filter only. Content that
     exists only in skipped lines (agy thought chunks, possibly Claude
     thinking) would never be captured, so (1730)=b could not be met for agy.
   - Compatibility statement (parent epic holder-version rule: the holder
     protocol is versioned and the new daemon speaks the old holder
     protocol). The audit channel is an additive, version-negotiated
     capability, T0. It must never refuse an old peer, because a hello or
     protocol bump that refuses old connections is T3
     (`ai-docs/manuals/release-compatibility.md`), which needs a minor bump
     and the owner's explicit approval, and neither is requested here.
     - New daemon, old holder: the daemon negotiates down and receives no
       audit channel. Sessions on that holder are captured from the
       post-filter stream only, so skipped-only content (Claude thinking and
       tool-input deltas, agy thought chunks) is missing from the audit.
       Nothing else changes. The old holder keeps running unmodified.
     - Old daemon, new holder: the holder sends audit lines only after the
       daemon negotiated the capability, so the old daemon receives nothing
       it cannot read.
     - The holder is PC-wide and outlives a daemon restart, so a holder older
       than the daemon is an expected, ordinary state, not an error.
   - Ordering: the two channels interleave by arrival, not by a shared
     sequence. The holder stamps each tee'd line with a UTC nanosecond time at
     read and with an anchor: the ring sequence of the last line it kept
     before this one. One holder reader sees both kinds of line in harness
     output order, so the anchor fixes each skipped line's place among the
     sequenced lines without the audit channel taking a sequence of its own.
     At every kept-line batch boundary, the holder sends an audit-channel
     fence `{anchor, tee_count_sent, dropped_count}` closing the interval,
     including a zero-skip interval with `dropped_count` 0, and sends a final
     fence at child exit. Child exit is a separate ring record and need not
     disconnect the holder (agents-plugin-tool/internal/wsholder/holder.py#L1295-L1309).
     The daemon finalizes the interval on its fence, on holder disconnect, or
     after a bounded wait. If overload prevents fence delivery within the
     bound, it closes that source's audit stream as incomplete and records
     the loss, never reordering; the display stream keeps moving. It assigns
     envelope `seq` in source order after finalization.
     Phase 1 verifies late tee delivery against sequenced lines of the same
     turn. Holder output is read and appended in order
     (agents-plugin-tool/internal/wsholder/holder.py#L1237-L1274).
   - Loss: when the holder drops tee lines (no daemon attached, or a full
     send buffer), it counts them in the interval fence. The daemon records
     the loss on the affected assembled event
     as incomplete, or as a standalone loss marker when no event spans it,
     and never requests missing lines. Rejected: ordering by the
     tee timestamp alone. Clocks on two channels do not establish order
     against the sequenced lines.
   - Capture still never blocks the holder reader or the stream mapper (see
     Constraints). A slow or absent daemon drops audit lines, never delays
     the session.
   - Durable identity: each envelope carries a source identity: holder
     generation and ring sequence for a sequenced line; holder generation,
     anchor and interval index for a tee line; or a deterministic key for a
     daemon-synthesized event. Include an event ordinal when one source yields
     multiple envelopes. On open, the ring store recovers the highest retained
     envelope `seq` and last retained source identity per source; capture
     resumes above that sequence and the store skips identities already
     retained. The adapters feed each source's finalized boundary at
     fence/finalization to the ring-store index (ring-store Decision 5): its
     highest source sequence finalized, whether kept or dropped. Replay appends
     only records above the boundary; a record at or below it without a
     retained identity is recorded as loss and never appended late. Extend
     the recovery contract alongside `Sink.Append` in
     agents-plugin-tool/internal/wsaudit/sink.go#L5-L13; holder reattachment
     replays records (agents-plugin-tool/internal/wsagent/holder.go#L253-L262,
     agents-plugin-tool/internal/wsagent/holder.go#L306-L320).
6. **agy compaction is recorded as the cut, labelled as an estimate
   ((1736)=a, owner 2026-11-19).** The daemon infers agy compaction (the
   capability catalog records it as `session/prompt /compact` at idle
   boundaries, detected by a descriptor usage drop or prompt-response
   completion (agents-plugin-tool/internal/wsagent/catalog.go#L503-L518)), so
   the inferred compaction is recorded as the model-seen cut,
   with `meta.estimated: true` on the compaction event so the UI can label it.
   - Rejected: b, no cut for agy (every event stays `model_seen: true` and the
     compaction is a plain `system` event). It shows provably stale context
     as seen.
   - Rejected: c, record the compaction but omit `model_seen` on earlier agy
     events. It leaves the cut unusable for agy without gaining accuracy over
     a, which already labels the estimate.
   - The current detector accepts either a clean ws `/compact` response or a
     sufficiently large same-window usage drop, including when no ws compaction
     is in progress (agents-plugin-tool/internal/wsagent/catalog.go#L503-L518,
     agents-plugin-tool/internal/wsagent/compaction.go#L316-L350). Whether
     agy reports its own automatic compaction explicitly stays unverified and
     is confirmed from a recorded fixture in Phase 3; a qualifying usage drop
     can infer a cut even without an explicit report.
7. **Request usage ((610) source, auto-confirmed 2026-11-22).** Every
   captured event that came from one model request (thinking and tool_call,
   and any other kind the harness attributes to a request) carries
   `meta.request = {id, prev_id, ctx}`: `id` is the harness's request or
   message ID, `ctx` is input + cache_read + cache_creation from that
   request's usage, and `prev_id` is the ID of the request the adapter saw
   immediately before it in the same model conversation, whether or not that
   request produced a captured event. A model conversation is the lead's own
   request chain or one native subagent's chain within the session; Claude
   keys a native subagent's chain by its `parent_tool_use_id`, so a subagent
   request interleaved between a lead call and the lead's next request never
   becomes the lead's `prev_id`. The chain advances once per distinct request
   ID: several stream lines of one assistant message share it. Both `id` and
   `prev_id` pass through the store's shared identifier helper (ring-store
   Decision 3) for every raw value, so long IDs take the digest form and a
   short literal beginning with `sha256:` keeps a disjoint key. `meta.request`
   is reader-required metadata under the ring's 64 KiB non-body bound, whose
   overflow handling collapses only redaction entries and never drops it. The adapter reads usage from every assistant
   message, including text-only ones it does not store (`assistant_text`
   stays outside the capture scope). A harness whose stream carries no
   usage omits `ctx`; one without a request ID omits `meta.request`. The
   query API derives `request_id`, `ctx_before` and `ctx_after` only from
   this object. Rejected: storing `assistant_text` to recover usage, which
   widens the capture scope the policy child settled; and a precomputed
   delta written later, which an append-only store cannot attach to an
   event already written.

## Constraints

- Implementation conventions (AGENTS.md): `agents-plugin-tool/` ->
  `ai-docs/manuals/shipped-surface-boundary.md`; `agents-plugin-tool/internal/`
  -> `ai-docs/manuals/release-compatibility.md`.
- Capture must not block the stream mapper or the holder reader. Hand off
  off the hot path. On a sink error, log and drop the event rather than
  stall the session (carried).
- The holder protocol stays versioned; the new daemon speaks the old holder
  protocol (parent epic Compatibility). Decision 5's audit channel is
  negotiated, additive and T0.
- (601) preserves old ring entry readability, the ring's caps and `/history`
  behaviour.
- Release-gate file-touch rule (parent epic): no per-event O(N) rereads, no
  rewrites; interactive paths never wait on file I/O.
- Per-version recorded stream fixtures follow the capability catalog's
  `harness@version` policy.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

## Prior Decisions

- 261119-feat-ws-audit-policy-event-envelope (2026-11-22, Resolution): "Phase 1 landed in agents-plugin-tool/internal/wsaudit (commits 69cb12676 and c72766e12): v1 harness-neutral Envelope, 11-kind set, pure model-seen compaction cut, daemon-hosted capture policy gate" — bearing: constrains
- 69cb1267 (2026-11-22, commit): "Placed the shared audit envelope, model-seen cut, capture policy gate, and sink interface in a dependency-free internal/wsaudit package" — bearing: supports
- 261114-feat-ws-conversation-audit-event-store (2026-11-19, Resolution): "(601), the (598) boundary sources and the non-blocking capture constraint go to `261119-feat-ws-audit-capture-harness-adapters`." — bearing: supports
- 261117-feat-ws-hosted-compaction-lifecycle (2026-11-19, commit): "per-harness start/end detection drives the harness-neutral compacting badge and a one-shot restore note" — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsagent/stream.go, agents-plugin-tool/internal/wsagent/holder.go, agents-plugin-tool/internal/wslive/toolsummary.go |
| scope.surface | cross-module | holder protocol, wsagent adapters, wslive ring and wsaudit sink cross package boundaries |
| scope.new_public_symbol | unknown | adapter and holder channel API symbols are not specified |
| scope.new_type_contract | yes | version-negotiated holder audit channel and optional ToolCallArgs id |
| scope.test_surface | unknown | fixture and protocol tests are required but test file paths are not specified |
| complexity.reuse_points | confirmed | agents-plugin-tool/internal/wsaudit/sink.go provides Sink.Append; policy.go provides CaptureLine; cut.go provides ApplyModelSeenCut |
| complexity.side_effect_risk | high | capture spans holder output, daemon streams and every hosted session under load |
| risk.correctness | high | cross-channel ordering, event assembly, call IDs and inferred compaction cuts can diverge |
| risk.fit | high | holder negotiation and capture must fit the shared audit policy, sink and ring contracts |
| risk.test | high | four harnesses, versioned fixtures, compatibility, dropped lines and slow sinks need coverage |
| risk.security_or_contract | high | hidden content and full tool arguments cross a versioned holder channel and audit sink |

## Phases

### Phase 1: Capture tap, Claude adapter and ring call ID

After the envelope child has landed: implement the holder-side tee and its
versioned audit channel (Decision 5), the daemon-side capture on that
channel, the Claude stream-json adapter for every kind the policy
admits (thinking, hooks, reminders, the compaction summary and subagent lines
included), the Claude `compact_boundary` cut, the ring call ID (601) for
Claude, and Claude's `meta.request` (Decision 7). Write into the envelope child's sink interface. Phase 1 closes on
in-memory sink tests; the store-integrated check is gated in Phase 5.

Verification: unit tests over recorded Claude stream-json fixtures, one per
kind, including a line the holder's `RingSkip` matches (it arrives over the audit
channel and is assembled, per (1732)=a, not stored as a raw line); a holder
protocol test that an old holder (no audit channel) still works against the
new daemon and a new holder sends no audit lines to an old daemon; a test
that lines skipped while no daemon is attached are dropped without stalling
the holder; ordering of assembled events against the sequenced lines of one
turn; `meta.request` on parallel calls from one assistant message (shared
`id` and `ctx`), on the call after a text-only assistant message (its
`prev_id` names the text-only message), with usage absent (`ctx`
omitted), a native subagent request interleaved between a lead call and the
lead's next request (each chain keeps its own `prev_id`; the recorded
subagent-turn fixture), and repeated lines of one assistant message
(the chain advances once); a ring test that a Claude tools row's calls carry the same IDs as
the captured `tool_call` events, and an older ring entry without IDs still
reads; a slow or failing sink never delays line delivery. Verify a late tee
after the next kept record and a final dropped tee with a fence while display
continues; a zero-skip interval closes, child exit closes the final interval
without holder disconnect, and overload timeout closes the source as
incomplete without reordering. Disconnect marks an unfenced interval
incomplete. With a fake in-memory boundary-keeping sink, fail append of source
sequence 10, append 11, simulate restart and replay: 10 is recorded as loss
and is not appended after 11. A tool-call ID over 256 encoded bytes reaches
the ring's `ToolCallArgs.id` and the audit `call_id` of both the call and its
result in the same shared digest form, and the history row opens its event;
a short literal ID spelled as that digest gets a different key and both
calls open their own events in the same session (ring-store Decision 3,
identifier normalization).

### Result (798e456) - 2026-11-22

Landed on `impl/develop/mural-kitty-yeast` in three commits:

- 94a00415b: tap, adapter, ring call ID.
- 56d98cb52: fence-before-anchor ordering fix.
- 798e456fb: lite review fixes.

- **Holder tee (Decision 5).** `holder.py` negotiates an optional audit
  channel through an `audit` attach field and reply. The hello and the
  protocol stay at 1. Only a holder started with `--ring-skip` takes the
  channel; the others have nothing to tee. Each ring-skipped stdout line is sent as an
  unsequenced `audit` event `{anchor, index, at (UTC ns), data}`, where
  anchor is the last kept stdout sequence. Every kept run is fenced with
  `audit_fence {anchor, next, through, tee_count_sent, dropped_count}`, and
  stdout end sends `final`. A line skipped with no audit connection, or one
  past the 4096-message / 4 MiB audit queue, is only counted. Drops in
  intervals whose fence reached no daemon carry into the next delivered
  fence as `lost_before`. The holder's own ring and replay are unchanged.
- **Daemon capture.** `wsholder` adds:
  - `AttachWith`/`AttachOptions.Audit` and `AttachInfo.Audit`;
  - the Event audit fields, plus a per-connection `Event.Conn` that never
    goes on the wire;
  - client-side `audit_attached` and `audit_detached` markers;
  - `Options.AuditChannel`;
  - `Manager.AuditAttach`. A launch attaches before its process is listed
    to receive events, so `holderChild.start` hands capture the latched
    attach before and after listing.
  `wsagent/audit_capture.go`: one goroutine per process behind a
  4096-item non-blocking queue. Feed and the holder reader only enqueue a
  copy. A kept line waits for the fence that releases it. A fenced interval
  is mapped only after its anchor line, because the holder writes audit
  messages ahead of ring records. Indices that never arrived are recorded as
  loss where they fell: on the open assembled event as `incomplete`,
  otherwise as a standalone `audit_loss`.
  - A lost fence: the next fence closes the stranded interval first,
    unfenced.
  - Nothing drains for 2 s: the source's tee stream closes as
    `fence_timeout`. Later tee lines become one coalesced `late` loss, never
    reordered, and the next fence on the live connection reopens the
    stream.
  - A disconnect or stream end marks an unfenced interval incomplete.
  - Envelope seq is assigned in source order.
- **Recovery contract.** `wsaudit/source.go` adds `SourceID`, `Recovery`
  and `SourcedSink` (`Recover`, `Retained`, `AppendSourced`, `Finalize`,
  `RecordLoss`). Capture resumes above `MaxSeq`. It skips retained
  identities and records a not-retained identity at or below the finalized
  boundary as loss (for a tee line: anchor below the boundary).
- **Claude adapter.** `wsagent/audit_claude.go` maps every kind the policy
  admits: thinking (from the assistant line, filled from the tee'd deltas
  when the line's body is empty, or assembled alone when the line never
  came), tool_call and tool_result, hooks, `<system-reminder>` blocks, the
  compact_boundary (the cut) and its synthetic summary (`compact_summary`),
  subagent lines and task lines, and every other system, control,
  rate-limit, result or `command_lifecycle` line as `system`. An unknown
  line type is kept raw as `other`.
- **Ring call ID (601).** `wsaudit.NormalizeID` implements the ring-store
  Decision 3 identifier normalization (at most 256 JSON-encoded bytes and
  no `sha256:` prefix: kept; anything else: `sha256:<hex>`). The Claude
  batch tracker puts the normalized ID on each call. `wslive` carries it
  unchanged through `BatchCall.ID` and `ToolCallArgs.ID` (`id`, omitempty),
  dropping only over 256 bytes or invalid UTF-8. A batch with IDs writes
  `CallArgs` without previews. `BatchCallCap` and `/history` are unchanged.
- **Wiring.** `wsagent.Options.AuditSink` gives every daemon-hosted process
  whose backend has an adapter a tap (lead: `hosted_lead`, other:
  `hosted_child`). `cmd/ws-mcp` sets neither `AuditSink` nor
  `AuditChannel` yet; the audit store wires both.

Compatibility: T0 for every path, with no hello or protocol bump.

- New daemon, old holder: the old `_authenticate` reads only
  op/token/protocol/id and ignores `audit`. The reply has no `audit`, so
  the client sends no markers and capture never waits on fences
  (`TestAuditChannelOldHolderAttachesWithoutIt`,
  `TestAuditOldHolderCapturesThePostFilterStream`).
- Old daemon, new holder: `push_audit` refuses unless `att.audit` was
  negotiated (`TestAuditChannelNewHolderSendsNothingToAnOldDaemon`,
  `test_a_client_that_offers_no_audit_gets_none`).
- Ring: `id` is omitempty. Old readers ignore it, old entries read with no
  ID, and the ring stays append-only (`TestCallIDsReachTheRingEntry`).

Performance: capture adds no file I/O. It is in memory, and no sink is wired.

Verification (2026-11-22, macOS):

- `python3 -m unittest discover -s agents-plugin-tool/internal/wsholder/tests`:
  44 OK, including `AuditChannelTest`. Covered: no-offer client, anchors,
  fence order, drops while detached, full audit queue.
- `go test ./internal/wsholder/ -run TestAuditChannel`: ok. Real holders,
  all three directions.
- `go test ./internal/wsagent/ ./internal/wsholder/ ./internal/wsaudit/ ./internal/wslive/`:
  all ok.
- `go test -race ./internal/wsagent/ -run 'TestAudit|TestClaudeAudit|TestClaudeRingCallID'`:
  ok.
- Per-kind recorded fixtures: `claude-lead-partial-tool-input`,
  `claude-tool-turn`, `claude-lead-compaction`, `claude-subagent-turn` and
  `claude-lead-background-agent-states`, replayed with the ring-skipped
  deltas as tee lines. The reminder block and the unknown line type are
  inline lines, because no recording holds them.
- `TestAuditRealHolderAttachReachesCapture` runs a real `holder.py` under a
  manager that offers the channel, through the real manager-to-process
  path.
- Each case the phase names has its own test: late tee after the next kept
  line, fence and tees ahead of the anchor line, backlog after attach,
  dropped tee with final fence, zero-skip interval, exit without
  disconnect, fence timeout without reordering, disconnect incomplete,
  failed append 10 then 11 then restart and replay (10 recorded as loss,
  not appended, seq resumes), blocking and failing sink, and an ID over
  256 bytes plus a literal spelled as its digest (ring ID equals the
  call_id of the call and its result; distinct keys).
- Full `go test ./...` at 798e456 (agents-plugin-tool,
  `-skip 'TestComposerCarriesWebPicks|TestDocFrameHandsOverviewKey'`;
  those two run Node on JS assets this change does not touch, and on the
  first full run one hung to the timeout and the other hit Node 25's
  `window is not defined`). The changed packages all pass when run alone.
  The remaining failures:
  - This worktree lacks the gitignored generated packages
    (`internal/wspackage/generated/` holds only `PLACEHOLDER`, dated
    before this work). That causes `TestHostedLauncherSurvivesReplacement…`
    in `cmd/ws-mcp`, `TestDashboardSettings{,UI}Catalog` and the four
    `TestPlaybookPrintGoldenExplore*` goldens in `internal/mcp`.
  - Known develop reds: `TestProxyWhiteboardLinkHasNoLoginCode` and
    `TestWhiteboardConstructionBeforeDashboardBind`.
  - Whiteboard tests in `internal/mcp` (board DB busy after 2 s, a missing
    comment label).
  - `internal/mcp` hit the default 10-minute timeout under machine load.
    Alone with a 30-minute timeout it finishes in 1230 s with only the
    failures above.
  - `TestCodexLeadSyntheticRequiredMCPFailure` failed once under full
    load. It passes 3 of 3 alone and in three full `wsagent` runs.

Lite review (one pass, `code-review-lite`, findings
`6919150e-01-findings-r2.md`):

- Fixed in 798e456fb: the Critical (the attach was dropped before the
  process was listed) and all six Important findings (lost-fence
  stranding, late-loss flood, workers waiting on fences, a test that did
  not discriminate, detached drops lost, missing holder wire-order test).
  The fence-before-anchor Critical was already fixed in 56d98cb52.
- Each new test was checked to fail without its fix.
- One Minor remains: a raw ID of at most 256 bytes with invalid UTF-8 is
  kept by `NormalizeID` but dropped by `wslive`. Claude and Codex IDs are
  ASCII.

Decisions:

- After a fence timeout, the next fence reopens the tee stream. The
  ticket only requires closing it as incomplete; staying closed for the
  rest of the session would lose all later thinking for one hiccup.
- The capability rides an attach field instead of a protocol 2. A
  protocol bump would make an old holder refuse the new daemon's attach
  (T3).
- `NormalizeID` and the `SourcedSink` contract live in new files
  `wsaudit/ident.go` and `wsaudit/source.go`. The ring-store sibling needs
  the same symbols, so reconcile at merge.
- Capture keeps tee'd deltas only as assembly inputs. A complete assistant
  block wins, and the assembly fills an empty thinking body or stands in
  for a missing line.

#### Edition (a35f2e48b) - 2026-11-22

Pre-merge review fixes, one commit each:

- ba7afccff: the holder fences a kept run only when a whole line ends. A
  line cut across reads no longer anchors an interval on its head, which
  stalled capture and produced a false `fence_timeout` loss. Regression:
  `TestAuditRealHolderLineCutAcrossReads` (a real holder, a 1.5 MiB line
  written in two writes).
- 6d0e667a1: a record's durable ordinals count only its own events. The
  assemblies a line ends are appended under their own key (`asm.<seq>`),
  so a replayed line keeps the identities it had live
  (`TestAuditReplayOrdinalsIgnoreCarriedAssemblies`).
- 155706713 and 003316f03: attach and detach markers use reserved queue
  room and are never refused (`TestAuditAttachIsNeverRefusedByAFullQueue`).
- 25e4626d1: a thinking block that lost every delta carries the loss as an
  incomplete event (`TestClaudeAuditTotalLossMarksTheThinking`).
- 0179fb83b: tee lines held for unfenced intervals are bounded at
  `auditPendingBytes` (32 MiB). Past the bound the tee stream closes as
  incomplete until the next fence (`TestAuditPendingTeesAreBounded`).
- 97d031a28: the old-holder test runs the shipped pre-audit holder
  (`testdata/holder-pre-audit.py`).
- a35f2e48b (ring-store Decision 3, as the lead decided): adapters write
  raw harness IDs into envelope `call_id` and parent ID, and the store
  normalizes once, at ingestion. The ring call ID above stays
  `NormalizeID(raw)`, the display key history rows carry. The ring parity
  tests now key events the way the store's ingestion does
  (`TestClaudeAuditEnvelopeIDsStayRaw` covers long and digest-shaped IDs).

#### Edition (ba9896127) - 2026-11-22

Second pre-merge review fixes, one commit each, after merging develop
(8759820cf: `wsholder.Event` keeps both develop's exit byte totals and the
audit fields):

- 530c46074: when one read holds whole kept lines followed by the head of
  a cut line (a record limit under the 64 KiB read), the holder fences the
  whole lines at once. Only the trailing cut run waits for its line's end,
  so capture no longer times out on those lines
  (`test_a_read_ending_in_a_cut_line_head_fences_its_whole_lines`).
- bfec67cbf: control markers never block the holder reader. Past the
  reserve they coalesce to the newest connection's attach and detach. Capture
  applies them ahead of the queue as a resync, recorded as one `queue_full`
  loss, and ignores an attach older than the current connection
  (`TestAuditControlReserveExhaustionNeverBlocks`).
- ba9896127: event identities are fixed by role, not position, superseding
  the positional ordinals of 6d0e667a1. A carried assembly's ordinal is its
  content block's index (also in the end flush), so a replay that cannot
  rebuild a tee-only block leaves the others' identities as they were. A
  line's own events keep their positions, because they are always rebuilt
  from the line (`TestAuditReplayKeepsCarriedSlotsWhenATeeOnlyBlockIsGone`).

#### Edition (630ee328f) - 2026-11-22

Third pre-merge review fixes, one commit each:

- 0fbb2ee39 (Decision 5): the recovery rule is judged by the source record
  that yields an event (its seq, or a tee line's anchor), not by the
  event's identity. A keyed event, such as a carried assembly or a Codex
  call, whose record is at or below the finalized boundary and is not
  retained is recorded as loss. It is never appended late, which would put
  it after its result or a cut with a fresh seq. The end flush is
  positioned at the last kept line mapped. `TestAuditReplayKeepsCarriedSlotsWhenATeeOnlyBlockIsGone`
  now expects the failed carried tool call recorded as loss at
  `asm.<seq>#1`.
- 4f3707582: an id-less Codex item's result takes slot 1, apart from its
  unkeyed call (`TestAuditCodexItemWithoutAnIDKeepsCallAndResultApart`).
- 630ee328f: a detach that overflows alone, while its connection's attach
  still sits in the queue, is applied once the queue drains. It never runs
  before that attach, so the ended connection is not left live
  (`TestAuditOverflowedDetachFollowsItsQueuedAttach`).

#### Edition (5b2f6fb8d) - 2026-11-22

Fourth pre-merge review fix (Decision 5, cold recovery).

**Problem.** Capture judged the recovery rule only against its `Recover`
snapshot, then handed the sink the bare identity. A keyed identity carries
no position. A store still loading the session answers `Recover` with zero.
On such a cold replay, a call whose append had failed could get a fresh seq
after its result and the cut.

**Interface change.** `SourcedSink.AppendSourced(ctx, id, rec, ev)` now also
takes `rec`, the source record that yielded the event.

**Contract.** The sink applies the rule to `rec` against its own ledger.
`SourceID.Covered` is the shared definition of the rule.

**End flush.** It is now positioned after the last kept line, not at it,
like a tee line anchored on that line. That line is usually finalized
already, and the sink judges against its live boundary.

**Tests.** The `boundarySink` fake admits as the store does, and its `cold`
flag makes `Recover` answer zero. Tests:

- the cold subtests of `TestAuditCodexFailedCallIsNeverAppendedAfterTheCut`
  and `TestAuditReplayKeepsCarriedSlotsWhenATeeOnlyBlockIsGone`;
- `TestAuditStreamEndFlushIsNotCoveredByItsOwnBoundary`;
- `TestSourceIDCovered`.

The store in `261119-feat-ws-audit-ring-store` must take `rec` (see the
Phase 5 constraint).

### Phase 2: Codex adapter

Depends on Phase 1. Map the Codex app-server stream onto the envelope,
including its compaction boundary if the stream carries one (confirm from a
recorded fixture), fill the ring call ID, and write `meta.request`
(Decision 7) where the stream carries a request ID, with `ctx` where it
carries usage.

Verification: per-kind fixtures for the supported Codex version range; ring
call ID parity as in Phase 1.

### Result (69149e9) - 2026-11-22

`wsagent/audit_codex.go`, wired through `prepareAudit`. Codex holders have
no RingSkip, so Codex capture is post-filter and never waits on fences.

- Tool items (`commandExecution`, `mcpToolCall`, `fileChange`, `webSearch`,
  `imageView`, `collabAgentToolCall`, `dynamicToolCall`): a tool_call at
  `item/started` (or at completion when the start was not seen) and a
  tool_result at `item/completed`. Both carry
  `call_id = NormalizeID(item.id)`, and the result carries output, status,
  exit code and error.
- `reasoning`: thinking. The recorded items are encrypted with an empty
  summary and content, so they are captured as `signature_only`.
- Also mapped: `hook/started` and `hook/completed` (hook),
  `subAgentActivity` (subagent), server requests (system, `call_id` from
  `itemId`), responses, and every other notification (system). Message
  and output deltas are recognized and dropped, because the completed item
  carries the whole body.
- **Compaction boundary, confirmed from codex-0.170.0-compaction-probe-1:**
  the `contextCompaction` item's `item/completed` is the explicit cut. No
  `thread/compacted` notification appears in the 0.160.0 or 0.170.0
  recordings.
- Ring call ID: `codexMapper`'s batch tracker sets `callIDs`.

Verification: `TestCodexAuditCapturesEveryKindFromRecordings` runs all 12
`codex-0.160.0-*` and `codex-0.170.0-*` recordings. Every admitted kind is
present, no user, assistant or other event is captured, each result joins
a call, and the cut is explicit. `TestCodexRingCallIDMatchesTheAuditCallID`
covers a digested long ID and a literal short ID. Both pass under `-race`,
and the full `wsagent` package passes.

#### Edition (2b3b0211f) - 2026-11-22

Correction to the compaction bullet above: the 0.170.0 probes do not
confirm the boundary. Both stop at a `thread/start` configuration error
(line 6) before any turn. The boundary is confirmed for 0.160.0 only, from
the hosted lead's manual compaction in `codex-0.160.0-hosted-lead.jsonl`:
the `contextCompaction` item starts at line 78 and completes at line 81.
Its completion is the explicit cut. For 0.170.0 the boundary is
unverified. `TestCodexAuditCompactionBoundaryPerVersion` pins this
exactly, per version: one compaction event, at line 81, for 0.160.0; none,
and no cut, for 0.170.0. The pooled recordings test no longer asserts a
cut.

#### Edition (a35f2e48b) - 2026-11-22

Correction to the tool-items bullet above: envelopes carry the raw
`call_id = item.id` (and the raw parent ID on `subAgentActivity`). The
audit store normalizes them once, at ingestion (ring-store Decision 3).
The ring call ID stays `NormalizeID(item.id)`.

#### Edition (ba9896127) - 2026-11-22

A tool call's durable identity is its item: the keyed `SourceID`
`call.<NormalizeID(item.id)>`, whichever record yields the call. When a
replay finds that `item/started` has wrapped out of the ring, the completed
item synthesizes the call. That call finds the live one retained, and the
result stays the completion's ordinal 0. Before, the result shifted to
`#1`: a clean replay recorded a false loss, and a failed result was recorded
as two losses. A live call whose append failed is appended on replay,
because keyed identities fall outside the boundary rule, as carried
assemblies do (`TestAuditCodexReplayAfterTheStartedItemWrapped`).

#### Edition (0fbb2ee39) - 2026-11-22

Correction to the Edition above: a live call whose append failed is not
appended on replay. The recovery rule now holds keyed identities to the
place of the record that yields them, so at or below the finalized
boundary the call is recorded as loss
(`TestAuditCodexFailedCallIsNeverAppendedAfterTheCut`: a failed call, its
retained result, and a compaction cut, replayed with and without
`item/started`). An item without an id keeps the call at its record's
slot 0, and its result takes slot 1 (4f3707582).

### Phase 3: agy adapter

Depends on Phase 2. Map the agy ACP stream onto the envelope, including
thought chunks per (1730)=b and the Decision 5 tee, the inferred compaction as
the cut with `meta.estimated: true` (Decision 6),
the ring call ID where agy carries a tool-call ID, and `meta.request`
(Decision 7) where the stream carries a request ID, with `ctx` where it
carries usage.

Verification: per-kind fixtures, including a thought chunk and a
`/compact` completion that yields the estimated cut (`meta.estimated: true`,
earlier events `model_seen: false`); ring call ID parity.

### Phase 4: Pi adapter

Depends on Phase 3. Map Pi's events onto the envelope, including
`compaction_start`/`compaction_end` as the cut, fill the ring call ID
where Pi's events carry one, and write `meta.request` (Decision 7) where
its events carry a request ID, with `ctx` where they carry usage.

Verification: per-kind fixtures, including a compaction pair; ring call ID
parity.

### Phase 5: Store-integrated restart and replay verification

After Phase 1 and the store in `261119-feat-ws-audit-ring-store` land, run
the integrated check here, independently of Phases 2–4. Append, restart the
daemon, replay the holder, and append again: retained source identities are
not duplicated and envelope sequences resume above the recovered maximum.
Fail append of source sequence 10, append 11, restart and replay: 10 is
recorded as loss and is not appended after 11.

Constraints:

- **Writer admission uses the source record position.** The store's writer
  admits each `AppendSourced` (Decision 5) by the source record position
  `rec` it is handed, not by the identity `id`. It checks
  `rec.Covered(boundary)` against the recovered ledger, including
  boundaries finalized since.
- **Reason.** A keyed identity (a Codex call `call.<id>`, a carried
  assembly `asm.*`) has no position. Capture's own check uses its `Recover`
  snapshot, which is zero for a ring still loading. Trusting that snapshot
  lets a failed call get a fresh seq after its result and cut, marked
  model-seen.
- **Wiring.** Wiring the adapter to the store means `Store.AppendSourced`
  takes `rec` (5b2f6fb8d changed the interface).

Additional verification: a cold-replay test. A call whose append failed, its
retained result, and a compaction cut are replayed while `Recover` still
answers zero: against the real store with the session ring not yet loaded,
or with an equivalent delayed load. The call is recorded as loss and never
appended after the result and the cut. Run it for a keyed Codex call and a
keyed Claude carried assembly.
