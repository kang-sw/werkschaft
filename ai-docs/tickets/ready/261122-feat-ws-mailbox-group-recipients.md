---
title: "다중 발송 메일: 수신 목록은 접고 mailbox.recipients로 펼쳐 보기, 발신자 이름 표시"
related:
  261114-feat-ws-mailbox-multi-recipient: prerequisite - landed the fan-out path (develop 8d2e16bc4) whose public To/CC body header this ticket replaces
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: fbc8215b7fb98e1c
sage-review-completeness-reviewed: fbc8215b7fb98e1c
---

# Fan-out mail: collapse the recipient list behind mailbox.recipients, show sender labels

## Background

`261114-feat-ws-mailbox-multi-recipient` landed explicit fan-out
(`mailbox.send` with `to: string | string[]`, `cc`, `bcc`). Every copy opens
with the generated public line `[ws mail to: a, b; cc: c]`
(`agents-plugin-tool/internal/mcp/mailbox_fanout.go`, `mailboxAudienceHeader`,
pinned by `TestMailboxAudienceHeader`). With many recipients that line grows
with the audience on every copy, and bare `id:<four words>` addresses leave
receivers asking who a peer is.

Owner direction (2026-11-22): in a fan-out mail
the body is what matters and the recipient list is supplementary. A copy
should show the sender and a short marker; an agent that needs the audience
expands it on demand. A sender who wants recipients to act as a group says so
in the body.

## Decisions

- **Collapsed marker plus on-demand expansion - (1520)=a.** Fan-out copies no
  longer carry the public To/CC list. Each copy carries one short marker line
  naming a group ID and the public recipient count, and a new read-only tool
  `mailbox.recipients(id)` returns the public To/CC list. This replaces the
  (787)=a body-header clause of `261114-feat-ws-mailbox-multi-recipient`
  ("all copies carry public To/CC context" in the body); the rest of (787)=a
  (argument shape, independent fan-out, hidden BCC, raw body, mixed-outcome
  receipt) stands. Rejected: keep the full header on every copy ((787)=a as
  landed); a hybrid that inlines the list up to three public recipients and
  collapses above that.
- **Display labels, not aliases - (1518)=a.** Show a short human-readable
  label next to an address in the `mailbox.recipients` list and for the sender
  of every delivered mail (scalar and fan-out). The label is the sender's
  `agent.spawn` label for a child session, else the lead session's dashboard
  title; truncated to 32 runes; absent when neither exists (address only).
  Addressing, routing and the alias question are unchanged. Rejected:
  name-to-address resolution in `to`; an alias registry store.
- **No recipient-count cap - (1519)=a.** Rejected: a per-call cap with an
  error; a warning in the receipt above a threshold.
- **BCC in the sender receipt - (1522), technical.** Preserve the existing
  sender-facing mixed-outcome receipt, including BCC destinations, while
  keeping BCC out of recipient-visible output and group expansion. Rejected:
  suppressing BCC from the sender's receipt, which would contradict the
  preserved fan-out receipt contract.
- **Marker shape (lead, technical).** The marker sits exactly where the
  landed header sits: after a leading peer-lead note when present, else first;
  a scalar send with no CC or BCC still gets no marker and stays
  byte-identical. Text:
  `[<namespace> group mail <gid>: <n> public recipients; mailbox.recipients lists them]`,
  `<n>` being the To+CC count after de-duplication, identical on every copy.
  Public count over "you + N others" - a BCC recipient is not in the public
  list, so a per-recipient phrasing would either lie to it or reveal that the
  copy was blind; one identical marker also keeps copies comparable. `<gid>`
  is `g-<generation>-<n>`: `<generation>` is 16 lowercase hex characters
  (64 random bits) drawn once per daemon start, `<n>` a decimal counter that
  only grows within that generation. Guarantee: an ID is never reissued within
  one daemon lifetime; across lifetimes two generations collide with
  probability about k^2/2^65 for k daemon starts, which the ticket accepts as
  negligible. A marker that outlives its record (queued mail, history, a
  restart) therefore resolves to the expiry error, not to a later audience.
  Rejected: a clock-derived generation - guaranteed only while the clock never
  steps back; a 32-bit generation (round-2 review: repeats are plausible).
  Remove
  `mailboxAudienceHeader` and its test; the marker gets its own builder and
  test.
- **Group store: memory-only bounded ring (lead, technical).** One record per
  fan-out send: gid, the sender's display address and routing identity,
  sent_at, public To and CC (display address plus the label snapshotted at
  send time, each with its routing identity), and BCC routing identities kept
  only for the access check below. One store per daemon, shared by every
  per-connection MCP server (`agents-plugin-tool/cmd/ws-mcp/proxy.go` builds
  one server per connection), so a recipient on another connection finds the
  sender's record. The send path inserts the record before it enqueues the
  first copy, so no copy can be claimed before its group exists. Two bounds,
  both evicting oldest-first: 1024 groups, and 4 MiB of retained address and
  label bytes summed over all records. The newest record is always kept even
  when it alone exceeds the byte bound, so one maximum-size admitted send
  (an MCP request may reach 64 MiB, `internal/mcpframe/mcpframe.go`) still
  expands; it then evicts every older record. The byte bound leaves the
  settled no-recipient-cap decision and the delivery receipt unchanged. A
  daemon restart empties the store. No file is written, so no storage type,
  capacity knob or on-disk compatibility surface arises. Over a persisted
  Type 1 segment ring - the list is supplementary (owner framing), and
  persistence would add per-send file I/O and three capacity knobs for it.
- **`mailbox.recipients(id)` contract (lead, technical).** Read-only; takes
  `session_key`, `id` (the gid) and `format`. Two gates, in order:
  - Transport admission: the tool joins the standard `agent.spawn` mailbox
    loadouts (`agents-plugin-tool/internal/wsagent/builtin.go`,
    `agents-plugin-tool/internal/mcp/server_inventory.go`) and the lead's tool
    list. A session whose transport does not admit it never reaches the
    membership check; the closed worker allowlist of
    `261115-feat-ws-worker-profile-session-spawn` (ready) stays exact and
    does not gain the tool. Such a worker sees the marker but cannot expand
    it.
  - Membership: the caller must be the group's sender or one of its To, CC or
    BCC recipients, compared by routing identity, never by display string. A
    reply-ID address matches the caller's own reply ID in the mailbox
    registry the send resolved it in. A named address (`slug@scope`) matches
    only a caller that currently owns that queue, compared by the queue's
    full route: the canonical mailbox ring identity (the cache root whose
    ring holds the named queues and their ownership,
    `agents-plugin-tool/internal/wsmailbox/memstore.go`), the resolved
    mailbox store path and the queue name. The same `slug@scope` names
    different queues across worktrees, clones, mailbox homes and cache roots
    (`agents-plugin-tool/internal/wsmailbox/store.go`,
    `agents-plugin-tool/cmd/ws-mcp/proxy.go`). The sender matches by
    the reply identity the send ran under.
  Returns sender, sent_at, To and CC, each as display address plus label
  when known; never BCC, not even to the sender or to a BCC recipient, and
  never a routing identity (store path, registry key). An unknown or expired
  gid and a caller outside the group return the same tool error text
  ("group not found or no longer kept"), so the tool cannot probe whether a
  group exists.
- **Sender labels: a generated legend line, resolved at delivery (lead,
  technical; replaces the round-1 per-message `[sender: ...]` body line).**
  A label never enters a message body or its head. Each delivered batch
  carries one legend line in a region only the formatter writes:
  `[senders: <handle> = <label>; <handle> = <label>]`, one entry per
  distinct sender in the batch that has a label, in first-appearance order;
  no line when none has one. A label is stripped of newlines, `;` and `]`
  before rendering.
  - Placement, hosted: the legend is added when the runner submits mail to
    the model (a steer or a turn), from the senders of exactly the texts in
    that submission, through a label resolver the daemon hands the runner
    (a text's sender is `ClaimedMail.Sender`, or for a held text its head
    handle mapped as `mailSenderReplyID` does). It is never part of a text
    `Drain` returns, so `Record.Mail` strings, held-turn submission and
    Return (`agents-plugin-tool/internal/wsagent/runner_conv.go`,
    `agents-plugin-tool/internal/mcp/lead_mail.go`) never carry a legend or
    a stale title: a held text gets a fresh legend when it is finally
    submitted. When the submission's first text opens with the batch head
    `formatMailBatch` writes, the legend becomes that head's second line;
    otherwise it is a first line followed by a blank line, ahead of the
    first `mail from` head. `parseMailText` drops either (the head region up
    to the first blank line) in this binary and in v0.50.8, and message
    bodies stay byte-identical.
  - Placement, dashboard: every consumer of the batch head in
    `agents-plugin-tool/internal/wsdashboard/conversation_mail_wake.go`
    reads its first line only - the `mailBatchHead` match and the
    `nudgeBatchSize` callers in `collapseBoardNudges` and `boardNudgeMail` -
    so single-message linking and whiteboard-nudge batch grouping survive a
    legend. Body matching is unchanged.
  - Placement, other paths: plain-text `mailbox.recv` prints the legend as
    its first output line. Phase 2's adapters print it once per delivered
    batch, ahead of the mail, in the batch header they already build (the
    Claude/wsflow watcher; Pi's header in
    `agents-plugin-pi/src/spawner-push.ts`, fed by `index.ts`).
  - Source: the daemon resolves each label when it formats a delivery, from
    its in-memory agent and session views keyed by the sender's reply ID
    (for a named sender, its current owner's reply ID, as
    `mailSenderReplyID` does): spawn label, else dashboard title, truncated
    to 32 runes. Nothing is stored in the envelope; mail returned and
    redelivered is re-resolved, so held-mail provenance never matters. The
    structured (`format: "json"`) `mailbox.recv` response fills each
    envelope's existing `label` field with the resolved label in the
    response only; an envelope with neither `From` nor `ReplyTo` keeps its
    stored `Label` as its head handle, as today.
  - Rejected: a per-message `[sender: ...]` body line (round 1 and 2
    reviews: a literal author line is indistinguishable from it, held
    `Record.Mail` strings lose provenance before Return, and v0.50.8's
    Return writes it into `Content`); `mail from id:x "label" (<sent_at>):`
    (breaks the positional head parsers and older binaries); storing the
    label in the envelope (stale on rename, and only matters if text is
    reparsed).
- **Compatibility (expected T1; the implementation confirms it).**
  `mailbox.recipients` is a new tool: T0. The marker and the group store
  change no stored shape: T0. No stored envelope or held-mail shape changes:
  labels are rendered at delivery only. The legend line sits in the batch
  head, which a v0.50.8 `parseMailText` drops whole, so an older binary's
  Return of held mail changes no data: T0. An older dashboard's
  `mailBatchHead` regex is anchored on a one-line head; a two-line head fails
  it, so that wake shows as its own row instead of being linked to its mail
  (the safe fallback of
  `261115-feat-ws-dashboard-conversation-rendering-consolidation`): T1. An
  older adapter reading a structured envelope with both an address and a
  `label` prefers the address: T0. The Result cites source lines for each
  case.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Read `ai-docs/manuals/shipped-surface-boundary.md`,
  `ai-docs/manuals/release-compatibility.md`, `ai-docs/manuals/ws-mcp.md`
  (per-tool description size cap) and `ai-docs/manuals/performance.md`
  before editing under `agents-plugin-tool/`.
- BCC keeps the 261114 meaning: hidden from other recipients, not secret from
  the sender or the operator. No group expansion, marker, recipient history
  entry or recipient dashboard rendering names a BCC address; the existing
  sender-facing receipt still names each destination.
- The interactive send path stays memory-only (performance Rule 5): group
  record insertion is an in-memory append.
- Keep every message body and head byte-identical; labels appear only on
  the generated legend line.
- `agents-plugin/hooks/` and `agents-plugin-wsflow/` edits follow
  ai-docs/manuals/wsflow-mirroring.md; Pi edits regenerate the Pi bundles
  per the Pi package's scripts.
- No live peer mail, daemon replacement or cross-session smoke inside the
  run; the lead and integrator handle the swap.
- Out of scope: name-to-address resolution, alias registry, reply-all,
  dashboard grouping UI, persisting groups across daemon restarts.

## Prior Decisions

- 261114-feat-ws-mailbox-multi-recipient (2026-11-22, Result): "Each recipient gets one ordinary envelope through the shared `deliverMailboxCopy`" — bearing: constrains
- 9bd91e982 (2026-11-22, commit): "one copy's routing or enqueue failure cannot discard the others" — bearing: constrains
- 261117-feat-ws-agent-tools-mailbox-routing (2026-11-18, Result): "A child's address `id:<reply-id>` is its key's reply-id queue" — bearing: constrains
- 569c7817a (2026-11-13, commit): "Preserve streamed wait lifecycle and display-only Pi envelope labels." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | Phase 1 spans agents-plugin-tool/internal/mcp (mailbox_fanout.go, lead_mail.go, mailbox_tools.go, server_inventory.go), internal/wsagent (builtin.go, runner_conv.go submission), cmd/ws-mcp/proxy.go and internal/wsdashboard/conversation_mail_wake.go; Phase 2 spans agents-plugin/hooks/claude-mod/watcher.ts (plus wsflow copy) and agents-plugin-pi/src/spawner-push.ts (batch header) with mailbox-waiter.ts and index.ts. |
| scope.surface | public-interface | mailbox.recipients adds an advertised MCP tool and its caller-visible contract. |
| scope.new_public_symbol | yes | The new mailbox.recipients tool is a public MCP symbol. |
| scope.new_type_contract | yes | mailbox.recipients accepts session_key, id and format and returns sender, sent_at, To and CC with optional labels. |
| scope.test_surface | existing | agents-plugin-tool/internal/mcp/mailbox_fanout_test.go, agents-plugin-tool/internal/mcp/lead_mail_test.go and agents-plugin-tool/internal/wsdashboard/conversation_mail_wake_test.go exist. |
| complexity.reuse_points | confirmed | agents-plugin-tool/internal/mcp/mailbox_fanout.go handles fan-out; agents-plugin-tool/internal/mcp/lead_mail.go formats and parses envelopes. |
| complexity.side_effect_risk | high | Send changes affect independent queues, labels and an evicting shared group store. |
| risk.correctness | high | Identity matching, group eviction and parsing must preserve routing and delivered content. |
| risk.fit | high | Sender labels must coexist with delivery adapters, held mail and dashboard body matching. |
| risk.test | high | Access, eviction, fan-out failure, labels, downgrade and parsing need focused coverage across modules. |
| risk.security_or_contract | high | A public lookup must enforce group membership and avoid exposing BCC. |

## Phases

### Phase 1: Group marker, mailbox.recipients, sender legend (daemon)

Replace the fan-out audience header with the group marker; add the shared
in-memory group store and the `mailbox.recipients` tool with both access
gates; resolve sender labels at delivery and render the legend in the Go
receive paths (hosted submission through the runner's label resolver,
plain-text `mailbox.recv`, and the structured response's `label`); make
every dashboard batch-head consumer read the first line only.
Update the advertised schemas and tool descriptions within the per-tool size
cap.

Verification:

- A delivered fan-out copy's `Content` is the peer-lead note (when present),
  then the marker, then the untrimmed body. Every copy of one send carries
  the identical marker with the correct public count; no generated
  recipient-visible metadata (marker, legend, head) names a recipient other
  than the sender, and the sender's body is delivered unchanged.
- A scalar send with no CC/BCC carries no marker.
- The group record exists before the first copy is enqueued.
- `mailbox.recipients` returns sender, sent_at, To and CC with labels to the
  sender and to To, CC and BCC recipients, including a caller on a separate
  MCP connection; it never returns BCC or a routing identity; a non-member,
  an owner of a same-name `slug@scope` inbox in another worktree or under
  another cache root, an unknown gid and an expired gid all get the same
  error text.
- An `agent.spawn` child with the standard mailbox loadout can call the
  tool; a worker on the closed `261115` allowlist does not see it.
- Count eviction past 1024 groups and byte eviction past 4 MiB are tested
  separately; the newest record survives alone when it exceeds the byte
  bound; a stale gid returns the expiry error after eviction and after a
  simulated restart (new generation).
- Legend: a child sender shows its spawn label (32 runes max), a lead its
  dashboard title, a sender with neither no entry; a batch with no labeled
  sender has no legend line; a label containing a newline, `;` or `]`
  renders safely. Message heads and bodies are byte-identical with and
  without labels. The JSON response carries `label` for an addressed sender
  and keeps the stored handle `Label` of a senderless envelope.
- Hosted: a two-text drain whose first steer succeeds and second is held
  gets a legend on each submission, each naming only its own sender; a held
  text submitted later shows the title current at submission; `Record.Mail`
  never contains a legend. `parseMailText` of a submitted labeled text
  returns the same envelope as today; a body whose first line is literal
  text such as `[senders: x = y]` stays whole.
- Dashboard: a labeled single-message wake links to its mail as before; a
  mixed batch of a labeled peer mail and whiteboard nudges keeps its nudge
  grouping (extend the board-nudge tests).
- The compatibility verdict cites source lines for the v0.50.8 Return of a
  labeled held text, the older dashboard's two-line head, and an older
  adapter reading `label` beside an address, and is recorded in the Result.
- Narrow tests only (`GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go test
  ./internal/mcp -run '<names>'`, plus `./internal/wsmailbox`,
  `./internal/wsagent -run '<names>'` and the targeted
  `./internal/wsdashboard` mail-wake tests); never the whole `internal/mcp`
  or `internal/wsdashboard` package. Record exact commands in the Result.

### Result (e9f9f0cc8) - 2026-11-22

Landed in 10b1c65f9, with the review fix in 59e8209c5 and its tests in
e9f9f0cc8:

- Each fan-out copy opens with
  `[<ns> group mail <gid>: <n> public recipients; mailbox.recipients lists them]`.
  The marker follows the peer-lead note and replaces the audience header. A
  scalar send carries no marker. The group is recorded before the first copy
  is sent.
- `internal/mcp/mailbox_groups.go` adds the group store. It is a process-wide
  in-memory `MailGroupStore`:
  - gids are `g-<16 hex generation>-<n>`;
  - it keeps at most 1024 groups and 4 MiB of retained bytes, evicting the
    oldest first and always keeping the newest;
  - membership compares routes, never display strings. A route is the named
    inbox, the ring identity (new `wsmailbox.Store.RingIdentity`), the
    worktree path and the reply queue;
  - BCC recipients are kept as routes only.
- `mailbox.recipients` returns id, sender, sent_at, To and CC, with labels.
  Every refusal returns the same text: `group not found or no longer kept`.
- Sender legend: `[senders: <handle> = <label>; ...]`.
  - The label is the runner's spawn label, else the dashboard title.
    `cmd/ws-mcp` wires both through `ServerOptions.MailLabels` and
    `Options.MailLegend`.
  - Labels are cleaned: a newline becomes a space, `;` and `]` are dropped,
    and the label is cut to 32 runes.
  - Three paths render it: `mailbox.recv` text output, `label` on the
    structured envelopes, and each hosted steer and turn through
    `LeadMailbox.Legend`. In a hosted submission it sits on the batch head's
    second line, or forms its own first paragraph when there is no head.
  - Drain texts and `Record.Mail` never hold a legend. A child's steered mail
    also keeps Drain's text (`steeredReport.Mail`), so a requeued steer gets a
    fresh legend.
- Dashboard batch-head consumers read only the head's first line. They also
  skip a standalone legend paragraph: without that, a labeled peer mail
  steered into the middle of a batch would break nudge grouping. Only the
  generated shape is skipped: a one-line `[senders: ...]` paragraph closed by
  `]` and a blank line, ahead of a `mail from` head. An author's text cannot
  come before that head. The mcp copy of `trimMailLegend` applies the same
  rule.
- `mailbox.recipients` is in `ChildSafeWSTools` and in the three runtime
  manifests (`>=0.50.8-dev <0.51.0`; the agents-plugin and agents-plugin-pi
  manifests are byte-identical). Its schema and the shortened `mailbox.send`
  description fit the 1450 B cap.

Verification. All commands ran from agents-plugin-tool with the prefix
`GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`:

- `go test ./internal/wsmailbox -count=1`: ok.
- `go test ./internal/mcp -count=1 -run '^(<names>)$'`: ok. `<names>` is all
  154 Test functions in these `_test.go` files: agent_mail_routing,
  agent_tools, hosted_login, lead_mail, mailbox_correspondents,
  mailbox_fanout, mailbox_groups, mailbox_history, mailbox_legend,
  mailbox_recv_concurrency, mailbox_runtime, mailbox_tools, mailbox_wake,
  session_lifecycle, session_mail_access, tool_aliases, tool_schema_size, and
  the five whiteboard_nudges* files.
- `go test ./internal/wsagent -count=1 -run '^(<names>)$'`: ok. `<names>` is
  all 190 Test functions in these `_test.go` files: builtin,
  builtin_lead_fallback, child_safe_mcp_tools, children, idle_children,
  lead_runner, mail_routing, pi_send, wake, worker_holder_recovery, agy,
  agy_lead, agy_output_rate, child_sleep, compaction_advisory,
  lead_claude_approval, pi, pi_worker_recovery.
- `go test ./internal/wsdashboard -count=1 -run
  'TestConversationMailWake|TestConversationBoardNudge'`: ok.
- `go test ./cmd/ws-mcp -count=1 -run
  'TestRuntimeCapabilitiesCommand|TestToolsCommandBareListMatchesToolsList|TestToolsCommandShowMatchesInputSchema|TestAgent|TestProxy|TestHostedCLI'`:
  ok, except `TestProxyWhiteboardLinkHasNoLoginCode`. That test fails the
  same way on the base commit 1356f7f67, so the failure predates this
  change and comes from the environment.
- `go test ./internal/wsrsrc -count=1 -run 'TestPiMirrorUpToDate|TestWsflow'`:
  ok.
- `go vet` on mcp, wsagent, wsmailbox, wsdashboard and cmd/ws-mcp is clean,
  and gofmt is clean on every touched file.
- The tests that pin the verification items:
  - fan-out marker: `TestMailboxFanOut*` and `TestMailboxGroupMarker`;
  - recipient listing: `TestMailboxRecipientsListsThePublicAudienceToEveryMember`;
  - refusals (outsider, same slug in another worktree, another cache root,
    unknown, malformed, evicted, restarted):
    `TestMailboxRecipientsRefusesOutsidersAlike`;
  - record-before-copy: `TestMailboxGroupRecordPrecedesEveryCopy`;
  - tool admission: `TestMailboxRecipientsTransportAdmission`;
  - eviction (count, byte, oversized newest): `TestMailGroupStore*`;
  - labels and legend: `TestCleanMailLabel`, `TestMailLegendLine`,
    `TestMailboxRecvShowsTheSenderLegend`, `TestLeadMailboxLegendPlacement`
    (including a literal `[senders: x = y]` body line, and a turn led by a
    child report), `TestLeadMailboxDrainKeepsTextsWithoutALegend` and
    `TestTrimMailLegendTakesOnlyTheGeneratedLine`;
  - hosted submission: `TestMailLegendRidesEachSubmission`,
    `TestMailLegendRefreshesOnARequeuedSteer` (a requeue and resubmit
    yields exactly one legend, with the current label) and
    `TestSpawnLabelNamesASpawnedChild`;
  - dashboard: `TestConversationBoardNudgeBatchSurvivesASenderLegend` and
    `TestConversationMailWakeTrimsOnlyTheGeneratedLegend`.

Decisions:

- The group store is process-wide by default (`ServerOptions.MailGroups`
  nil), so the per-connection servers share it.
- Recipient labels are snapshotted at send time. The sender's label is read
  at lookup time.
- The `261115` closed allowlist is not in the code yet. The test models it
  with an explicit `WS_MCP_ALLOWED_TOOLS` list. Forward item for `261115`:
  when that allowlist lands, `TestMailboxRecipientsTransportAdmission`
  should drive the real list in place of the explicit one.
- The steer record (`Record.Steered`, written at
  `internal/wsagent/children.go:779`) keeps the labeled steer in
  `steeredReport.Text`. That text is only a match key and is never
  resubmitted:
  - `children.go:724` (steerTaken) compares it with the text the harness
    reports it took;
  - `runner_process.go:196` passes it as `RecoveredSteers`, which
    `agy.go:1018-1022` only replays as that same acknowledgement.

  Both requeue paths, `children.go:709` (a refused steer) and `:766`
  (requeueSteered), take `steeredReport.requeued()`, which returns Drain's
  legend-free text (`Mail`). The turn then adds one legend resolved at
  submission (`runner_conv.go:1299`). Swapping `Text` and `Mail` would make
  a downgraded daemon miss the acknowledgement match and resend the steer.
- A turn's reports and held mail share one legend, placed on the first mail
  text. A child report never gets a legend.

Compatibility verdict: T1. The only degradation is display-only, on an older
dashboard.

- v0.50.8 Return of a labeled held text: T0. This binary never stores a
  legend in held text:
  - `internal/wsagent/runner_conv.go:365,370` hold Drain's `m.Text`;
  - `:1299,1337,1424` use the labeled copy only for the Send and the wake;
  - `:1050-1052` Return receives `rec.Mail`.

  A labeled head would still round-trip: in v0.50.8
  `internal/mcp/lead_mail.go`, `:199,212` route Return through parseMailText,
  and `:235-238` drop the head region up to the first blank line.
- Older dashboard: T1. In v0.50.8
  `internal/wsdashboard/conversation_mail_wake.go`, `:16` anchors the
  batch-head regex to a single line, and `:30,34` match it against the whole
  head region. A two-line head therefore fails, and the wake shows as its own
  row instead of linking to its mail. `:95-96,99,179` make nudge grouping
  fall back the same way. Display only.
- Older adapter reading `label` beside an address: T0.
  `agents-plugin/hooks/claude-mod/watcher.ts:429` and
  `agents-plugin-pi/src/mailbox-waiter.ts:135` (the same lines at v0.50.8)
  prefer `from`/`reply_to` over `label` for the head. Pi's `:145` carries
  `label` only in the structured details.
- Record shape: T0. `steered_reports[].mail` is a new omitempty field. A
  v0.50.8 daemon ignores it and requeues the labeled text, which shows a
  stale label (display only). A record written by v0.50.8 has no `mail` and
  requeues as before. The new tool, the marker (plain body text) and the
  in-memory store change no stored shape.

Minor review findings left open:

- A named sender whose mail crossed worktrees gets its label resolved
  against the recipient's root. This comes from the existing relation
  mapping.
- The runner test drives a fake legend; the real `LeadMailbox.Legend` is
  pinned separately in mcp.
- agy's direct tool list lacks `mailbox.recipients`. That list is outside
  the ticket's loadout scope.

### Phase 2: Sender legend in the Claude/wsflow watcher and Pi

Render the legend from the structured envelopes' `label`, for senders that
have both an address and a label, once per delivered batch ahead of the
mail, in the batch header each adapter already builds:
`agents-plugin/hooks/claude-mod/watcher.ts` (mirrored to the wsflow copy per
ai-docs/manuals/wsflow-mirroring.md) and Pi's live FIFO submission
(`agents-plugin-pi/src/index.ts` maps envelopes, `spawner-push.ts` builds the
header; `mailbox-waiter.ts` as needed). Regenerate the Pi bundles.

Verification:

- Each adapter shows the legend once per batch for labeled senders, no
  legend without one, and unchanged message heads and bodies (reply address,
  senderless label); Pi's test drives the actual FIFO submission path.
- The watcher's and the Pi package's existing test suites pass, run in full
  (install missing dependencies; compare any failure names against
  `develop`); the wsflow mirror test passes. Record exact commands in the
  Result.
