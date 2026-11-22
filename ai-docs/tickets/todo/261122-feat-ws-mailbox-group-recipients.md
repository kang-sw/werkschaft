---
title: "다중 발송 메일: 수신 목록은 접고 mailbox.recipients로 펼쳐 보기, 발신자 이름 표시"
related:
  261114-feat-ws-mailbox-multi-recipient: prerequisite - landed the fan-out path (develop 8d2e16bc4) whose public To/CC body header this ticket replaces
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

Owner direction (2026-11-22, board topic `mailbox-naming`): in a fan-out mail
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
- **Marker shape (lead, technical).** The marker sits exactly where the
  landed header sits: after a leading peer-lead note when present, else first;
  a scalar send with no CC or BCC still gets no marker and stays
  byte-identical. Text:
  `[<namespace> group mail <gid>: <n> public recipients; mailbox.recipients lists them]`,
  `<n>` being the To+CC count after de-duplication, identical on every copy.
  Public count over "you + N others" - a BCC recipient is not in the public
  list, so a per-recipient phrasing would either lie to it or reveal that the
  copy was blind; one identical marker also keeps copies comparable. `<gid>`
  is `g-` plus 8 lowercase hex characters from a random source, unique within
  the live store. Remove `mailboxAudienceHeader` and its test; the marker gets
  its own builder and test.
- **Group store: memory-only bounded ring (lead, technical).** One record per
  fan-out send: gid, sender address, sent_at, public To and CC (canonical
  address plus the label snapshotted at send time), and BCC canonical
  addresses kept only for the access check below. Records live in daemon
  memory in a FIFO ring of 1024 groups; the oldest record drops first; a
  daemon restart empties it. No file is written, so no storage type, capacity
  knob or on-disk compatibility surface arises. Over a persisted Type 1
  segment ring - the list is supplementary (owner framing), and persistence
  would add per-send file I/O and three capacity knobs for it.
- **`mailbox.recipients(id)` contract (lead, technical).** Read-only; takes
  `session_key` and `id` (the gid). Allowed callers: the group's sender and
  any of its To, CC or BCC recipients, matched against the caller's own
  mailbox addresses (its reply ID and any named inbox it currently owns).
  Returns sender, sent_at, To and CC, each as address plus label when known;
  never BCC, not even to the sender or to a BCC recipient. An unknown or
  expired gid and a caller outside the group return the same tool error text
  ("group not found or no longer kept"), so the tool cannot probe whether a
  group exists. Supports `format: "json"`.
- **Sender label placement (lead, technical).** The label goes on its own
  line, not into the `mail from <handle> (<sent_at>):` head.
  `parseMailText` (`internal/mcp/lead_mail.go`) and the dashboard's
  `conversation_mail_wake.go` parse that head positionally, and a held text
  can reach an older binary after a downgrade (the reason the relation line
  is its own line, per `formatMailEnvelope`'s comment). Render it as
  `[sender: <label>]` immediately after the relation line (or first in the
  body when there is no relation line). The current binary's
  `trimRelationLine`-style trimming and `parseMailText` read it back (label
  into `Envelope.Label`, the existing display-only field), and the dashboard
  body match in `conversation_mail_wake.go` trims it like the relation line.
  The label is resolved by the daemon at send time from its agent and
  dashboard-title records and carried in the existing `Envelope.Label`
  field; when `From` and `ReplyTo` are both empty, `Label` keeps its current
  role as the head handle and no `[sender: ...]` line is added. Rejected:
  `mail from id:x "label" (<sent_at>):` - breaks the positional parsers and
  older binaries.
- **Compatibility.** `mailbox.recipients` is a new tool: T0. The marker and
  the group store change no stored shape: T0. The `[sender: ...]` line is
  forward T0; a held text read by an older binary after a downgrade keeps the
  line in the body (T1 display-degraded, the relation-line precedent) and
  changes no data. The envelope `Label` field already exists in every
  supported version (`internal/wsmailbox/store.go`).

## Constraints

- Read `ai-docs/manuals/shipped-surface-boundary.md`,
  `ai-docs/manuals/release-compatibility.md`, `ai-docs/manuals/ws-mcp.md`
  (per-tool description size cap) and `ai-docs/manuals/performance.md`
  before editing under `agents-plugin-tool/`.
- BCC keeps the 261114 meaning: hidden from other recipients, not secret from
  the sender or the operator. No tool output, marker, history entry or
  dashboard rendering names a BCC address.
- The interactive send path stays memory-only (performance Rule 5): group
  record insertion is an in-memory append.
- Keep scalar sends byte-identical apart from the new `[sender: ...]` line,
  which appears only when a label exists.
- No live peer mail, daemon replacement or cross-session smoke inside the
  run; the lead and integrator handle the swap.
- Out of scope: name-to-address resolution, alias registry, reply-all,
  dashboard grouping UI, persisting groups across daemon restarts.

## Phases

### Phase 1: Group marker, mailbox.recipients, sender labels

Replace the fan-out audience header with the group marker; add the in-memory
group ring and the `mailbox.recipients` tool; resolve and render sender
labels per the Decisions. Update the advertised schemas and tool descriptions
within the per-tool size cap.

Verification:

- A fan-out copy carries the peer-lead note (when present), then the marker,
  then the untrimmed body; every copy of one send carries the identical
  marker with the correct public count; no copy names any recipient.
- A scalar send with no CC/BCC carries no marker.
- `mailbox.recipients` returns sender, sent_at, To and CC with labels to the
  sender and to To, CC and BCC recipients; it never returns BCC; a
  non-member caller and an unknown gid get the same error text; the ring
  drops its oldest record past 1024 groups.
- A child's mail shows `[sender: <spawn label, 32 runes max>]`; a lead with a
  dashboard title shows that title; a sender with neither shows no line.
  `parseMailText` round-trips the line into `Envelope.Label`, and the
  dashboard mail-wake body match still pairs a delivered mail with its ring
  entry.
- Narrow tests only (`GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go test
  ./internal/mcp -run '<names>'`, plus `./internal/wsmailbox` and the
  targeted `./internal/wsdashboard` mail-wake tests); never the whole
  `internal/mcp` or `internal/wsdashboard` package. Record exact commands in
  the Result.
